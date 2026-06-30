package resourcemanager

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

// OrganizationController reconciles an Organization object
type OrganizationController struct {
	Client client.Client
}

// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizations,verbs=get;list;watch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=authorization.k8s.io,resources=subjectaccessreviews,verbs=create

func (r *OrganizationController) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	var organization resourcemanagerv1alpha.Organization
	if err := r.Client.Get(ctx, req.NamespacedName, &organization); apierrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get organization: %w", err)
	}

	if !organization.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	logger.Info("reconciling organization")
	defer logger.Info("reconcile complete")

	namespaceName := resourcemanagerv1alpha.OrganizationNamespace(organization.Name)
	var namespace corev1.Namespace
	if err := r.Client.Get(ctx, types.NamespacedName{Name: namespaceName}, &namespace); err == nil {
		hasOwnerRef, ownerErr := controllerutil.HasOwnerReference(namespace.OwnerReferences, &organization, r.Client.Scheme())
		if ownerErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to check if organization is owner reference: %w", ownerErr)
		} else if !hasOwnerRef {
			logger.Info("adding organization as owner reference to namespace", "namespace", namespaceName)
			if err := controllerutil.SetControllerReference(&organization, &namespace, r.Client.Scheme()); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set controller reference: %w", err)
			}
			if err := r.Client.Update(ctx, &namespace); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update namespace owner references: %w", err)
			}
		}
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get organization namespace: %w", err)
	}

	if utilfeature.DefaultFeatureGate.Enabled(features.UnifiedOrganizations) {
		statusChanged, err := reconcileOrganizationOnboarding(ctx, r.Client, &organization)
		if err != nil {
			return ctrl.Result{}, err
		}
		if statusChanged {
			if err := r.Client.Status().Update(ctx, &organization); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update organization onboarding status: %w", err)
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *OrganizationController) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()

	controllerBuilder := ctrl.NewControllerManagedBy(mgr).
		For(&resourcemanagerv1alpha.Organization{})

	if utilfeature.DefaultFeatureGate.Enabled(features.UnifiedOrganizations) && billingAccountsSupported(mgr.GetRESTMapper()) {
		controllerBuilder = controllerBuilder.Watches(
			&billingv1alpha1.BillingAccount{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				keys := mapBillingAccountToOrganization(ctx, obj)
				requests := make([]reconcile.Request, 0, len(keys))
				for _, key := range keys {
					requests = append(requests, reconcile.Request{NamespacedName: key})
				}
				return requests
			}),
			builder.WithPredicates(),
		)
	}

	return controllerBuilder.
		Named("organization").
		Complete(r)
}

func billingAccountsSupported(mapper meta.RESTMapper) bool {
	_, err := mapper.RESTMapping(
		schema.GroupKind{Group: "billing.miloapis.com", Kind: "BillingAccount"},
		"v1alpha1",
	)
	return err == nil
}
