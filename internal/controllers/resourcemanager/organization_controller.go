package resourcemanager

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
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

// OrganizationNamespaceFinalizer blocks Organization deletion until its
// namespace has been confirmed to carry an owner reference back to it (or
// the namespace doesn't exist, so there's nothing to protect). It's added
// synchronously at admission time (OrganizationMutator.Default), not by this
// controller's reconcile: without it present from the object's very first
// persisted version, an Organization deleted before the controller's first
// reconcile has run would have nothing to block deletion, and its namespace
// would be removed with no owner reference ever having been set, orphaning
// it and everything inside it. Reconcile still adds it defensively if
// missing, as a backstop for Organizations that predate this finalizer.
const OrganizationNamespaceFinalizer = "resourcemanager.miloapis.com/organization-namespace"

// OrganizationController reconciles an Organization object
type OrganizationController struct {
	Client client.Client

	// OwnerRoleName is the role granted to the organization creator.
	OwnerRoleName string

	// OwnerRoleNamespace is the namespace containing OwnerRoleName.
	OwnerRoleNamespace string
}

// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizations/finalizers,verbs=update
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizationmemberships,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=authorization.k8s.io,resources=subjectaccessreviews,verbs=create

func (r *OrganizationController) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	var organization resourcemanagerv1alpha.Organization
	if err := r.Client.Get(ctx, req.NamespacedName, &organization); apierrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get organization: %w", err)
	}

	namespaceName := resourcemanagerv1alpha.OrganizationNamespace(organization.Name)

	if !organization.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&organization, OrganizationNamespaceFinalizer) {
			return ctrl.Result{}, nil
		}

		if err := r.ensureNamespaceOwnerReference(ctx, &organization, namespaceName, logger); err != nil {
			return ctrl.Result{}, err
		}

		before := organization.DeepCopy()
		controllerutil.RemoveFinalizer(&organization, OrganizationNamespaceFinalizer)
		if err := r.Client.Patch(ctx, &organization, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove organization namespace finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	if utilfeature.DefaultFeatureGate.Enabled(features.UnifiedOrganizations) {
		if err := r.reconcileOrganizationOwnerBootstrap(ctx, &organization); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling organization owner bootstrap: %w", err)
		}
	}

	logger.Info("reconciling organization")
	defer logger.Info("reconcile complete")

	if err := r.ensureNamespaceOwnerReference(ctx, &organization, namespaceName, logger); err != nil {
		return ctrl.Result{}, err
	}

	if !controllerutil.ContainsFinalizer(&organization, OrganizationNamespaceFinalizer) {
		before := organization.DeepCopy()
		controllerutil.AddFinalizer(&organization, OrganizationNamespaceFinalizer)
		if err := r.Client.Patch(ctx, &organization, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add organization namespace finalizer: %w", err)
		}
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

// ensureNamespaceOwnerReference sets organization as the controller owner
// reference of its namespace if the namespace exists and doesn't already
// have one. It is a no-op if the namespace doesn't exist yet.
func (r *OrganizationController) ensureNamespaceOwnerReference(
	ctx context.Context,
	organization *resourcemanagerv1alpha.Organization,
	namespaceName string,
	logger logr.Logger,
) error {
	var namespace corev1.Namespace
	if err := r.Client.Get(ctx, types.NamespacedName{Name: namespaceName}, &namespace); apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get organization namespace: %w", err)
	}

	hasOwnerRef, err := controllerutil.HasOwnerReference(namespace.OwnerReferences, organization, r.Client.Scheme())
	if err != nil {
		return fmt.Errorf("failed to check if organization is owner reference: %w", err)
	}
	if hasOwnerRef {
		return nil
	}

	logger.Info("adding organization as owner reference to namespace", "namespace", namespaceName)
	if err := controllerutil.SetControllerReference(organization, &namespace, r.Client.Scheme()); err != nil {
		return fmt.Errorf("failed to set controller reference: %w", err)
	}
	if err := r.Client.Update(ctx, &namespace); err != nil {
		return fmt.Errorf("failed to update namespace owner references: %w", err)
	}
	return nil
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
