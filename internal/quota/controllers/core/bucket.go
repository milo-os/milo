// Package core implements the core quota controllers that manage AllowanceBuckets,
// ResourceClaims, ResourceGrants, and ResourceRegistrations.
//
// The AllowanceBucketController owns the lifecycle of AllowanceBuckets.
// Centralizing bucket writes in a single controller avoids write races between
// controllers and keeps reconciliation predictable. This controller
// materializes aggregates for efficient quota evaluation by:
// - Computing Limit from Active ResourceGrants (scoped to the same owner)
// - Computing Allocated from Granted ResourceClaims (same owner + shape)
// Buckets are created on demand when claims reference them to minimize churn
// while still handling eventual consistency between controllers.
package core

import (
	"context"
	"crypto/sha256"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mccontroller "sigs.k8s.io/multicluster-runtime/pkg/controller"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

const (
	// resourceClaimConsumerRefIndex is the field index name for ResourceClaim.Spec.ConsumerRef
	resourceClaimConsumerRefIndex = "spec.consumerRef"
)

// AllowanceBucketController reconciles AllowanceBucket objects and maintains
// aggregated quota (Limit/Allocated/Available). It is the single writer for
// bucket objects; other controllers are read-only.
type AllowanceBucketController struct {
	Scheme                  *runtime.Scheme
	Manager                 mcmanager.Manager
	MaxConcurrentReconciles int
}

// +kubebuilder:rbac:groups=quota.miloapis.com,resources=allowancebuckets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=allowancebuckets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourcegrants,verbs=get;list;watch
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourceclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourceclaims/status,verbs=get;update;patch

// Reconcile maintains AllowanceBucket limits and usage aggregates by watching
// ResourceGrants and ResourceClaims across all control planes.
func (r *AllowanceBucketController) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if req.ClusterName != "" {
		logger = logger.WithValues("cluster", req.ClusterName)
		ctx = log.IntoContext(ctx, logger)
	}

	// Multicluster support enables quota enforcement across project control planes
	cluster, err := r.Manager.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.ClusterName, err)
	}
	clusterClient := cluster.GetClient()

	// Get the AllowanceBucket
	var bucket quotav1alpha1.AllowanceBucket
	if err := clusterClient.Get(ctx, req.NamespacedName, &bucket); err != nil {
		if apierrors.IsNotFound(err) {
			// Single-writer pattern: create bucket on first claim reference
			if err := r.ensureBucketFromClaims(ctx, clusterClient, req.NamespacedName); err != nil {
				return ctrl.Result{}, err
			}
			// Bucket creation triggers automatic requeue via watch event
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, fmt.Errorf("failed to get AllowanceBucket: %w", err)
		}
	}

	originalStatus := bucket.Status.DeepCopy()

	bucket.Status.ObservedGeneration = bucket.Generation

	if err := r.updateLimitsFromGrants(ctx, clusterClient, &bucket); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update limits from grants: %w", err)
	}

	if err := r.updateUsageFromClaims(ctx, clusterClient, &bucket); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update usage from claims: %w", err)
	}

	// processPendingClaims performs intermediate status updates for atomic quota reservation.
	if err := r.processPendingClaims(ctx, clusterClient, &bucket); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed processing pending grants: %w", err)
	}

	bucket.Status.Available = max(0, bucket.Status.Limit-bucket.Status.Allocated)

	return r.updateStatusIfChanged(ctx, clusterClient, &bucket, originalStatus)
}

// updateLimitsFromGrants calculates total quota limits from active ResourceGrants.
// Searches cluster-wide because buckets are centralized but grants may be distributed.
func (r *AllowanceBucketController) updateLimitsFromGrants(ctx context.Context, clusterClient client.Client, bucket *quotav1alpha1.AllowanceBucket) error {

	// Centralized buckets require scanning all namespaces for grants
	var grants quotav1alpha1.ResourceGrantList
	if err := clusterClient.List(ctx, &grants); err != nil {
		return fmt.Errorf("failed to list ResourceGrants: %w", err)
	}

	var totalLimit int64
	var contributingGrants []quotav1alpha1.ContributingGrantRef

	for _, grant := range grants.Items {
		// Only consider active grants
		if !r.isResourceGrantActive(&grant) {
			continue
		}

		// Prevent cross-owner mixing by requiring an exact owner match
		if grant.Spec.ConsumerRef.Kind != bucket.Spec.ConsumerRef.Kind ||
			grant.Spec.ConsumerRef.Name != bucket.Spec.ConsumerRef.Name {
			continue
		}

		// Check if this grant applies to this bucket
		for _, allowance := range grant.Spec.Allowances {
			if allowance.ResourceType != bucket.Spec.ResourceType {
				continue
			}

			// Check each bucket in the allowance
			for _, allowanceBucket := range allowance.Buckets {
				totalLimit += allowanceBucket.Amount
				contributingGrants = append(contributingGrants, quotav1alpha1.ContributingGrantRef{
					Name:                   grant.Name,
					LastObservedGeneration: grant.Generation,
					Amount:                 allowanceBucket.Amount,
				})
			}
		}
	}

	bucket.Status.Limit = totalLimit
	bucket.Status.GrantCount = int32(len(contributingGrants))
	bucket.Status.ContributingGrantRefs = contributingGrants

	return nil
}

