package resourcemanager

import (
	"context"
	"crypto/sha256"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

const (
	// OrganizationMembershipReady indicates that the organization membership status has been populated
	OrganizationMembershipReady = "Ready"
	// RolesApplied indicates that the roles have been applied to the membership
	RolesApplied = "RolesApplied"
)

const (
	// OrganizationMembershipReadyReason indicates that the organization membership is ready
	OrganizationMembershipReadyReason = "Ready"
	// OrganizationNotFoundReason indicates that the referenced organization was not found
	OrganizationNotFoundReason = "OrganizationNotFound"
	// UserNotFoundReason indicates that the referenced user was not found
	UserNotFoundReason = "UserNotFound"
	// ReconcileErrorReason indicates an error occurred during reconciliation
	ReconcileErrorReason = "ReconcileError"
	// AllRolesAppliedReason indicates all roles have been successfully applied
	AllRolesAppliedReason = "AllRolesApplied"
	// PartialRolesAppliedReason indicates some roles failed to apply
	PartialRolesAppliedReason = "PartialRolesApplied"
	// NoRolesSpecifiedReason indicates no roles were specified
	NoRolesSpecifiedReason = "NoRolesSpecified"
)

const (
	// Labels for PolicyBindings managed by this controller
	MembershipLabel = "resourcemanager.miloapis.com/membership"
	ManagedByLabel  = "resourcemanager.miloapis.com/managed-by"
	ManagedByValue  = "organization-membership-controller"
)

// OrganizationMembershipController reconciles an OrganizationMembership object
type OrganizationMembershipController struct {
	Client client.Client
	// Role that allows a user to delete their own OrganizationMembership
	SelfDeleteRoleName      string
	SelfDeleteRoleNamespace string
}

// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizationmemberships,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizationmemberships/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizations,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=roles,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=policybindings,verbs=get;list;watch;create;update;delete

func (r *OrganizationMembershipController) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	var organizationMembership resourcemanagerv1alpha.OrganizationMembership
	if err := r.Client.Get(ctx, req.NamespacedName, &organizationMembership); apierrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get organization membership: %w", err)
	}

	logger.Info("reconciling organization membership",
		"organization", organizationMembership.Spec.OrganizationRef.Name,
		"user", organizationMembership.Spec.UserRef.Name)

	// Get the current ready condition or create a new one
	readyCondition := apimeta.FindStatusCondition(organizationMembership.Status.Conditions, OrganizationMembershipReady)
	if readyCondition == nil {
		readyCondition = &metav1.Condition{
			Type:               OrganizationMembershipReady,
			Status:             metav1.ConditionFalse,
			Reason:             "Unknown",
			ObservedGeneration: organizationMembership.Generation,
		}
	} else {
		readyCondition = readyCondition.DeepCopy()
		readyCondition.ObservedGeneration = organizationMembership.Generation
	}

	// Fetch the referenced Organization
	var organization resourcemanagerv1alpha.Organization
	organizationKey := types.NamespacedName{
		Name: organizationMembership.Spec.OrganizationRef.Name,
	}

	if err := r.Client.Get(ctx, organizationKey, &organization); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("referenced organization not found", "organization", organizationMembership.Spec.OrganizationRef.Name)
			readyCondition.Status = metav1.ConditionFalse
			readyCondition.Reason = OrganizationNotFoundReason
			readyCondition.Message = fmt.Sprintf("Organization '%s' does not exist. Please ensure the organization name is correct and the organization has been created.", organizationMembership.Spec.OrganizationRef.Name)
		} else {
			logger.Error(err, "failed to get organization")
			readyCondition.Status = metav1.ConditionFalse
			readyCondition.Reason = ReconcileErrorReason
			readyCondition.Message = "Unable to retrieve organization information. Please try again later or contact support if the problem persists."
		}

		if apimeta.SetStatusCondition(&organizationMembership.Status.Conditions, *readyCondition) {
			if err := r.Client.Status().Update(ctx, &organizationMembership); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update organization membership status: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Fetch the referenced User
	var user iamv1alpha1.User
	userKey := types.NamespacedName{
		Name: organizationMembership.Spec.UserRef.Name,
	}

	if err := r.Client.Get(ctx, userKey, &user); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("referenced user not found", "user", organizationMembership.Spec.UserRef.Name)

			// Two-pass self-delete: if we already set UserNotFound on a previous
			// reconcile, delete the membership now so it does not linger after
			// the owning User is gone. The second reconcile is triggered by the
			// status condition update below, which re-enqueues the membership.
			existingCondition := apimeta.FindStatusCondition(organizationMembership.Status.Conditions, OrganizationMembershipReady)
			if existingCondition != nil && existingCondition.Reason == UserNotFoundReason {
				logger.Info("deleting OrganizationMembership because referenced user no longer exists",
					"membership", organizationMembership.Name,
					"user", organizationMembership.Spec.UserRef.Name)
				if err := r.Client.Delete(ctx, &organizationMembership); err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("failed to self-delete organization membership: %w", err)
				}
				return ctrl.Result{}, nil
			}

			readyCondition.Status = metav1.ConditionFalse
			readyCondition.Reason = UserNotFoundReason
			readyCondition.Message = fmt.Sprintf("User '%s' does not exist. Please ensure the user name is correct and the user account has been created.", organizationMembership.Spec.UserRef.Name)
		} else {
			logger.Error(err, "failed to get user")
			readyCondition.Status = metav1.ConditionFalse
			readyCondition.Reason = ReconcileErrorReason
			readyCondition.Message = "Unable to retrieve user information. Please try again later or contact the support team if the problem persists."
		}

		if apimeta.SetStatusCondition(&organizationMembership.Status.Conditions, *readyCondition) {
			if err := r.Client.Status().Update(ctx, &organizationMembership); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update organization membership status: %w", err)
			}
		}
		return ctrl.Result{}, nil
	} else {
		// Ensure the self-delete PolicyBinding exists for this membership/user
		if err := r.ensureSelfDeletePolicyBinding(ctx, &organizationMembership, &user); err != nil {
			logger.Error(err, "failed to ensure self-delete policy binding")
			return ctrl.Result{}, fmt.Errorf("failed to ensure self-delete policy binding: %w", err)
		}
	}

	// Update the status with information from the Organization and User
	originalStatus := organizationMembership.Status.DeepCopy()

	// Update observed generation
	organizationMembership.Status.ObservedGeneration = organizationMembership.Generation

	// Update organization status information
	organizationMembership.Status.Organization = resourcemanagerv1alpha.OrganizationMembershipOrganizationStatus{
		Type:        organization.Spec.Type,
		DisplayName: organization.Annotations["kubernetes.io/display-name"],
	}

	// Update user status information
	organizationMembership.Status.User = resourcemanagerv1alpha.OrganizationMembershipUserStatus{
		Email:      user.Spec.Email,
		GivenName:  user.Spec.GivenName,
		FamilyName: user.Spec.FamilyName,
		AvatarURL:  user.Status.AvatarURL,
	}

	// Set ready condition to true
	readyCondition.Status = metav1.ConditionTrue
	readyCondition.Reason = OrganizationMembershipReadyReason
	readyCondition.Message = "Organization membership status has been populated"

	apimeta.SetStatusCondition(&organizationMembership.Status.Conditions, *readyCondition)

	// Reconcile roles if any are specified
	if len(organizationMembership.Spec.Roles) > 0 {
		if err := r.reconcileRoles(ctx, &organizationMembership, &organization, &user); err != nil {
			logger.Error(err, "failed to reconcile roles")
			return ctrl.Result{}, fmt.Errorf("failed to reconcile roles: %w", err)
		}
	} else {
		// No roles specified, ensure RolesApplied condition reflects this
		rolesAppliedCondition := metav1.Condition{
			Type:               RolesApplied,
			Status:             metav1.ConditionTrue,
			Reason:             NoRolesSpecifiedReason,
			Message:            "No roles specified for this membership",
			ObservedGeneration: organizationMembership.Generation,
		}
		apimeta.SetStatusCondition(&organizationMembership.Status.Conditions, rolesAppliedCondition)
		organizationMembership.Status.AppliedRoles = []resourcemanagerv1alpha.AppliedRole{}
	}

	// Update the status only if something changed
	if !equality.Semantic.DeepEqual(originalStatus, organizationMembership.Status) {
		if err := r.Client.Status().Update(ctx, &organizationMembership); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update organization membership status: %w", err)
		}
		logger.Info("organization membership status updated")
	}

	return ctrl.Result{}, nil
}

