package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"go.miloapis.com/milo/pkg/quota"
)

// DefaultStalePendingClaimAge is the age after which a Pending auto-created
// ResourceClaim is considered abandoned and eligible for deletion. It is set
// well above the admission plugin's watch timeout (30s by default) so that
// normal in-flight admission requests are never disturbed, while any claim
// that outlives a failed admission path is reliably cleaned up.
const DefaultStalePendingClaimAge = quota.DefaultStalePendingClaimAge

// DeniedAutoClaimCleanupController automatically deletes ResourceClaims that
// were created by the admission plugin and are no longer useful:
//
//   - Denied claims (Granted=False, reason=QuotaExceeded) - kept for
//     DeniedClaimRetention after the denial, then removed. A request still
//     waiting on the claim reads its denial if its watch answers 410 Gone;
//     a retry of the same resource replaces the kept claim itself.
//   - Stale pending claims (no final Granted condition, older than
//     StalePendingClaimAge) - removed so leftover claims from admission
//     timeouts cannot permanently block retries via AlreadyExists.
//
// Manually created claims are never touched; the controller keys off the
// auto-created label and annotation set by the admission plugin.
type DeniedAutoClaimCleanupController struct {
	Scheme  *runtime.Scheme
	Manager mcmanager.Manager

	// StalePendingClaimAge is the minimum age before a Pending claim is
	// considered abandoned. Zero means use DefaultStalePendingClaimAge.
	StalePendingClaimAge time.Duration

	// DeniedClaimRetention is how long a denied claim is kept after its
	// denial. Zero means use quota.DefaultDeniedClaimRetention.
	DeniedClaimRetention time.Duration

	logger logr.Logger
	now    func() time.Time
}

// NewDeniedAutoClaimCleanupController creates a new DeniedAutoClaimCleanupController.
func NewDeniedAutoClaimCleanupController(
	scheme *runtime.Scheme,
	manager mcmanager.Manager,
) *DeniedAutoClaimCleanupController {
	return &DeniedAutoClaimCleanupController{
		Scheme:  scheme,
		Manager: manager,
		logger:  ctrl.Log.WithName("denied-autoclaim-cleanup"),
		now:     time.Now,
	}
}

// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourceclaims,verbs=get;list;watch;delete

// Reconcile processes ResourceClaims and deletes those that are:
//  1. Auto-created by the admission plugin AND
//  2. Either denied (Granted=False, reason=QuotaExceeded) or pending for
//     longer than the configured stale-pending age.
//
// This controller runs across all control planes to clean up these claims
// wherever they exist. Stale pending claims are requeued just past their
// deadline so they get deleted even without a follow-up event.
func (r *DeniedAutoClaimCleanupController) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("claim", req.Name, "namespace", req.Namespace)
	if req.ClusterName != "" {
		logger = logger.WithValues("cluster", req.ClusterName)
		ctx = log.IntoContext(ctx, logger)
	}

	cluster, err := r.Manager.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.ClusterName, err)
	}
	clusterClient := cluster.GetClient()

	var claim quotav1alpha1.ResourceClaim
	if err := clusterClient.Get(ctx, req.NamespacedName, &claim); err != nil {
		// Claim was deleted or doesn't exist - nothing to do
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(2).Info("Processing ResourceClaim for cleanup evaluation")

	// Filter 1: Only process auto-created claims
	if !r.isAutoCreatedClaim(&claim) {
		logger.V(3).Info("Skipping manually created claim")
		return ctrl.Result{}, nil
	}

	// Denied claims are kept for the retention window, then removed.
	if r.isClaimDenied(&claim) {
		if left := r.deniedRetentionLeft(&claim); left > 0 {
			return ctrl.Result{RequeueAfter: left}, nil
		}
		logger.Info("Deleting denied auto-created ResourceClaim",
			"policy", claim.Labels["quota.miloapis.com/policy"],
			"resourceName", claim.Annotations["quota.miloapis.com/resource-name"],
			"denialReason", r.getClaimDenialReason(&claim))
		return ctrl.Result{}, deleteJudgedClaim(ctx, clusterClient, &claim, "denied")
	}

	// Stale pending claims (no final condition, older than the threshold) are
	// removed so they do not permanently block admission retries for the same
	// deterministic name.
	if r.isClaimPending(&claim) {
		threshold := r.stalePendingAge()
		age := r.now().Sub(claim.CreationTimestamp.Time)
		if age < threshold {
			// Requeue just past the threshold to guarantee eventual cleanup
			// even if no further events fire for this claim.
			return ctrl.Result{RequeueAfter: (threshold - age) + time.Second}, nil
		}

		logger.Info("Deleting stale pending auto-created ResourceClaim",
			"policy", claim.Labels["quota.miloapis.com/policy"],
			"resourceName", claim.Annotations["quota.miloapis.com/resource-name"],
			"age", age)

		return ctrl.Result{}, deleteJudgedClaim(ctx, clusterClient, &claim, "stale pending")
	}

	logger.V(3).Info("Skipping granted claim")
	return ctrl.Result{}, nil
}