// updateUsageFromClaims calculates the total allocated usage from ResourceClaims
// based on individual request allocations that have been granted.
func (r *AllowanceBucketController) updateUsageFromClaims(ctx context.Context, clusterClient client.Client, bucket *quotav1alpha1.AllowanceBucket) error {
	// Find all ResourceClaims cluster-wide that reference this bucket's consumer
	var claims quotav1alpha1.ResourceClaimList
	if err := clusterClient.List(ctx, &claims,
		client.MatchingFields{resourceClaimConsumerRefIndex: consumerRefKey(bucket.Spec.ConsumerRef)},
	); err != nil {
		return fmt.Errorf("failed to list ResourceClaims: %w", err)
	}

	var totalAllocated int64
	var claimCount int32

	for _, claim := range claims.Items {
		// Track whether this claim has any granted allocations for this bucket
		// (consumer ref already filtered by field selector)
		hasGrantedAllocation := false

		// Check allocations for granted requests that match this bucket
		for _, allocation := range claim.Status.Allocations {
			if allocation.Status != quotav1alpha1.ResourceClaimAllocationStatusGranted {
				continue
			}

			// Check if this allocation matches the bucket
			if allocation.ResourceType != bucket.Spec.ResourceType {
				continue
			}

			// Find the corresponding request from the spec to check dimensions
			var matchingRequest *quotav1alpha1.ResourceRequest
			for _, req := range claim.Spec.Requests {
				if req.ResourceType == allocation.ResourceType {
					matchingRequest = &req
					break
				}
			}
			if matchingRequest == nil {
				continue
			}

			// Use the allocated amount from the allocation status
			totalAllocated += allocation.AllocatedAmount
			hasGrantedAllocation = true
		}

		// Increment claim count once per claim if it has any granted allocations for this bucket
		if hasGrantedAllocation {
			claimCount++
		}
	}

	bucket.Status.Allocated = totalAllocated
	bucket.Status.ClaimCount = claimCount

	return nil
}

// ensureBucketFromClaims creates the bucket spec from a referencing claim if found.
// It returns true if a bucket was created, false if no referencing claim was found.
func (r *AllowanceBucketController) ensureBucketFromClaims(ctx context.Context, clusterClient client.Client, bucketKey types.NamespacedName) error {
	var claims quotav1alpha1.ResourceClaimList
	if err := clusterClient.List(ctx, &claims); err != nil {
		return fmt.Errorf("failed to list ResourceClaims: %w", err)
	}
	for _, claim := range claims.Items {
		for _, request := range claim.Spec.Requests {
			name := generateAllowanceBucketName(request.ResourceType, claim.Spec.ConsumerRef)
			if name == bucketKey.Name {
				// create bucket
				bucket := &quotav1alpha1.AllowanceBucket{
					ObjectMeta: metav1.ObjectMeta{
						Name:      bucketKey.Name,
						Namespace: bucketKey.Namespace,
						Labels: map[string]string{
							"quota.miloapis.com/consumer-kind": claim.Spec.ConsumerRef.Kind,
							"quota.miloapis.com/consumer-name": claim.Spec.ConsumerRef.Name,
						},
					},
					Spec: quotav1alpha1.AllowanceBucketSpec{
						ConsumerRef:  claim.Spec.ConsumerRef,
						ResourceType: request.ResourceType,
					},
				}
				if err := clusterClient.Create(ctx, bucket); err != nil && !apierrors.IsAlreadyExists(err) {
					return fmt.Errorf("failed to create AllowanceBucket %s: %w", bucketKey.Name, err)
				}
				return nil
			}
		}
	}
	return nil
}