// reconcileRoles manages PolicyBinding resources for the roles specified in the membership
func (r *OrganizationMembershipController) reconcileRoles(
	ctx context.Context,
	membership *resourcemanagerv1alpha.OrganizationMembership,
	organization *resourcemanagerv1alpha.Organization,
	user *iamv1alpha1.User,
) error {
	logger := log.FromContext(ctx)

	// Get existing PolicyBindings managed by this controller
	existingBindings, err := r.getManagedPolicyBindings(ctx, membership)
	if err != nil {
		return fmt.Errorf("failed to list existing policy bindings: %w", err)
	}

	// Build a map of desired roles
	desiredRoles := make(map[string]resourcemanagerv1alpha.RoleReference)
	for _, role := range membership.Spec.Roles {
		key := r.getRoleKey(role)
		desiredRoles[key] = role
	}

	// Build a map of existing bindings
	existingBindingMap := make(map[string]*iamv1alpha1.PolicyBinding)
	for i := range existingBindings.Items {
		binding := &existingBindings.Items[i]
		roleKey := r.getRoleKeyFromBinding(binding)
		existingBindingMap[roleKey] = binding
	}

	// Track applied roles for status
	appliedRoles := []resourcemanagerv1alpha.AppliedRole{}
	successCount := 0
	failureCount := 0
	pendingCount := 0

	// Process each desired role in the order specified in the spec
	for _, roleRef := range membership.Spec.Roles {
		roleKey := r.getRoleKey(roleRef)
		appliedRole := resourcemanagerv1alpha.AppliedRole{
			Name:      roleRef.Name,
			Namespace: roleRef.Namespace,
		}

		if existingBinding, exists := existingBindingMap[roleKey]; exists {
			// PolicyBinding exists, check if it's Ready
			isReady := r.isPolicyBindingReady(existingBinding)

			if isReady {
				appliedRole.Status = "Applied"
				appliedRole.PolicyBindingRef = &resourcemanagerv1alpha.PolicyBindingReference{
					Name:      existingBinding.Name,
					Namespace: existingBinding.Namespace,
				}
				if existingBinding.CreationTimestamp.Time.IsZero() {
					now := metav1.Now()
					appliedRole.AppliedAt = &now
				} else {
					appliedRole.AppliedAt = &existingBinding.CreationTimestamp
				}
				successCount++
			} else {
				appliedRole.Status = "Pending"
				appliedRole.Message = "Waiting for PolicyBinding to become Ready"
				appliedRole.PolicyBindingRef = &resourcemanagerv1alpha.PolicyBindingReference{
					Name:      existingBinding.Name,
					Namespace: existingBinding.Namespace,
				}
				pendingCount++
			}
			delete(existingBindingMap, roleKey) // Mark as processed
		} else {
			// Need to create PolicyBinding
			if err := r.createPolicyBinding(ctx, membership, organization, user, roleRef); err != nil {
				logger.Error(err, "failed to create policy binding", "role", roleRef.Name)
				appliedRole.Status = "Failed"
				appliedRole.Message = fmt.Sprintf("Failed to create PolicyBinding: %v", err)
				failureCount++
			} else {
				appliedRole.Status = "Pending"
				appliedRole.Message = "PolicyBinding created, waiting for Ready status"
				appliedRole.PolicyBindingRef = &resourcemanagerv1alpha.PolicyBindingReference{
					Name:      r.generatePolicyBindingName(membership, roleRef),
					Namespace: membership.Namespace,
				}
				pendingCount++
			}
		}

		appliedRoles = append(appliedRoles, appliedRole)
	}

	// Delete PolicyBindings that are no longer desired
	for _, binding := range existingBindingMap {
		logger.Info("deleting policy binding for removed role", "policyBinding", binding.Name)
		if err := r.Client.Delete(ctx, binding); err != nil && !apierrors.IsNotFound(err) {
			logger.Error(err, "failed to delete policy binding", "policyBinding", binding.Name)
			// Continue with other deletions
		}
	}

	// Update status with applied roles
	membership.Status.AppliedRoles = appliedRoles

	// Set RolesApplied condition
	rolesAppliedCondition := metav1.Condition{
		Type:               RolesApplied,
		ObservedGeneration: membership.Generation,
	}

	if failureCount == 0 && pendingCount == 0 {
		// All roles are ready
		rolesAppliedCondition.Status = metav1.ConditionTrue
		rolesAppliedCondition.Reason = AllRolesAppliedReason
		rolesAppliedCondition.Message = fmt.Sprintf("All %d role(s) successfully applied", successCount)
	} else if pendingCount > 0 {
		// Some roles are still pending
		rolesAppliedCondition.Status = metav1.ConditionFalse
		rolesAppliedCondition.Reason = PartialRolesAppliedReason
		rolesAppliedCondition.Message = fmt.Sprintf("%d of %d role(s) ready, %d pending", successCount, successCount+pendingCount+failureCount, pendingCount)
		// Requeue to check again when PolicyBindings become ready
		logger.Info("some roles pending, will requeue", "pending", pendingCount)
	} else {
		// Some roles failed
		rolesAppliedCondition.Status = metav1.ConditionFalse
		rolesAppliedCondition.Reason = PartialRolesAppliedReason
		rolesAppliedCondition.Message = fmt.Sprintf("%d of %d role(s) successfully applied", successCount, successCount+failureCount)
	}

	apimeta.SetStatusCondition(&membership.Status.Conditions, rolesAppliedCondition)

	return nil
}