// deleteJudgedClaim deletes the claim this reconcile judged, and only that
// claim: identity and version preconditions. A retry may have replaced it
// under the same name since the cache was read, and the replacement is a
// fresh claim of a waiting request; it gets its own reconcile. A claim that
// is gone or replaced is not an error.
func deleteJudgedClaim(ctx context.Context, c client.Client, claim *quotav1alpha1.ResourceClaim, kind string) error {
	logger := log.FromContext(ctx)
	err := c.Delete(ctx, claim, client.Preconditions{UID: &claim.UID, ResourceVersion: &claim.ResourceVersion})
	switch {
	case apierrors.IsNotFound(err) || apierrors.IsConflict(err):
		logger.V(1).Info("Auto-created ResourceClaim already gone or replaced", "kind", kind)
		return nil
	case err != nil:
		logger.Error(err, "Failed to delete auto-created ResourceClaim", "kind", kind)
		return fmt.Errorf("failed to delete %s auto-created ResourceClaim: %w", kind, err)
	}
	logger.V(1).Info("Deleted auto-created ResourceClaim", "kind", kind)
	return nil
}

// deniedRetentionLeft returns how much longer a denied claim is kept: the
// retention window from the denial's lastTransitionTime (the claim's creation
// when the condition carries none), less the time since.
func (r *DeniedAutoClaimCleanupController) deniedRetentionLeft(claim *quotav1alpha1.ResourceClaim) time.Duration {
	retention := r.DeniedClaimRetention
	if retention <= 0 {
		retention = quota.DefaultDeniedClaimRetention
	}
	deniedAt := claim.CreationTimestamp.Time
	if cond := apimeta.FindStatusCondition(claim.Status.Conditions, quotav1alpha1.ResourceClaimGranted); cond != nil && !cond.LastTransitionTime.IsZero() {
		deniedAt = cond.LastTransitionTime.Time
	}
	return retention - r.now().Sub(deniedAt)
}

// stalePendingAge returns the configured stale-pending threshold, falling
// back to the default when unset.
func (r *DeniedAutoClaimCleanupController) stalePendingAge() time.Duration {
	if r.StalePendingClaimAge > 0 {
		return r.StalePendingClaimAge
	}
	return DefaultStalePendingClaimAge
}

// isAutoCreatedClaim checks if a ResourceClaim was automatically created by the admission plugin.
// Returns true only if both the label and annotation markers are present.
func (r *DeniedAutoClaimCleanupController) isAutoCreatedClaim(claim *quotav1alpha1.ResourceClaim) bool {
	r.logger.V(3).Info("Checking auto-created markers",
		"claim", claim.Name,
		"autoCreatedLabel", claim.Labels["quota.miloapis.com/auto-created"] == "true",
		"createdByPlugin", claim.Annotations["quota.miloapis.com/created-by"] == "claim-creation-plugin")
	return isAutoCreatedClaim(claim)
}

// isClaimDenied checks if a ResourceClaim has been denied due to quota exceeded.
func (r *DeniedAutoClaimCleanupController) isClaimDenied(claim *quotav1alpha1.ResourceClaim) bool {
	cond := apimeta.FindStatusCondition(claim.Status.Conditions, quotav1alpha1.ResourceClaimGranted)
	return cond != nil && cond.Status == metav1.ConditionFalse && cond.Reason == quotav1alpha1.ResourceClaimDeniedReason
}

// isClaimPending returns true when the claim has no Granted condition, or the
// Granted condition is False with a non-denied reason (e.g. PendingEvaluation).
// Granted=True claims return false.
func (r *DeniedAutoClaimCleanupController) isClaimPending(claim *quotav1alpha1.ResourceClaim) bool {
	cond := apimeta.FindStatusCondition(claim.Status.Conditions, quotav1alpha1.ResourceClaimGranted)
	if cond == nil {
		return true
	}
	if cond.Status == metav1.ConditionTrue {
		return false
	}
	// Granted=False but not Denied → still pending/in-progress from the
	// caller's perspective (e.g. PendingEvaluation, ValidationFailed).
	return cond.Reason != quotav1alpha1.ResourceClaimDeniedReason
}

// getClaimDenialReason returns the reason why a ResourceClaim was denied.
func (r *DeniedAutoClaimCleanupController) getClaimDenialReason(claim *quotav1alpha1.ResourceClaim) string {
	cond := apimeta.FindStatusCondition(claim.Status.Conditions, quotav1alpha1.ResourceClaimGranted)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		return "unknown"
	}
	if cond.Message != "" {
		return cond.Message
	}
	return cond.Reason
}

// SetupWithManager sets up the controller with the Manager and configures efficient filtering.
func (r *DeniedAutoClaimCleanupController) SetupWithManager(mgr mcmanager.Manager) error {
	if r.now == nil {
		r.now = time.Now
	}
	return mcbuilder.ControllerManagedBy(mgr).
		For(&quotav1alpha1.ResourceClaim{},
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(true)).
		// Use predicate to filter at the watch level for efficiency
		WithEventFilter(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			claim, ok := obj.(*quotav1alpha1.ResourceClaim)
			if !ok {
				return false
			}
			return isAutoCreatedClaim(claim)
		})).
		Named("denied-auto-claim-cleanup").
		Complete(r)
}