// isResourceGrantActive checks if a ResourceGrant has an Active condition with status True.
func (r *AllowanceBucketController) isResourceGrantActive(grant *quotav1alpha1.ResourceGrant) bool {
	return apimeta.IsStatusConditionTrue(grant.Status.Conditions, quotav1alpha1.ResourceGrantActive)
}

// processPendingClaims attempts to grant pending requests that reference this bucket.
// For each eligible claim, it evaluates individual requests that match this bucket,
// reserves capacity, then marks specific request allocations as Granted/Denied.
func (r *AllowanceBucketController) processPendingClaims(ctx context.Context, clusterClient client.Client, bucket *quotav1alpha1.AllowanceBucket) error {
	logger := log.FromContext(ctx)
	var claims quotav1alpha1.ResourceClaimList
	if err := clusterClient.List(ctx, &claims,
		client.MatchingFields{resourceClaimConsumerRefIndex: consumerRefKey(bucket.Spec.ConsumerRef)},
	); err != nil {
		return fmt.Errorf("failed to list ResourceClaims: %w", err)
	}

	// Current state for available calculation during this reconcile loop
	limit := bucket.Status.Limit
	allocated := bucket.Status.Allocated
	fieldManagerName := fmt.Sprintf("allowance-bucket-%s", bucket.Name)

	for _, claim := range claims.Items {
		// Process each request that matches this bucket
		for _, request := range claim.Spec.Requests {
			// Skip if request doesn't match this bucket
			if request.ResourceType != bucket.Spec.ResourceType {
				continue
			}

			// Check if this request is already processed by looking at allocations
			if r.isResourceClaimAllocationProcessed(&claim, request.ResourceType) {
				logger.V(2).Info("Request allocation already processed, skipping",
					"claimName", claim.Name,
					"resourceType", request.ResourceType)
				continue
			}

			// Check availability using current local view
			if limit-allocated < request.Amount {
				logger.Info("Insufficient quota available for request",
					"claimName", claim.Name,
					"resourceType", request.ResourceType,
					"requestAmount", request.Amount,
					"available", limit-allocated)

				// Mark this specific request as denied
				if err := r.updateResourceClaimAllocation(ctx, clusterClient, &claim, request.ResourceType, quotav1alpha1.ResourceClaimAllocationStatusDenied,
					quotav1alpha1.ResourceClaimDeniedReason,
					fmt.Sprintf("Resource quota exceeded: requested %d, available %d", request.Amount, limit-allocated),
					0, "", fieldManagerName); err != nil {
					logger.Error(err, "failed to update request allocation for denial",
						"claimName", claim.Name, "resourceType", request.ResourceType)
				}
				continue
			}

			// Reserve capacity and keep status fields self-consistent for validation
			bucket.Status.Allocated = allocated + request.Amount
			// Recompute Available with clamp to satisfy CRD validation
			bucket.Status.Available = max(0, bucket.Status.Limit-bucket.Status.Allocated)
			bucket.Status.ObservedGeneration = bucket.Generation

			if err := clusterClient.Status().Update(ctx, bucket); err != nil {
				return fmt.Errorf("failed to update bucket during reservation: %w", err)
			}

			// Reservation successful; update local allocated for subsequent requests
			allocated = bucket.Status.Allocated

			// Mark this specific request as granted
			if err := r.updateResourceClaimAllocation(ctx, clusterClient, &claim, request.ResourceType, quotav1alpha1.ResourceClaimAllocationStatusGranted,
				quotav1alpha1.ResourceClaimGrantedReason,
				"Capacity reserved",
				request.Amount, bucket.Name, fieldManagerName); err != nil {
				logger.Error(err, "failed to update request allocation after reservation",
					"claimName", claim.Name, "resourceType", request.ResourceType)
				// Don't revert the bucket allocation - the capacity has been reserved
				return fmt.Errorf("failed to update request allocation: %w", err)
			}

		}
	}
	return nil
}

// isResourceClaimAllocationProcessed checks if a specific request allocation has already been processed.
func (r *AllowanceBucketController) isResourceClaimAllocationProcessed(claim *quotav1alpha1.ResourceClaim, resourceType string) bool {
	for _, allocation := range claim.Status.Allocations {
		if allocation.ResourceType == resourceType &&
			(allocation.Status == quotav1alpha1.ResourceClaimAllocationStatusGranted || allocation.Status == quotav1alpha1.ResourceClaimAllocationStatusDenied) {
			return true
		}
	}
	return false
}

