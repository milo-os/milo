// Package core implements the core quota controllers that manage AllowanceBuckets,
// ResourceClaims, ResourceGrants, and ResourceRegistrations.
//
// The ResourceRegistrationController validates ResourceRegistrations and manages
// their Active status condition. It ensures that resource type configurations
// are valid before allowing them to be used in the quota system.
package core

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// ResourceRegistrationController reconciles ResourceRegistration objects.
type ResourceRegistrationController struct {
	Scheme  *runtime.Scheme
	Manager mcmanager.Manager
}

// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourceregistrations,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourceregistrations/status,verbs=get;update;patch

// Reconcile reconciles a ResourceRegistration object by validating it and updating the
// status to reflect whether the registration is active and the resource type
// can be managed by the quota system.
//
// The current implementation has no cross-system validation to determine if the
// resource type being registered exists in the system overall.
// Once a common service is created that tracks all existing resource types,
// additional validation can be added.
func (r *ResourceRegistrationController) Reconcile(ctx context.Context, req mcreconcile.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	// ResourceRegistrations only exist in the core control plane, but we log cluster for consistency
	if req.ClusterName != "" {
		logger = logger.WithValues("cluster", req.ClusterName)
		ctx = log.IntoContext(ctx, logger)
	}

	// Get the cluster-specific client from the multicluster manager
	cluster, err := r.Manager.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.ClusterName, err)
	}
	clusterClient := cluster.GetClient()

	// Fetch the ResourceRegistration
	var registration quotav1alpha1.ResourceRegistration
	if err := clusterClient.Get(ctx, req.NamespacedName, &registration); err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("ResourceRegistration not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ResourceRegistration: %w", err)
	}

	// Handle deletion (TODO: add finalizer for cleanup)
	if !registration.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Update status based on validation
	return ctrl.Result{}, r.updateRegistrationStatus(ctx, clusterClient, &registration)
}

// updateRegistrationStatus validates the registration and updates its status.
func (r *ResourceRegistrationController) updateRegistrationStatus(ctx context.Context, clusterClient client.Client, registration *quotav1alpha1.ResourceRegistration) error {
	originalStatus := registration.Status.DeepCopy()

	// Update observed generation
	registration.Status.ObservedGeneration = registration.Generation

	// Initialize or update active condition
	activeCondition := r.initializeActiveCondition(registration)

	// All validation is handled by OpenAPI schema and CEL rules in the CRD:
	// - Required fields: OpenAPI schema validation
	// - ClaimingResources duplicates: CEL rule validation
	// - Pattern validation: OpenAPI schema validation
	// ResourceRegistration is valid if it passes CRD validation
	r.setActiveCondition(activeCondition)

	// Apply the updated condition to the status
	apimeta.SetStatusCondition(&registration.Status.Conditions, *activeCondition)

	return r.updateStatusIfChanged(ctx, clusterClient, registration, originalStatus)
}

// initializeActiveCondition gets or creates the active condition.
func (r *ResourceRegistrationController) initializeActiveCondition(registration *quotav1alpha1.ResourceRegistration) *metav1.Condition {
	activeCondition := apimeta.FindStatusCondition(registration.Status.Conditions, quotav1alpha1.ResourceRegistrationActive)
	if activeCondition == nil {
		return &metav1.Condition{
			Type:               quotav1alpha1.ResourceRegistrationActive,
			Status:             metav1.ConditionFalse,
			Reason:             quotav1alpha1.ResourceRegistrationPendingReason,
			Message:            "The registration is pending validation",
			ObservedGeneration: registration.Generation,
		}
	}

	activeCondition = activeCondition.DeepCopy()
	activeCondition.ObservedGeneration = registration.Generation
	return activeCondition
}

// setActiveCondition sets the condition to reflect successful validation.
func (r *ResourceRegistrationController) setActiveCondition(condition *metav1.Condition) {
	condition.Status = metav1.ConditionTrue
	condition.Reason = quotav1alpha1.ResourceRegistrationActiveReason
	condition.Message = "The registration is active and resource grants and claims can now be created for this resource type."
}

// updateStatusIfChanged updates the status only if it has changed.
func (r *ResourceRegistrationController) updateStatusIfChanged(ctx context.Context, clusterClient client.Client, registration *quotav1alpha1.ResourceRegistration, originalStatus *quotav1alpha1.ResourceRegistrationStatus) error {
	if !equality.Semantic.DeepEqual(originalStatus, &registration.Status) {
		if err := clusterClient.Status().Update(ctx, registration); err != nil {
			return fmt.Errorf("failed to update ResourceRegistration status: %w", err)
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
// ResourceRegistrations are centralized resource type definitions that only exist in the local cluster (Milo API server).
func (r *ResourceRegistrationController) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		For(&quotav1alpha1.ResourceRegistration{},
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Named("resource-registration").
		Complete(r)
}
