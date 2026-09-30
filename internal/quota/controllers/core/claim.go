// Package core implements the core quota controllers that manage AllowanceBuckets,
// ResourceClaims, ResourceGrants, and ResourceRegistrations.
//
// The ResourceClaimController manages the overall status of ResourceClaims by
// aggregating individual request allocation results from the AllowanceBucketController.
// It does not make quota decisions itself - that responsibility belongs entirely
// to the AllowanceBucketController to ensure consistency and avoid races.
package core

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontroller "sigs.k8s.io/multicluster-runtime/pkg/controller"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// ResourceClaimController reconciles a ResourceClaim object and is
// responsible for evaluating resource claims against available quota.
type ResourceClaimController struct {
	Scheme                  *runtime.Scheme
	Manager                 mcmanager.Manager
	MaxConcurrentReconciles int
}

// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourceclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourceclaims/status,verbs=get;update;patch

// Reconcile reconciles a ResourceClaim object by updating the overall Granted condition
// based on individual request allocations made by the AllowanceBucketController.
// This controller watches ResourceClaims across all control planes.
func (r *ResourceClaimController) Reconcile(ctx context.Context, req mcreconcile.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	if req.ClusterName != "" {
		logger = logger.WithValues("cluster", req.ClusterName)
		ctx = log.IntoContext(ctx, logger)
	}

	cluster, err := r.Manager.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.ClusterName, err)
	}
	clusterClient := cluster.GetClient()

	// Fetch the ResourceClaim
	var claim quotav1alpha1.ResourceClaim
	if err := clusterClient.Get(ctx, req.NamespacedName, &claim); err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("ResourceClaim not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ResourceClaim: %w", err)
	}

	if !claim.DeletionTimestamp.IsZero() {
		logger.Info("ResourceClaim is being deleted, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Update the overall claim condition based on individual request allocations
	if err := r.updateOverallClaimConditionFromAllocations(ctx, clusterClient, &claim); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update overall claim condition: %w", err)
	}

	return ctrl.Result{}, nil
}

// updateOverallClaimConditionFromAllocations updates the overall Granted condition
// based on the status of individual request allocations.
func (r *ResourceClaimController) updateOverallClaimConditionFromAllocations(ctx context.Context, clusterClient client.Client, claim *quotav1alpha1.ResourceClaim) error {

	// Initialize allocation map for tracking which requests have been processed
	allocationMap := make(map[string]quotav1alpha1.ResourceClaimAllocationStatus)
	for _, allocation := range claim.Status.Allocations {
		allocationMap[allocation.ResourceType] = allocation
	}

	var grantedCount, deniedCount, pendingCount int
	var totalRequests = len(claim.Spec.Requests)

	// Check the status of each request by resource type
	for _, request := range claim.Spec.Requests {
		allocation, exists := allocationMap[request.ResourceType]
		if !exists {
			// No allocation status exists for this request - mark as pending
			pendingCount++
			continue
		}

		switch allocation.Status {
		case quotav1alpha1.ResourceClaimAllocationStatusGranted:
			grantedCount++
		case quotav1alpha1.ResourceClaimAllocationStatusDenied:
			deniedCount++
		case quotav1alpha1.ResourceClaimAllocationStatusPending:
			pendingCount++
		default:
			// Unknown status - treat as pending
			pendingCount++
		}
	}

	// Determine overall condition based on allocation results
	var conditionStatus metav1.ConditionStatus
	var reason, message string

	if grantedCount == totalRequests {
		// All requests granted
		conditionStatus = metav1.ConditionTrue
		reason = quotav1alpha1.ResourceClaimGrantedReason
		message = fmt.Sprintf("All %d resource requests have been granted", totalRequests)
	} else if deniedCount > 0 {
		// At least one request denied
		conditionStatus = metav1.ConditionFalse
		reason = quotav1alpha1.ResourceClaimDeniedReason
		message = "Insufficient quota resources. Contact your account administrator to review quota limits and usage."
	} else {
		// Some requests still pending
		conditionStatus = metav1.ConditionFalse
		reason = quotav1alpha1.ResourceClaimPendingReason
		message = fmt.Sprintf("Awaiting capacity evaluation: %d granted, %d pending", grantedCount, pendingCount)
	}

	return r.updateOverallClaimCondition(ctx, clusterClient, claim, conditionStatus, reason, message)
}

// updateOverallClaimCondition updates the overall Granted condition using Server Side Apply.
func (r *ResourceClaimController) updateOverallClaimCondition(ctx context.Context, clusterClient client.Client, claim *quotav1alpha1.ResourceClaim,
	status metav1.ConditionStatus, reason, message string) error {

	changed := apimeta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:    quotav1alpha1.ResourceClaimGranted,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
	if !changed {
		return nil
	}

	// Create minimal claim object for Server Side Apply
	patchClaim := &quotav1alpha1.ResourceClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "quota.miloapis.com/v1alpha1",
			Kind:       "ResourceClaim",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      claim.Name,
			Namespace: claim.Namespace,
		},
		Status: quotav1alpha1.ResourceClaimStatus{
			ObservedGeneration: claim.Generation,
			Conditions:         claim.Status.Conditions,
		},
	}

	// Apply the patch using Server Side Apply with our field manager
	fieldManagerName := "resource-claim-controller"
	if err := clusterClient.Status().Patch(ctx, patchClaim, client.Apply, client.FieldOwner(fieldManagerName), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to apply overall claim condition: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
// Watches ResourceClaims across all control planes (core and project control planes).
func (r *ResourceClaimController) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		For(&quotav1alpha1.ResourceClaim{},
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(true)).
		Named("resource-claim").
		WithOptions(mccontroller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		Complete(r)
}
