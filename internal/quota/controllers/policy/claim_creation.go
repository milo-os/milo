// Package policy implements controllers for quota policy management.
// It contains controllers for ClaimCreationPolicy and GrantCreationPolicy resources
// that validate policy configurations and manage grant creation workflows.
package policy

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.miloapis.com/milo/pkg/quota/validation"
	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// ClaimCreationPolicyReconciler reconciles a ClaimCreationPolicy object.
// Its sole responsibility is to validate the policy and set the Ready status condition.
// The PolicyEngine (used only by the admission plugin) watches for policies with Ready=True.
type ClaimCreationPolicyReconciler struct {
	Scheme  *runtime.Scheme
	Manager mcmanager.Manager
	// PolicyValidator validates ClaimCreationPolicy resources.
	PolicyValidator *validation.ClaimCreationPolicyValidator
}

// +kubebuilder:rbac:groups=quota.miloapis.com,resources=claimcreationpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=claimcreationpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourceregistrations,verbs=get;list;watch

// Reconcile reconciles a ClaimCreationPolicy object by validating it and setting its Ready status.
// The controller's sole responsibility is validation - the PolicyEngine (in the admission plugin)
// watches for policies with Ready=True status condition.
// This controller only runs in the core control plane where policies are defined.
func (r *ClaimCreationPolicyReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
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

	var policy quotav1alpha1.ClaimCreationPolicy
	logger.V(1).Info("Reconciling ClaimCreationPolicy", "name", req.Name)

	if err := clusterClient.Get(ctx, req.NamespacedName, &policy); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("ClaimCreationPolicy was deleted", "name", req.Name)
			// Policy was deleted - nothing to do (PolicyEngine will handle via watch)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ClaimCreationPolicy: %w", err)
	}

	// Store original status to detect changes
	originalStatus := policy.Status.DeepCopy()

	validationErrs := r.PolicyValidator.Validate(ctx, &policy, validation.ControllerValidationOptions())

	// Update policy status based on validation results
	r.updatePolicyStatus(&policy, validationErrs)

	// Always track the latest generation so the diff captures generation-only changes
	policy.Status.ObservedGeneration = policy.Generation

	// Only write to the API if something actually changed
	if !equality.Semantic.DeepEqual(&policy.Status, originalStatus) {
		if err := clusterClient.Status().Update(ctx, &policy); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update ClaimCreationPolicy status: %w", err)
		}
		logger.V(1).Info("Updated ClaimCreationPolicy status",
			"policy", policy.Name,
			"ready", apimeta.IsStatusConditionTrue(policy.Status.Conditions, quotav1alpha1.ClaimCreationPolicyReady))
	}

	return ctrl.Result{}, nil
}

// updatePolicyStatus updates the policy status conditions based on validation results.
func (r *ClaimCreationPolicyReconciler) updatePolicyStatus(policy *quotav1alpha1.ClaimCreationPolicy, validationErrs field.ErrorList) {
	if len(validationErrs) > 0 {
		// Validation failed - format errors with field paths
		apimeta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:    quotav1alpha1.ClaimCreationPolicyReady,
			Status:  metav1.ConditionFalse,
			Reason:  quotav1alpha1.ClaimCreationPolicyValidationFailedReason,
			Message: validationErrs.ToAggregate().Error(),
		})
		return
	}

	if policy.Spec.Disabled != nil && *policy.Spec.Disabled {
		// Policy is disabled
		apimeta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:    quotav1alpha1.ClaimCreationPolicyReady,
			Status:  metav1.ConditionFalse,
			Reason:  quotav1alpha1.ClaimCreationPolicyDisabledReason,
			Message: "Policy is disabled",
		})
		return
	}

	// Validation passed and policy is enabled
	apimeta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:    quotav1alpha1.ClaimCreationPolicyReady,
		Status:  metav1.ConditionTrue,
		Reason:  quotav1alpha1.ClaimCreationPolicyReadyReason,
		Message: "Policy is ready and active",
	})
}

// enqueueAffectedPolicies finds all ClaimCreationPolicies that reference a ResourceRegistration
// and enqueues them for reconciliation when the registration changes.
func (r *ClaimCreationPolicyReconciler) enqueueAffectedPolicies(ctx context.Context, obj client.Object) []mcreconcile.Request {
	registration, ok := obj.(*quotav1alpha1.ResourceRegistration)
	if !ok {
		return nil
	}

	clusterName, _ := mccontext.ClusterFrom(ctx)

	cluster, err := r.Manager.GetCluster(ctx, clusterName)
	if err != nil {
		klog.V(1).ErrorS(err, "failed to get cluster client when enqueuing affected policies", "clusterName", clusterName)
		return nil
	}
	clusterClient := cluster.GetClient()

	// List all ClaimCreationPolicies
	var policyList quotav1alpha1.ClaimCreationPolicyList
	if err := clusterClient.List(ctx, &policyList); err != nil {
		klog.V(1).ErrorS(err, "failed to list claim creation policies when enqueuing affected policies")
		return nil
	}

	var requests []mcreconcile.Request
	// Find policies that reference this resource type
	for _, policy := range policyList.Items {
		for _, request := range policy.Spec.Target.ResourceClaimTemplate.Spec.Requests {
			if request.ResourceType == registration.Spec.ResourceType {
				// This policy references the changed ResourceRegistration - trigger reconciliation
				requests = append(requests, mcreconcile.Request{
					Request: ctrl.Request{
						NamespacedName: client.ObjectKeyFromObject(&policy),
					},
				})
				break // Only need to enqueue each policy once
			}
		}
	}

	return requests
}

// SetupWithManager sets up the controller with the Manager.
// ClaimCreationPolicies are centralized resources that only exist in the local cluster (Milo API server).
func (r *ClaimCreationPolicyReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		For(&quotav1alpha1.ClaimCreationPolicy{},
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		// Watch ResourceRegistrations to revalidate policies when registrations change
		Watches(
			&quotav1alpha1.ResourceRegistration{},
			mchandler.TypedEnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					return r.enqueueAffectedPolicies(ctx, obj)
				},
			),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false),
		).
		Named("claim-creation-policy").
		Complete(r)
}
