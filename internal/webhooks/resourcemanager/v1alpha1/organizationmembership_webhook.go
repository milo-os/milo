package v1alpha1

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

var organizationmembershiplog = logf.Log.WithName("organizationmembership-resource")

// +kubebuilder:webhook:path=/validate-resourcemanager-miloapis-com-v1alpha1-organizationmembership,mutating=false,failurePolicy=fail,sideEffects=None,groups=resourcemanager.miloapis.com,resources=organizationmemberships,verbs=create;update;delete,versions=v1alpha1,name=vorganizationmembership.datum.net,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// SetupOrganizationMembershipWebhooksWithManager sets up OrganizationMembership webhooks
func SetupOrganizationMembershipWebhooksWithManager(mgr ctrl.Manager, organizationOwnerRoleName string, organizationOwnerRoleNamespace string) error {
	organizationmembershiplog.Info("Setting up resourcemanager.miloapis.com organizationmembership webhooks")

	return ctrl.NewWebhookManagedBy(mgr, &resourcemanagerv1alpha1.OrganizationMembership{}).
		WithCustomValidator(&OrganizationMembershipValidator{
			client:             mgr.GetClient(),
			apiReader:          mgr.GetAPIReader(),
			ownerRoleName:      organizationOwnerRoleName,
			ownerRoleNamespace: organizationOwnerRoleNamespace,
		}).
		Complete()
}

// OrganizationMembershipValidator validates OrganizationMemberships
type OrganizationMembershipValidator struct {
	client             client.Client
	apiReader          client.Reader
	decoder            admission.Decoder
	ownerRoleName      string
	ownerRoleNamespace string
}