// updateResourceClaimAllocation updates or creates a request allocation status using Server Side Apply.
func (r *AllowanceBucketController) updateResourceClaimAllocation(ctx context.Context, clusterClient client.Client, claim *quotav1alpha1.ResourceClaim,
	resourceType string, status, reason, message string, allocatedAmount int64, bucketName, fieldManagerName string) error {

	allocation := quotav1alpha1.ResourceClaimAllocationStatus{
		ResourceType:       resourceType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		AllocatedAmount:    allocatedAmount,
		LastTransitionTime: metav1.Now(),
	}

	// Set the allocating bucket reference only when status is Granted
	if status == quotav1alpha1.ResourceClaimAllocationStatusGranted {
		allocation.AllocatingBucket = bucketName
	}

	// Create a minimal claim object for Server Side Apply
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
			Allocations: []quotav1alpha1.ResourceClaimAllocationStatus{allocation},
		},
	}

	// Apply the patch using Server Side Apply with our field manager
	// The allocations list is a map-list keyed by resourceType, so SSA will merge entries correctly
	if err := clusterClient.Status().Patch(ctx, patchClaim, client.Apply, client.FieldOwner(fieldManagerName)); err != nil {
		return fmt.Errorf("failed to apply request allocation status: %w", err)
	}

	return nil
}

// updateStatusIfChanged updates the bucket status if it changed.
// Skips the update if the status is semantically identical to prevent unnecessary
// API server writes and audit log entries. Updates the LastReconciliation timestamp
// only when status changes.
func (r *AllowanceBucketController) updateStatusIfChanged(ctx context.Context, clusterClient client.Client, bucket *quotav1alpha1.AllowanceBucket, originalStatus *quotav1alpha1.AllowanceBucketStatus) (ctrl.Result, error) {
	if equality.Semantic.DeepEqual(&bucket.Status, originalStatus) {
		return ctrl.Result{}, nil
	}

	bucket.Status.LastReconciliation = ptr.To(metav1.Now())

	if err := clusterClient.Status().Update(ctx, bucket); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update AllowanceBucket status: %w", err)
	}

	return ctrl.Result{}, nil
}

// generateAllowanceBucketName creates a deterministic name for an AllowanceBucket.
// Buckets are global per consumer and resource type, not per claim namespace.
func generateAllowanceBucketName(resourceType string, ownerRef quotav1alpha1.ConsumerRef) string {
	input := fmt.Sprintf("%s%s%s", resourceType, ownerRef.Kind, ownerRef.Name)
	return fmt.Sprintf("bucket-%x", sha256.Sum256([]byte(input)))
}

// getBucketNamespace determines the namespace where an AllowanceBucket should be created
// based on the consumer type:
// - Organization consumers → organization-{name} namespace
// - Project consumers → milo-system namespace (centralized quota tracking)
// - Other consumers → milo-system namespace (default)
func getBucketNamespace(consumerRef quotav1alpha1.ConsumerRef) string {
	if consumerRef.Kind == "Organization" {
		return fmt.Sprintf("organization-%s", consumerRef.Name)
	}
	return "milo-system"
}

// consumerRefKey generates a consistent field index key for a ConsumerRef.
// This key is used to efficiently query ResourceClaims by their consumer reference.
func consumerRefKey(ref quotav1alpha1.ConsumerRef) string {
	return fmt.Sprintf("%s/%s/%s/%s", ref.APIGroup, ref.Kind, ref.Namespace, ref.Name)
}