// getManagedPolicyBindings retrieves PolicyBindings managed by this controller for the given membership
func (r *OrganizationMembershipController) getManagedPolicyBindings(
	ctx context.Context,
	membership *resourcemanagerv1alpha.OrganizationMembership,
) (*iamv1alpha1.PolicyBindingList, error) {
	var bindings iamv1alpha1.PolicyBindingList
	err := r.Client.List(ctx, &bindings,
		client.InNamespace(membership.Namespace),
		client.MatchingLabels{
			MembershipLabel: membership.Name,
			ManagedByLabel:  ManagedByValue,
		},
	)
	return &bindings, err
}

// createPolicyBinding creates a new PolicyBinding for a role assignment
func (r *OrganizationMembershipController) createPolicyBinding(
	ctx context.Context,
	membership *resourcemanagerv1alpha.OrganizationMembership,
	organization *resourcemanagerv1alpha.Organization,
	user *iamv1alpha1.User,
	roleRef resourcemanagerv1alpha.RoleReference,
) error {
	logger := log.FromContext(ctx)

	// Resolve role namespace
	roleNamespace := roleRef.Namespace
	if roleNamespace == "" {
		roleNamespace = membership.Namespace
	}

	// Verify the role exists
	var role iamv1alpha1.Role
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      roleRef.Name,
		Namespace: roleNamespace,
	}, &role); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("role %s not found in namespace %s", roleRef.Name, roleNamespace)
		}
		return fmt.Errorf("failed to get role: %w", err)
	}

	// Create PolicyBinding
	policyBinding := &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.generatePolicyBindingName(membership, roleRef),
			Namespace: membership.Namespace,
			Labels: map[string]string{
				MembershipLabel: membership.Name,
				ManagedByLabel:  ManagedByValue,
			},
		},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef: iamv1alpha1.RoleReference{
				Name:      roleRef.Name,
				Namespace: roleNamespace,
			},
			Subjects: []iamv1alpha1.Subject{
				{
					Kind: "User",
					Name: user.Name,
					UID:  string(user.UID),
				},
			},
			ResourceSelector: iamv1alpha1.ResourceSelector{
				ResourceRef: &iamv1alpha1.ResourceReference{
					APIGroup:  "resourcemanager.miloapis.com",
					Kind:      "Organization",
					Name:      organization.Name,
					UID:       string(organization.UID),
					Namespace: "",
				},
			},
		},
	}

	// Set owner reference
	if err := controllerutil.SetControllerReference(membership, policyBinding, r.Client.Scheme()); err != nil {
		return fmt.Errorf("failed to set controller reference: %w", err)
	}

	logger.Info("creating policy binding", "policyBinding", policyBinding.Name, "role", roleRef.Name)

	if err := r.Client.Create(ctx, policyBinding); err != nil {
		return fmt.Errorf("failed to create policy binding: %w", err)
	}

	return nil
}