func (v *OrganizationMembershipValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	membership := obj.(*resourcemanagerv1alpha1.OrganizationMembership)
	organizationmembershiplog.Info("Validating OrganizationMembership create", "name", membership.Name, "namespace", membership.Namespace)

	// Validate roles if specified
	if len(membership.Spec.Roles) > 0 {
		if err := v.validateRoles(ctx, membership); err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func (v *OrganizationMembershipValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldMembership := oldObj.(*resourcemanagerv1alpha1.OrganizationMembership)
	newMembership := newObj.(*resourcemanagerv1alpha1.OrganizationMembership)
	organizationmembershiplog.Info("Validating OrganizationMembership update", "name", newMembership.Name, "namespace", newMembership.Namespace)

	// Validate roles if specified
	if len(newMembership.Spec.Roles) > 0 {
		if err := v.validateRoles(ctx, newMembership); err != nil {
			return nil, err
		}
	}

	if err := v.ensureOwnerRoleNotRemovedFromLastOwner(ctx, oldMembership, newMembership); err != nil {
		return nil, err
	}

	return nil, nil
}

func (v *OrganizationMembershipValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	membership := obj.(*resourcemanagerv1alpha1.OrganizationMembership)
	organizationmembershiplog.Info("Validating OrganizationMembership delete", "name", membership.Name, "namespace", membership.Namespace)

	if !v.isOwnerMembership(membership) {
		return nil, nil
	}

	// Allow deletion when the referenced user is itself being deleted — the
	// UserController is responsible for cleaning up orphaned memberships and
	// must not be blocked by the last-owner guard.
	if v.allowDeletionBecauseUserIsBeingDeleted(ctx, membership) {
		return nil, nil
	}

	if v.allowOwnerDeletionDuringTeardown(ctx, membership) {
		return nil, nil
	}

	var membershipList resourcemanagerv1alpha1.OrganizationMembershipList
	if err := v.client.List(ctx, &membershipList, client.InNamespace(membership.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list organization memberships: %w", err)
	}

	hasOtherOwner := false
	for i := range membershipList.Items {
		other := &membershipList.Items[i]
		if other.Name == membership.Name {
			continue
		}
		if other.Spec.OrganizationRef.Name != membership.Spec.OrganizationRef.Name {
			continue
		}
		if v.isOwnerMembership(other) {
			hasOtherOwner = true
			break
		}
	}

	if hasOtherOwner {
		return nil, nil
	}

	return nil, v.lastOwnerForbiddenError(membership, "delete")
}

// allowOwnerDeletionDuringTeardown returns true when the membership owner removal should be permitted because
// the surrounding namespace or organization is already in the process of being deleted.
func (v *OrganizationMembershipValidator) allowOwnerDeletionDuringTeardown(ctx context.Context, membership *resourcemanagerv1alpha1.OrganizationMembership) bool {
	if v.isNamespaceTerminating(ctx, membership.Namespace) {
		return true
	}

	orgName := membership.Spec.OrganizationRef.Name
	if orgName == "" {
		return false
	}

	var organization resourcemanagerv1alpha1.Organization
	if err := v.client.Get(ctx, client.ObjectKey{Name: orgName}, &organization); err != nil {
		if apierrors.IsNotFound(err) {
			return true
		}
		organizationmembershiplog.Error(err, "failed to fetch organization while validating delete",
			"organization", orgName)
		return false
	}

	return organization.DeletionTimestamp != nil
}

// allowDeletionBecauseUserIsBeingDeleted returns true when the membership should be
// allowed to be deleted because the referenced user has a non-zero DeletionTimestamp.
// The UserController adds a finalizer and drives membership cleanup; we must not
// block it with the last-owner guard.
//
// Uses the direct API reader (not the informer cache) to avoid stale reads that
// could incorrectly bypass the last-owner business invariant.
func (v *OrganizationMembershipValidator) allowDeletionBecauseUserIsBeingDeleted(ctx context.Context, membership *resourcemanagerv1alpha1.OrganizationMembership) bool {
	userName := membership.Spec.UserRef.Name
	if userName == "" {
		return false
	}

	var user iamv1alpha1.User
	if err := v.apiReader.Get(ctx, client.ObjectKey{Name: userName}, &user); err != nil {
		if apierrors.IsNotFound(err) {
			// User is already gone — allow the membership deletion.
			return true
		}
		organizationmembershiplog.Error(err, "failed to fetch user while validating delete",
			"user", userName)
		return false
	}

	return user.DeletionTimestamp != nil
}

// isNamespaceTerminating returns true if the namespace is terminating or already deleted.
func (v *OrganizationMembershipValidator) isNamespaceTerminating(ctx context.Context, namespace string) bool {
	if namespace == "" {
		return false
	}

	var ns corev1.Namespace
	if err := v.client.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err != nil {
		organizationmembershiplog.Error(err, "failed to fetch namespace while validating delete", "namespace", namespace)
		return false
	}

	return ns.DeletionTimestamp != nil
}

// validateRoles validates the role references in the membership
func (v *OrganizationMembershipValidator) validateRoles(ctx context.Context, membership *resourcemanagerv1alpha1.OrganizationMembership) error {
	// Check for duplicate roles
	if err := v.checkDuplicateRoles(membership); err != nil {
		return err
	}

	// Validate each role reference
	for _, roleRef := range membership.Spec.Roles {
		if err := v.validateRoleReference(ctx, membership, roleRef); err != nil {
			return err
		}
	}

	return nil
}

// isOwnerMembership returns true when the membership includes the configured owner role.
func (v *OrganizationMembershipValidator) isOwnerMembership(membership *resourcemanagerv1alpha1.OrganizationMembership) bool {
	if v.ownerRoleName == "" || v.ownerRoleNamespace == "" {
		return false
	}

	for _, roleRef := range membership.Spec.Roles {
		roleNamespace := roleRef.Namespace
		if roleNamespace == "" {
			roleNamespace = membership.Namespace
		}

		if roleRef.Name == v.ownerRoleName && roleNamespace == v.ownerRoleNamespace {
			return true
		}
	}

	return false
}

func (v *OrganizationMembershipValidator) ensureOwnerRoleNotRemovedFromLastOwner(ctx context.Context, oldMembership, newMembership *resourcemanagerv1alpha1.OrganizationMembership) error {
	var current resourcemanagerv1alpha1.OrganizationMembership
	if err := v.client.Get(ctx, client.ObjectKey{Namespace: oldMembership.Namespace, Name: oldMembership.Name}, &current); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get organization membership: %w", err)
	}

	if !v.isOwnerMembership(&current) || v.isOwnerMembership(newMembership) {
		return nil
	}

	if v.allowOwnerDeletionDuringTeardown(ctx, &current) {
		return nil
	}

	var membershipList resourcemanagerv1alpha1.OrganizationMembershipList
	if err := v.client.List(ctx, &membershipList, client.InNamespace(current.Namespace)); err != nil {
		return fmt.Errorf("failed to list organization memberships: %w", err)
	}

	hasOtherOwner := false
	for i := range membershipList.Items {
		other := &membershipList.Items[i]
		if other.Name == current.Name {
			continue
		}
		if other.Spec.OrganizationRef.Name != current.Spec.OrganizationRef.Name {
			continue
		}
		if v.isOwnerMembership(other) {
			hasOtherOwner = true
			break
		}
	}

	if hasOtherOwner {
		return nil
	}

	return v.lastOwnerForbiddenError(&current, "update")
}

func (v *OrganizationMembershipValidator) lastOwnerForbiddenError(membership *resourcemanagerv1alpha1.OrganizationMembership, action string) error {
	message := fmt.Sprintf(
		"organization '%s' must have at least one owner. Assign the owner role to another member before removing this membership, or delete the organization instead if you intend to remove all owners.",
		membership.Spec.OrganizationRef.Name,
	)

	return apierrors.NewForbidden(
		schema.GroupResource{Group: resourcemanagerv1alpha1.GroupVersion.Group, Resource: "organizationmemberships"},
		membership.Name,
		fmt.Errorf("cannot %s membership for user '%s': %s", action, membership.Spec.UserRef.Name, message),
	)
}

// checkDuplicateRoles ensures no duplicate roles are specified
func (v *OrganizationMembershipValidator) checkDuplicateRoles(membership *resourcemanagerv1alpha1.OrganizationMembership) error {
	seen := make(map[string]bool)

	for _, roleRef := range membership.Spec.Roles {
		// Create unique key for role
		roleNamespace := roleRef.Namespace
		if roleNamespace == "" {
			roleNamespace = membership.Namespace
		}
		roleKey := fmt.Sprintf("%s/%s", roleNamespace, roleRef.Name)

		if seen[roleKey] {
			return fmt.Errorf("duplicate role reference detected: %s in namespace %s", roleRef.Name, roleNamespace)
		}
		seen[roleKey] = true
	}

	return nil
}

// validateRoleReference validates a single role reference
func (v *OrganizationMembershipValidator) validateRoleReference(ctx context.Context, membership *resourcemanagerv1alpha1.OrganizationMembership, roleRef resourcemanagerv1alpha1.RoleReference) error {
	// Validate role name is not empty
	if roleRef.Name == "" {
		return fmt.Errorf("role name cannot be empty")
	}

	// Determine the namespace to check
	roleNamespace := roleRef.Namespace
	if roleNamespace == "" {
		roleNamespace = membership.Namespace
	}

	// Verify the role exists
	var role iamv1alpha1.Role
	roleKey := client.ObjectKey{
		Name:      roleRef.Name,
		Namespace: roleNamespace,
	}

	if err := v.client.Get(ctx, roleKey, &role); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return fmt.Errorf("role '%s' not found in namespace '%s'", roleRef.Name, roleNamespace)
		}
		return fmt.Errorf("failed to verify role '%s' in namespace '%s': %w", roleRef.Name, roleNamespace, err)
	}

	// Additional validation: ensure role is ready (if it has a status condition)
	// This is optional but helps catch issues early
	if len(role.Status.Conditions) > 0 {
		var readyCondition *metav1.Condition
		for i := range role.Status.Conditions {
			if role.Status.Conditions[i].Type == "Ready" {
				readyCondition = &role.Status.Conditions[i]
				break
			}
		}

		if readyCondition != nil && readyCondition.Status != metav1.ConditionTrue {
			organizationmembershiplog.Info("Warning: role is not ready",
				"role", roleRef.Name,
				"namespace", roleNamespace,
				"condition", readyCondition)
			// Note: We don't fail here, just log a warning
		}
	}

	return nil
}