// SetupWithManager sets up the controller with the Manager.
// This controller watches AllowanceBuckets, ResourceGrants, and ResourceClaims across all control planes.
func (r *AllowanceBucketController) SetupWithManager(mgr mcmanager.Manager) error {
	indexFunc := func(obj client.Object) []string {
		claim := obj.(*quotav1alpha1.ResourceClaim)
		return []string{consumerRefKey(claim.Spec.ConsumerRef)}
	}

	// Register index on both multicluster manager and local manager to support queries across all clusters
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&quotav1alpha1.ResourceClaim{},
		resourceClaimConsumerRefIndex,
		indexFunc,
	); err != nil {
		return fmt.Errorf("failed to set up field index for ResourceClaim.Spec.ConsumerRef on provider clusters: %w", err)
	}

	if err := mgr.GetLocalManager().GetFieldIndexer().IndexField(
		context.Background(),
		&quotav1alpha1.ResourceClaim{},
		resourceClaimConsumerRefIndex,
		indexFunc,
	); err != nil {
		return fmt.Errorf("failed to set up field index for ResourceClaim.Spec.ConsumerRef on local cluster: %w", err)
	}

	return mcbuilder.ControllerManagedBy(mgr).
		For(&quotav1alpha1.AllowanceBucket{},
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(true)).
		// Watch ResourceGrants that affect bucket limits
		Watches(
			&quotav1alpha1.ResourceGrant{},
			mchandler.TypedEnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					return r.enqueueAffectedBuckets(ctx, obj)
				},
			),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(true),
			mcbuilder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					// React on new grants so newly active grants can be picked up
					return true
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					// Trigger on spec (generation) changes or active status flips
					oldGrant := e.ObjectOld.(*quotav1alpha1.ResourceGrant)
					newGrant := e.ObjectNew.(*quotav1alpha1.ResourceGrant)
					if oldGrant.Generation != newGrant.Generation {
						return true
					}
					oldActive := apimeta.IsStatusConditionTrue(oldGrant.Status.Conditions, quotav1alpha1.ResourceGrantActive)
					newActive := apimeta.IsStatusConditionTrue(newGrant.Status.Conditions, quotav1alpha1.ResourceGrantActive)
					return oldActive != newActive
				},
			}),
		).
		// Watch ResourceClaims that affect bucket usage
		Watches(
			&quotav1alpha1.ResourceClaim{},
			mchandler.TypedEnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					return r.enqueueAffectedBuckets(ctx, obj)
				},
			),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(true),
			mcbuilder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					// React immediately on new claims to create buckets on demand
					return true
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					// Trigger on spec changes or any Granted condition status/reason change
					oldClaim := e.ObjectOld.(*quotav1alpha1.ResourceClaim)
					newClaim := e.ObjectNew.(*quotav1alpha1.ResourceClaim)
					if oldClaim.Generation != newClaim.Generation {
						return true
					}
					oldCond := apimeta.FindStatusCondition(oldClaim.Status.Conditions, quotav1alpha1.ResourceClaimGranted)
					newCond := apimeta.FindStatusCondition(newClaim.Status.Conditions, quotav1alpha1.ResourceClaimGranted)
					if (oldCond == nil) != (newCond == nil) {
						return true
					}
					if oldCond != nil && newCond != nil {
						return oldCond.Status != newCond.Status || oldCond.Reason != newCond.Reason
					}
					return false
				},
			}),
		).
		Named("allowance-bucket").
		WithOptions(mccontroller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		Complete(r)
}

// enqueueAffectedBuckets determines which AllowanceBuckets need to be reconciled
// when ResourceGrants or ResourceClaims change. Buckets are centralized in milo-system namespace.
func (r *AllowanceBucketController) enqueueAffectedBuckets(ctx context.Context, obj client.Object) []mcreconcile.Request {
	var requests []mcreconcile.Request

	clusterName, _ := mccontext.ClusterFrom(ctx)

	switch o := obj.(type) {
	case *quotav1alpha1.ResourceGrant:
		// For each allowance in the grant, enqueue the corresponding bucket
		// Bucket namespace is determined by consumer type (Organization namespace or milo-system)
		for _, allowance := range o.Spec.Allowances {
			bucketName := generateAllowanceBucketName(allowance.ResourceType, o.Spec.ConsumerRef)
			bucketNamespace := getBucketNamespace(o.Spec.ConsumerRef)
			requests = append(requests, mcreconcile.Request{
				ClusterName: clusterName,
				Request: ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      bucketName,
						Namespace: bucketNamespace,
					},
				},
			})
		}

	case *quotav1alpha1.ResourceClaim:
		// For each request in the claim, enqueue the corresponding bucket
		// Bucket namespace is determined by consumer type (Organization namespace or milo-system)
		for _, request := range o.Spec.Requests {
			bucketName := generateAllowanceBucketName(request.ResourceType, o.Spec.ConsumerRef)
			bucketNamespace := getBucketNamespace(o.Spec.ConsumerRef)
			requests = append(requests, mcreconcile.Request{
				ClusterName: clusterName,
				Request: ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      bucketName,
						Namespace: bucketNamespace,
					},
				},
			})
		}
	}

	return requests
}