// generatePolicyBindingName generates a deterministic name for a PolicyBinding
func (r *OrganizationMembershipController) generatePolicyBindingName(
	membership *resourcemanagerv1alpha.OrganizationMembership,
	roleRef resourcemanagerv1alpha.RoleReference,
) string {
	// Create a full hash for uniqueness
	roleKey := r.getRoleKey(roleRef)
	hash := sha256.Sum256([]byte(roleKey))
	hashStr := fmt.Sprintf("%x", hash)

	return fmt.Sprintf("%s-%s", membership.Name, hashStr)
}

// getRoleKey generates a unique key for a role reference
func (r *OrganizationMembershipController) getRoleKey(roleRef resourcemanagerv1alpha.RoleReference) string {
	if roleRef.Namespace != "" {
		return fmt.Sprintf("%s/%s", roleRef.Namespace, roleRef.Name)
	}
	return roleRef.Name
}

// getRoleKeyFromBinding extracts the role key from a PolicyBinding
func (r *OrganizationMembershipController) getRoleKeyFromBinding(binding *iamv1alpha1.PolicyBinding) string {
	if binding.Spec.RoleRef.Namespace != "" {
		return fmt.Sprintf("%s/%s", binding.Spec.RoleRef.Namespace, binding.Spec.RoleRef.Name)
	}
	return binding.Spec.RoleRef.Name
}

