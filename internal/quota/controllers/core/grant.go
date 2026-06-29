// Package core implements the core quota controllers that manage AllowanceBuckets,
// ResourceClaims, ResourceGrants, and ResourceRegistrations.
//
// The ResourceGrantController validates ResourceGrants against ResourceRegistrations
// and manages their Active status condition. It ensures that all resource types
// referenced in grants have valid registrations before marking grants as active.
package core

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.miloapis.com/milo/pkg/quota/validation"
	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// ResourceGrantController reconciles a ResourceGrant object.
type ResourceGrantController struct {
	Scheme         *runtime.Scheme
	Manager        mcmanager.Manager
	GrantValidator *validation.ResourceGrantValidator
}

// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourcegrants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourcegrants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourceregistrations,verbs=get;list;watch
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=allowancebuckets,verbs=get;create

// Reconcile manages the lifecycle of ResourceGrant objects across all control planes.
func (r *ResourceGrantController) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if req.ClusterName != "" {
		logger = logger.WithValues("cluster", req.ClusterName)
	}

	cluster, err := r.Manager.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.ClusterName, err)
	}
	clusterClient := cluster.GetClient()

	// Fetch the ResourceGrant
	var grant quotav1alpha1.ResourceGrant
	if err := clusterClient.Get(ctx, req.NamespacedName, &grant); err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("ResourceGrant not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ResourceGrant: %w", err)
	}

	// Update observed generation and conditions
	if err := r.updateResourceGrantStatus(ctx, clusterClient, &grant); err != nil {
		return ctrl.Result{}, err
	}

	if apimeta.IsStatusConditionTrue(grant.Status.Conditions, quotav1alpha1.ResourceGrantActive) {
		if err := r.ensureBucketsFromGrant(ctx, clusterClient, &grant); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to pre-create allowance buckets: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

// updateResourceGrantStatus updates the status of the ResourceGrant.
func (r *ResourceGrantController) updateResourceGrantStatus(ctx context.Context, clusterClient client.Client, grant *quotav1alpha1.ResourceGrant) error {
	logger := log.FromContext(ctx)
	originalStatus := grant.Status.DeepCopy()

	// Always update the observed generation in the status to match the current generation of the spec.
	grant.Status.ObservedGeneration = grant.Generation

	if validationErrs := r.GrantValidator.Validate(ctx, grant, validation.ControllerValidationOptions()); len(validationErrs) > 0 {
		logger.Info("ResourceGrant validation failed", "errors", validationErrs.ToAggregate())

		apimeta.SetStatusCondition(&grant.Status.Conditions, metav1.Condition{
			Type:    quotav1alpha1.ResourceGrantActive,
			Status:  metav1.ConditionFalse,
			Reason:  quotav1alpha1.ResourceGrantValidationFailedReason,
			Message: fmt.Sprintf("Validation failed: %v", validationErrs.ToAggregate()),
		})

		// Use the same change detection for validation failures
		return r.updateStatusIfChanged(ctx, clusterClient, grant, originalStatus)
	}

	// Set active condition
	r.setActiveCondition(grant)

	// Only update the status if it has changed
	return r.updateStatusIfChanged(ctx, clusterClient, grant, originalStatus)
}

// setActiveCondition sets the active condition on the grant.
func (r *ResourceGrantController) setActiveCondition(grant *quotav1alpha1.ResourceGrant) {
	condition := metav1.Condition{
		Type:               quotav1alpha1.ResourceGrantActive,
		Status:             metav1.ConditionTrue,
		Reason:             quotav1alpha1.ResourceGrantActiveReason,
		Message:            "ResourceGrant is active",
		ObservedGeneration: grant.Generation,
	}
	apimeta.SetStatusCondition(&grant.Status.Conditions, condition)
}

// updateStatusIfChanged updates the status only if it has actually changed.
// This prevents unnecessary API server writes and audit log entries.
func (r *ResourceGrantController) updateStatusIfChanged(ctx context.Context, clusterClient client.Client, grant *quotav1alpha1.ResourceGrant, originalStatus *quotav1alpha1.ResourceGrantStatus) error {
	// Use semantic equality to detect actual changes in status
	// This compares all fields including conditions, observed generation, etc.
	if equality.Semantic.DeepEqual(&grant.Status, originalStatus) {
		// Status hasn't changed, skip update
		return nil
	}

	// Status has changed, update it
	if err := clusterClient.Status().Update(ctx, grant); err != nil {
		return fmt.Errorf("failed to update ResourceGrant status: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
// ResourceGrants can exist in both the local cluster and project control planes for delegated quota management.
//
// Note: We don't watch ResourceRegistrations because the admission plugin validates that
// all resource types are already registered before allowing grant creation.
func (r *ResourceGrantController) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		For(&quotav1alpha1.ResourceGrant{},
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(true),
		).
		Named("resource-grant").
		Complete(r)
}

// ensureBucketsFromGrant pre-creates dimensionless allowance buckets when a grant becomes active.
// This allows consumers to see their quota limits immediately without waiting for the first claim.
// These buckets are identical to claim-created buckets and follow the same naming convention.
func (r *ResourceGrantController) ensureBucketsFromGrant(ctx context.Context, clusterClient client.Client, grant *quotav1alpha1.ResourceGrant) error {
	logger := log.FromContext(ctx).WithValues(
		"grant", grant.Name,
		"grantNamespace", grant.Namespace,
		"consumerKind", grant.Spec.ConsumerRef.Kind,
		"consumerName", grant.Spec.ConsumerRef.Name,
	)

	logger.Info("Pre-creating AllowanceBuckets from active grant",
		"allowanceCount", len(grant.Spec.Allowances))

	// For each allowance in the grant, create a dimensionless bucket if it doesn't exist
	for _, allowance := range grant.Spec.Allowances {
		// Generate bucket name using helper functions from bucket controller
		bucketName := generateAllowanceBucketName(allowance.ResourceType, grant.Spec.ConsumerRef)
		bucketNamespace := getBucketNamespace(grant.Spec.ConsumerRef)

		logger.Info("Checking if bucket needs pre-creation",
			"bucket", bucketName,
			"namespace", bucketNamespace,
			"resourceType", allowance.ResourceType)

		// Check if bucket already exists
		var existingBucket quotav1alpha1.AllowanceBucket
		err := clusterClient.Get(ctx, types.NamespacedName{
			Name:      bucketName,
			Namespace: bucketNamespace,
		}, &existingBucket)

		if err == nil {
			// Bucket already exists, skip
			logger.V(2).Info("Bucket already exists, skipping pre-creation",
				"bucket", bucketName,
				"namespace", bucketNamespace)
			continue
		}

		if !apierrors.IsNotFound(err) {
			logger.Error(err, "Failed to check bucket existence",
				"bucket", bucketName,
				"namespace", bucketNamespace)
			return fmt.Errorf("failed to check bucket existence: %w", err)
		}

		// Create dimensionless bucket
		bucket := &quotav1alpha1.AllowanceBucket{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bucketName,
				Namespace: bucketNamespace,
				Labels: map[string]string{
					"quota.miloapis.com/consumer-kind": grant.Spec.ConsumerRef.Kind,
					"quota.miloapis.com/consumer-name": grant.Spec.ConsumerRef.Name,
				},
			},
			Spec: quotav1alpha1.AllowanceBucketSpec{
				ConsumerRef:  grant.Spec.ConsumerRef,
				ResourceType: allowance.ResourceType,
				// No dimensions specified - this is a dimensionless bucket
			},
		}

		logger.Info("Creating pre-created AllowanceBucket",
			"bucket", bucketName,
			"namespace", bucketNamespace,
			"resourceType", allowance.ResourceType)

		if err := clusterClient.Create(ctx, bucket); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				logger.Error(err, "Failed to create bucket",
					"bucket", bucketName,
					"namespace", bucketNamespace)
				return fmt.Errorf("failed to pre-create bucket %s: %w", bucketName, err)
			}
			logger.Info("Bucket already exists (race condition)",
				"bucket", bucketName,
				"namespace", bucketNamespace)
		} else {
			logger.Info("Successfully pre-created dimensionless AllowanceBucket",
				"bucket", bucketName,
				"namespace", bucketNamespace,
				"resourceType", allowance.ResourceType,
				"consumer", grant.Spec.ConsumerRef.Name)
		}
	}

	return nil
}