// isPolicyBindingReady checks if a PolicyBinding has a Ready condition with status True
func (r *OrganizationMembershipController) isPolicyBindingReady(binding *iamv1alpha1.PolicyBinding) bool {
	for _, condition := range binding.Status.Conditions {
		if condition.Type == "Ready" {
			return condition.Status == metav1.ConditionTrue
		}
	}
	return false
}

// ensureSelfDeletePolicyBinding makes sure a PolicyBinding exists that allows the referenced
// user to delete their own OrganizationMembership. It is idempotent.
func (r *OrganizationMembershipController) ensureSelfDeletePolicyBinding(
	ctx context.Context,
	membership *resourcemanagerv1alpha.OrganizationMembership,
	user *iamv1alpha1.User,
) error {
	logger := log.FromContext(ctx)

	bindingName := fmt.Sprintf("usermembership-self-delete-%s", user.Name)

	var existing iamv1alpha1.PolicyBinding
	if err := r.Client.Get(ctx, types.NamespacedName{Name: bindingName, Namespace: membership.Namespace}, &existing); err == nil {
		// Already exists – nothing to do.
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get existing self-delete PolicyBinding: %w", err)
	}

	pb := &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingName,
			Namespace: membership.Namespace,
		},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef: iamv1alpha1.RoleReference{
				Name:      r.SelfDeleteRoleName,
				Namespace: r.SelfDeleteRoleNamespace,
			},
			Subjects: []iamv1alpha1.Subject{
				{
					Kind: "User",
					Name: user.Name,
					UID:  string(user.UID),
				},
			},
			ResourceSelector: iamv1alpha1.ResourceSelector{
				ResourceRef: &iamv1alpha1.ResourceReference{
					APIGroup:  "resourcemanager.miloapis.com",
					Kind:      "OrganizationMembership",
					Name:      membership.Name,
					Namespace: membership.Namespace,
					UID:       string(membership.UID),
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(membership, pb, r.Client.Scheme()); err != nil {
		return fmt.Errorf("failed to set owner reference: %w", err)
	}

	logger.Info("creating self-delete PolicyBinding", "policyBinding", pb.Name)
	if err := r.Client.Create(ctx, pb); err != nil {
		return fmt.Errorf("failed to create self-delete PolicyBinding: %w", err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *OrganizationMembershipController) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &resourcemanagerv1alpha.OrganizationMembership{}, "spec.organizationRef.name", func(rawObj client.Object) []string {
		obj := rawObj.(*resourcemanagerv1alpha.OrganizationMembership)
		if obj.Spec.OrganizationRef.Name == "" {
			return nil
		}
		return []string{obj.Spec.OrganizationRef.Name}
	}); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &resourcemanagerv1alpha.OrganizationMembership{}, "spec.userRef.name", func(rawObj client.Object) []string {
		obj := rawObj.(*resourcemanagerv1alpha.OrganizationMembership)
		if obj.Spec.UserRef.Name == "" {
			return nil
		}
		return []string{obj.Spec.UserRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&resourcemanagerv1alpha.OrganizationMembership{}).
		Watches(&resourcemanagerv1alpha.Organization{},
			handler.EnqueueRequestsFromMapFunc(r.findOrganizationMembershipsForOrganization)).
		Watches(&iamv1alpha1.User{},
			handler.EnqueueRequestsFromMapFunc(r.findOrganizationMembershipsForUser)).
		Owns(&iamv1alpha1.PolicyBinding{}).
		Named("organization-membership").
		Complete(r)
}

// findOrganizationMembershipsForOrganization finds all OrganizationMembership resources that reference a given Organization
func (r *OrganizationMembershipController) findOrganizationMembershipsForOrganization(ctx context.Context, obj client.Object) []reconcile.Request {
	organization := obj.(*resourcemanagerv1alpha.Organization)

	log.FromContext(ctx).Info("finding organization memberships for organization", "organization", organization.Name)

	var organizationMemberships resourcemanagerv1alpha.OrganizationMembershipList
	if err := r.Client.List(ctx, &organizationMemberships, client.MatchingFields{
		"spec.organizationRef.name": organization.Name,
	}); err != nil {
		log.FromContext(ctx).Error(err, "failed to list organization memberships for organization", "organization", organization.Name)
		return nil
	}

	var requests []reconcile.Request
	for _, membership := range organizationMemberships.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      membership.Name,
				Namespace: membership.Namespace,
			},
		})
	}

	return requests
}

// findOrganizationMembershipsForUser finds all OrganizationMembership resources that reference a given User
func (r *OrganizationMembershipController) findOrganizationMembershipsForUser(ctx context.Context, obj client.Object) []reconcile.Request {
	user := obj.(*iamv1alpha1.User)

	log.FromContext(ctx).Info("finding organization memberships for user", "user", user.Name)

	var organizationMemberships resourcemanagerv1alpha.OrganizationMembershipList
	if err := r.Client.List(ctx, &organizationMemberships, client.MatchingFields{
		"spec.userRef.name": user.Name,
	}); err != nil {
		log.FromContext(ctx).Error(err, "failed to list organization memberships for user", "user", user.Name)
		return nil
	}

	var requests []reconcile.Request
	for _, membership := range organizationMemberships.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      membership.Name,
				Namespace: membership.Namespace,
			},
		})
	}

	return requests
}
