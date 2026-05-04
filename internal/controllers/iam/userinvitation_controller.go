package iam

import (
	"context"
	"fmt"
	"strings"
	"time"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/finalizer"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	userInvitationFinalizerKey   = "iam.miloapis.com/userinvitation"
	uiPlatformAccessApprovalKey  = "iam.miloapis.com/ui-platform-access-approval"
	uiPlatformAccessRejectionKey = "iam.miloapis.com/ui-platform-access-rejection"
)

const (
	inviteeUserStatusUpdateConditionType = "InviteeUserStatusUpdate"
)

type UserInvitationController struct {
	Client                          client.Client
	finalizer                       finalizer.Finalizers
	SystemNamespace                 string
	GetInvitationRoleName           string
	AcceptInvitationRoleName        string
	UserInvitationEmailTemplateName string
	uiRelatedRoles                  []iamv1alpha1.RoleReference
}

type userInvitationFinalizer struct {
	client         client.Client
	uiRelatedRoles []iamv1alpha1.RoleReference
}

func (f *userInvitationFinalizer) Finalize(ctx context.Context, obj client.Object) (finalizer.Result, error) {
	log := logf.FromContext(ctx).WithName("userinvitation-finalizer")
	log.Info("Finalizing UserInvitation", "name", obj.GetName())

	ui, ok := obj.(*iamv1alpha1.UserInvitation)
	if !ok {
		return finalizer.Result{}, fmt.Errorf("unexpected object type %T, expected UserInvitation", obj)
	}

	// Delete the PolicyBindings invitation-related roles
	for _, roleRe := range f.uiRelatedRoles {
		if err := deletePolicyBinding(ctx, f.client, &iamv1alpha1.RoleReference{
			Name:      roleRe.Name,
			Namespace: roleRe.Namespace,
		}, *ui); err != nil {
			log.Error(err, "Failed to delete PolicyBinding for invitation-related role on UserInvitation finalization", "role", roleRe)
			return finalizer.Result{}, fmt.Errorf("failed to delete PolicyBinding for invitation-related role on UserInvitation finalization: %w", err)
		}
	}

	log.Info("Successfully finalized UserInvitation (cleaned up ui PolicyBindings)")

	return finalizer.Result{}, nil
}

func (r *UserInvitationController) SetupController(mgr ctrl.Manager, systemNamespace, getInvitationRoleName, acceptInvitationRoleName string) error {
	r.Client = mgr.GetClient()
	r.SystemNamespace = systemNamespace
	r.GetInvitationRoleName = getInvitationRoleName
	r.AcceptInvitationRoleName = acceptInvitationRoleName
	return nil
}

const (
	userEmailIndexKey = "spec.email"
)

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=userinvitations,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=userinvitations/status,verbs=update
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=userinvitations/finalizers,verbs=update
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=policybindings,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizationmemberships,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizations,verbs=get;list;watch
// +kubebuilder:rbac:groups=notification.miloapis.com,resources=emails,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=notification.miloapis.com,resources=emailtemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=platformaccessrejections,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=platformaccessapprovals,verbs=get;list;watch

func (r *UserInvitationController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("userinvitation-reconciler")
	log.Info("Starting reconciliation", "name", req.Name)

	// Get the UserInvitation
	ui := &iamv1alpha1.UserInvitation{}
	if err := r.Client.Get(ctx, req.NamespacedName, ui); err != nil {
		if errors.IsNotFound(err) {
			log.Info("UserInvitation not found, probably deleted. Skipping reconciliation")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get UserInvitation")
		return ctrl.Result{}, fmt.Errorf("failed to get UserInvitation: %w", err)
	}

	// Run finalizers. This adds the finalizer string on first reconcile and
	// executes userInvitationFinalizer.Finalize on deletion to clean up
	// invitation-related PolicyBindings before the object is removed.
	finalizeResult, err := r.finalizer.Finalize(ctx, ui)
	if err != nil {
		log.Error(err, "Failed to run finalizers for UserInvitation")
		return ctrl.Result{}, fmt.Errorf("failed to run finalizers for UserInvitation: %w", err)
	}

	if finalizeResult.Updated {
		log.Info("Finalizer updated UserInvitation, persisting to API server")
		if updateErr := r.Client.Update(ctx, ui); updateErr != nil {
			log.Error(updateErr, "Failed to update UserInvitation after finalizer update")
			return ctrl.Result{}, fmt.Errorf("failed to update UserInvitation after finalizer update: %w", updateErr)
		}
		return ctrl.Result{}, nil
	}

	if ui.GetDeletionTimestamp() != nil {
		log.Info("UserInvitation is marked for deletion, stopping reconciliation")
		return ctrl.Result{}, nil
	}

	log.Info("reconciling UserInvitation", "name", ui.Name, "email", ui.Spec.Email)

	// Update the UserInvitation status with the invitee user information
	// Done here for migration purposes, to ensure that the UserInvitation status is updated with the invitee user information.
	if err := r.updateUserInvitationInviteeUserStatus(ctx, ui); err != nil {
		log.Error(err, "Failed to update UserInvitation status with invitee user information")
		return ctrl.Result{}, fmt.Errorf("failed to update UserInvitation status with invitee user information: %w", err)
	}

	// Check if the UserInvitation is ready
	if meta.IsStatusConditionTrue(ui.Status.Conditions, string(iamv1alpha1.UserInvitationReadyCondition)) {
		log.Info("UserInvitation is ready, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Check if the UserInvitation is not expired
	if meta.IsStatusConditionTrue(ui.Status.Conditions, string(iamv1alpha1.UserInvitationExpiredCondition)) {
		log.Info("UserInvitation is expired, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Get the display name of the Organization referenced by the UserInvitation
	organizationDisplayName, err := r.getReferencedOrganizationDisplayName(ctx, ui.Spec.OrganizationRef)
	if err != nil {
		log.Error(err, "Failed to get Organization Display Name")
		return ctrl.Result{}, fmt.Errorf("failed to get Organization Display Name: %w", err)
	}
	// Get the display name and email address of the User who invited the user in the invitation
	inviterDisplayName, inviterEmailAddress, err := r.getReferencedInviterUserInfo(ctx, ui.Spec.InvitedBy)
	if err != nil {
		log.Error(err, "Failed to get Inviter Display Name")
		return ctrl.Result{}, fmt.Errorf("failed to get Inviter Display Name: %w", err)
	}
	// Update the UserInvitation status with the organization and inviter user information
	ui.Status.Organization = iamv1alpha1.UserInvitationOrganizationStatus{
		DisplayName: organizationDisplayName,
	}
	ui.Status.InviterUser = iamv1alpha1.UserInvitationUserStatus{
		DisplayName:  inviterDisplayName,
		EmailAddress: inviterEmailAddress,
	}

	// Check that the UserInvitation is not expired
	// Expiration is checked in the validationwebhook, but we check here in case some UserInvitation got
	// stuck in the controller loop for a long time, and we want to prevent giving roles to a user that is no longer valid.
	if isUserInvitationExpired(ui) {
		if err := r.updateUserInvitationStatus(ctx, ui.DeepCopy(), metav1.Condition{
			Type:    string(iamv1alpha1.UserInvitationExpiredCondition),
			Status:  metav1.ConditionTrue,
			Reason:  string(iamv1alpha1.UserInvitationStateExpiredReason),
			Message: "User Invitation is expired",
		}); err != nil {
			log.Error(err, "Failed to update expired UserInvitation status")
			return ctrl.Result{}, fmt.Errorf("failed to update expired UserInvitation status: %w", err)
		}
		log.Info("ExpiredUserInvitation status updated", "name", ui.Name)
		return ctrl.Result{}, nil
	}

	// Get the User that was invited by the UserInvitation
	user, err := r.getInviteeUser(ctx, ui.Spec.Email)
	if err != nil {
		log.Error(err, "Failed to get Invitee User")
		return ctrl.Result{}, fmt.Errorf("failed to get Invitee User: %w", err)
	}

	// Grant the access approval to the invitee user
	if err := r.grantAccessApproval(ctx, user, ui); err != nil {
		log.Error(err, "Failed to grant access approval to invitee user")
		return ctrl.Result{}, fmt.Errorf("failed to grant access approval to invitee user: %w", err)
	}

	// Send an email to the invitee user to accept the invitation
	// It is possible that the invitee User is not created yet, so we send the email anyway.
	if err := r.createInvitationEmail(ctx, ui.DeepCopy()); err != nil {
		log.Error(err, "Failed to send invitation email to user", "userInvitation", ui.GetName())
		return ctrl.Result{}, fmt.Errorf("failed to send invitation email to user: %w", err)
	}

	if user == nil {
		log.Info("Invitee User not found, skipping reconciliation. Reconciliation will be triggered again when the User is created.")
		return ctrl.Result{}, nil
	}

	// Grant roles to the invitee user for the organization if the invitation is accepted
	if isUserInvitationAccepted(ui) {
		log.Info("Creating OrganizationMembership with roles for the invitee user, as the invitation is accepted", "user", user.Name, "roles", ui.Spec.Roles)

		// Create the OrganizationMembership with roles
		if err := r.createOrganizationMembership(ctx, user, ui); err != nil {
			log.Error(err, "Failed to create OrganizationMembership for userInvitation")
			return ctrl.Result{}, fmt.Errorf("failed to create OrganizationMembership for userInvitation: %w", err)
		}

		// Delete the UserInvitation now that it has been fully processed and accepted.
		if err := r.Client.Delete(ctx, ui); err != nil && !errors.IsNotFound(err) {
			log.Error(err, "Failed to delete UserInvitation after acceptance")
			return ctrl.Result{}, fmt.Errorf("failed to delete UserInvitation after acceptance: %w", err)
		}

		log.Info("UserInvitation accepted and deleted", "userInvitation", ui.GetName())
		return ctrl.Result{}, nil
	}

	if isUserInvitationDeclined(ui) {
		// Delete the PolicyBindings for the invitation-related roles
		log.Info("Deleting PolicyBindings for accepting the invitation, as the invitation is declined", "userInvitation", ui.GetName())
		if err := deletePolicyBinding(ctx, r.Client, &iamv1alpha1.RoleReference{
			Name:      r.AcceptInvitationRoleName,
			Namespace: r.SystemNamespace,
		}, *ui); err != nil {
			log.Error(err, "Failed to delete PolicyBinding for accepting the invitation")
			return ctrl.Result{}, fmt.Errorf("failed to delete PolicyBinding for accepting the invitation: %w", err)
		}

		// Update the UserInvitation status
		if err := r.updateUserInvitationStatus(ctx, ui.DeepCopy(), metav1.Condition{
			Type:    string(iamv1alpha1.UserInvitationReadyCondition),
			Status:  metav1.ConditionTrue,
			Reason:  string(iamv1alpha1.UserInvitationStateDeclinedReason),
			Message: "User declined the invitation",
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update UserInvitation status: %w", err)
		}

		log.Info("UserInvitation reconciled. User declined the invitation", "userInvitation", ui.GetName())
		return ctrl.Result{}, nil
	}

	// Check if the UserInvitation is pending
	if meta.IsStatusConditionTrue(ui.Status.Conditions, string(iamv1alpha1.UserInvitationPendingCondition)) {
		log.Info("UserInvitation is pending, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Grant permissions to the invitee user so they can accept the invitation
	for _, role := range r.uiRelatedRoles {
		err := r.createPolicyBinding(ctx, user, ui, &iamv1alpha1.RoleReference{
			Name:      role.Name,
			Namespace: role.Namespace,
		})
		if err != nil {
			log.Error(err, "Failed to create policy binding with %s role", role, "userInvitation", ui.GetName())
			return ctrl.Result{}, fmt.Errorf("failed to create policy binding with %s role: %w", role, err)
		}
	}

	// Update the UserInvitation status
	if err := r.updateUserInvitationStatus(ctx, ui.DeepCopy(), metav1.Condition{
		Type:    string(iamv1alpha1.UserInvitationPendingCondition),
		Status:  metav1.ConditionTrue,
		Reason:  string(iamv1alpha1.UserInvitationStatePendingReason),
		Message: "Waiting for user to accept the invitation",
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update UserInvitation status: %w", err)
	}

	log.Info("UserInvitation reconciled", "userInvitation", ui.GetName())

	return ctrl.Result{}, nil
}

func (r *UserInvitationController) SetupWithManager(mgr ctrl.Manager) error {
	log := logf.FromContext(context.Background()).WithName("userinvitation-setup-with-manager")
	log.Info("Setting up UserInvitationController with Manager")

	r.uiRelatedRoles = append(r.uiRelatedRoles, iamv1alpha1.RoleReference{
		Name:      r.GetInvitationRoleName,
		Namespace: r.SystemNamespace,
	}, iamv1alpha1.RoleReference{
		Name:      r.AcceptInvitationRoleName,
		Namespace: r.SystemNamespace,
	})

	r.finalizer = finalizer.NewFinalizers()
	if err := r.finalizer.Register(userInvitationFinalizerKey, &userInvitationFinalizer{
		client:         r.Client,
		uiRelatedRoles: r.uiRelatedRoles,
	}); err != nil {
		log.Error(err, "Failed to register user invitation finalizer")
		return fmt.Errorf("failed to register user invitation finalizer: %w", err)
	}

	// Register field indexer for User email for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(context.Background(),
		&iamv1alpha1.User{}, userEmailIndexKey,
		func(obj client.Object) []string {
			user := obj.(*iamv1alpha1.User)
			return []string{strings.ToLower(user.Spec.Email)}
		}); err != nil {
		log.Error(err, "Failed to set field index on User by .spec.email")
		return fmt.Errorf("failed to set field index on User by .spec.email: %w", err)
	}

	// Register field indexer for UserInvitation email for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(context.Background(),
		&iamv1alpha1.UserInvitation{}, userEmailIndexKey,
		func(obj client.Object) []string {
			ui := obj.(*iamv1alpha1.UserInvitation)
			return []string{strings.ToLower(ui.Spec.Email)}
		}); err != nil {
		return fmt.Errorf("failed to set field index on UserInvitation by .spec.email: %w", err)
	}

	// Register field indexer for PlatformAccessApproval for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(context.Background(),
		&iamv1alpha1.PlatformAccessApproval{}, uiPlatformAccessApprovalKey,
		func(obj client.Object) []string {
			paa := obj.(*iamv1alpha1.PlatformAccessApproval)
			return []string{buildPlatformAccessApprovalIndexKey(&paa.Spec.SubjectRef)}
		}); err != nil {
		return fmt.Errorf("failed to set field index on PlatformAccessApproval by .spec.subjectRef.userRef.name: %w", err)
	}

	// Register field indexer for PlatformAccessRejection for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(context.Background(),
		&iamv1alpha1.PlatformAccessRejection{}, uiPlatformAccessRejectionKey,
		func(obj client.Object) []string {
			par := obj.(*iamv1alpha1.PlatformAccessRejection)
			return []string{par.Spec.UserRef.Name}
		}); err != nil {
		return fmt.Errorf("failed to set field index on PlatformAccessRejection by .spec.userRef.name: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&iamv1alpha1.UserInvitation{}).
		Watches(
			&iamv1alpha1.User{},
			handler.EnqueueRequestsFromMapFunc(r.findUserInvitationsForUser),
			builder.WithPredicates(userCreateOnlyPredicate),
		).
		Named("userinvitation").
		Complete(r)
}

// updateUserInvitationStatus updates the status of the UserInvitation
func (r *UserInvitationController) updateUserInvitationStatus(ctx context.Context, ui *iamv1alpha1.UserInvitation, condition metav1.Condition) error {
	log := logf.FromContext(ctx).WithName("userinvitation-update-status")

	originalStatus := ui.Status.DeepCopy()
	meta.SetStatusCondition(&ui.Status.Conditions, condition)

	// Only update if status actually changed
	if equality.Semantic.DeepEqual(&ui.Status, originalStatus) {
		log.V(1).Info("UserInvitation status unchanged, skipping update")
		return nil
	}

	log.Info("Updating UserInvitation status", "status", ui.Status)
	if err := r.Client.Status().Update(ctx, ui); err != nil {
		log.Error(err, "failed to update UserInvitation status", "userInvitation", ui.Name)
		return fmt.Errorf("failed to update UserInvitation status: %w", err)
	}
	log.Info("UserInvitation status updated")

	return nil
}

// createPolicyBinding creates a PolicyBinding for the invitee user to the organization.
// This is an idempotent operation.
func (r *UserInvitationController) createPolicyBinding(
	ctx context.Context,
	user *iamv1alpha1.User,
	invitation *iamv1alpha1.UserInvitation,
	roleRef *iamv1alpha1.RoleReference) error {

	log := logf.FromContext(ctx).WithName("userinvitation-create-invitee-policy-binding")
	log.Info("Attempting to create PolicyBinding for invitee user", "user", user.Name)

	// Check if the PolicyBinding already exists
	policyBinding := &iamv1alpha1.PolicyBinding{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: getDeterministicRoleName(roleRef, *invitation), Namespace: roleRef.Namespace}, policyBinding); err != nil {
		if errors.IsNotFound(err) {
			log.Info("PolicyBinding not found, creating")
		} else {
			return fmt.Errorf("failed to get PolicyBinding: %w", err)
		}
	} else {
		log.Info("PolicyBinding found, skipping creation")
		return nil
	}

	// Generate the ResourceRef
	resourceRef, err := r.getResourceRef(ctx, roleRef, *invitation)
	if err != nil {
		return fmt.Errorf("failed to generate ResourceRef: %w", err)
	}

	// Build the PolicyBinding
	policyBinding = &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getDeterministicRoleName(roleRef, *invitation),
			Namespace: roleRef.Namespace,
		},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef: iamv1alpha1.RoleReference{
				Name:      roleRef.Name,
				Namespace: roleRef.Namespace,
			},
			Subjects: []iamv1alpha1.Subject{
				{
					Kind: "User",
					Name: user.Name,
					UID:  string(user.GetUID()),
				},
			},
			ResourceSelector: iamv1alpha1.ResourceSelector{
				ResourceRef: &resourceRef,
			},
		},
	}

	// Create the PolicyBinding
	if err := r.Client.Create(ctx, policyBinding); err != nil {
		return fmt.Errorf("failed to create policy binding resource: %w", err)
	}

	log.Info("PolicyBinding created", "name", policyBinding.GetName())

	return nil
}

// getDeterministicResourceName generates a deterministic name for a resource to create based on the UserInvitation.
// This can be used in order to get/create the PolicyBinding, or other resources.
func getDeterministicResourceName(name string, ui iamv1alpha1.UserInvitation) string {
	// Sanitize the provided name: remove all whitespace characters (spaces, tabs, newlines) and convert to lower-case
	sanitized := strings.ToLower(strings.Join(strings.Fields(name), ""))
	return fmt.Sprintf("%s-%s", string(ui.GetUID()), sanitized)
}

// getResourceRef generates a ResourceRef for the PolicyBinding. As the ResourceRef depends on the roleRef
func (r *UserInvitationController) getResourceRef(ctx context.Context, roleRef *iamv1alpha1.RoleReference, ui iamv1alpha1.UserInvitation) (iamv1alpha1.ResourceReference, error) {
	log := logf.FromContext(ctx).WithName("userinvitation-generate-resource-ref")
	log.Info("Generating ResourceRef for UserInvitation", "roleRef", roleRef, "uiName", ui.GetName())

	for _, role := range r.uiRelatedRoles {
		if role.Name == roleRef.Name && role.Namespace == roleRef.Namespace {
			// If the roleRef contains the invitation-related roles, then the resourceRef is the UserInvitation
			return iamv1alpha1.ResourceReference{
				APIGroup:  iamv1alpha1.SchemeGroupVersion.Group,
				Kind:      "UserInvitation",
				Name:      ui.GetName(),
				UID:       string(ui.GetUID()),
				Namespace: ui.GetNamespace(),
			}, nil
		}
	}

	// If the roleRef is the organization role, then the resourceRef is the Organization

	// Get the Organization
	org := &resourcemanagerv1alpha1.Organization{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: ui.Spec.OrganizationRef.Name}, org); err != nil {
		return iamv1alpha1.ResourceReference{}, fmt.Errorf("failed to get Organization: %w", err)
	}

	return iamv1alpha1.ResourceReference{
		APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
		Kind:     "Organization",
		Name:     org.GetName(),
		UID:      string(org.GetUID()),
	}, nil
}

// deletePolicyBinding deletes a PolicyBinding for the invitee user to the organization.
// This is an idempotent operation.
func deletePolicyBinding(ctx context.Context, c client.Client, roleRef *iamv1alpha1.RoleReference, ui iamv1alpha1.UserInvitation) error {
	log := logf.FromContext(ctx).WithName("userinvitation-delete-policy-binding")
	log.Info("Deleting PolicyBinding for UserInvitation", "roleRef", roleRef, "uiName", ui.GetName())

	// Check if the PolicyBinding exists
	policyBinding := &iamv1alpha1.PolicyBinding{}
	if err := c.Get(ctx, client.ObjectKey{Name: getDeterministicRoleName(roleRef, ui), Namespace: roleRef.Namespace}, policyBinding); err != nil {
		if errors.IsNotFound(err) {
			log.Info("PolicyBinding not found, skipping deletion")
			return nil
		}
		log.Error(err, "Failed to get PolicyBinding")
		return fmt.Errorf("failed to get PolicyBinding: %w", err)
	}

	// Delete the PolicyBinding
	if err := c.Delete(ctx, &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getDeterministicRoleName(roleRef, ui),
			Namespace: roleRef.Namespace,
		},
	}); err != nil {
		return fmt.Errorf("failed to delete policy binding resource: %w", err)
	}

	log.Info("PolicyBinding deleted", "name", getDeterministicRoleName(roleRef, ui))

	return nil
}

// createOrganizationMembership creates an OrganizationMembership for the invitee user with roles from the invitation. This is an idempotent operation.
func (r *UserInvitationController) createOrganizationMembership(ctx context.Context, user *iamv1alpha1.User, ui *iamv1alpha1.UserInvitation) error {
	log := logf.FromContext(ctx).WithName("userinvitation-create-organization-membership")
	log.Info("Creating OrganizationMembership for userInvitation", "userInvitation", ui.GetName(), "user", user.GetName())

	// Convert IAM RoleReferences to ResourceManager RoleReferences
	roles := make([]resourcemanagerv1alpha1.RoleReference, 0, len(ui.Spec.Roles))
	for _, roleRef := range ui.Spec.Roles {
		roles = append(roles, resourcemanagerv1alpha1.RoleReference{
			Name:      roleRef.Name,
			Namespace: roleRef.Namespace,
		})
	}

	// Build the OrganizationMembership with roles
	organizationMembership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("member-%s", user.Name),
			Namespace: fmt.Sprintf("organization-%s", ui.Spec.OrganizationRef.Name),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: iamv1alpha1.SchemeGroupVersion.String(),
					Kind:       "User",
					Name:       user.GetName(),
					UID:        user.GetUID(),
				},
			},
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, organizationMembership, func() error {
		log.Info("Creating or updating invitation-related organization membership", "organization", organizationMembership.Spec.OrganizationRef.Name)
		organizationMembership.Spec = resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: ui.Spec.OrganizationRef.Name,
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: user.Name,
			},
			Roles: roles,
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update organization membership: %w", err)
	}

	log.Info("OrganizationMembership created with roles", "name", organizationMembership.GetName(), "roles", roles)

	return nil
}

// findUserInvitationsForUser finds all UserInvitation resources that reference a given User email.
// This is used to reconcile the UserInvitation resources when a User is created, in the case that the User was invited by a UserInvitation
// and the User was created after the UserInvitation was created.
func (r *UserInvitationController) findUserInvitationsForUser(ctx context.Context, obj client.Object) []reconcile.Request {
	log := logf.FromContext(ctx).WithName("find-userinvitations-for-user")

	user, ok := obj.(*iamv1alpha1.User)
	if !ok {
		log.Error(fmt.Errorf("unexpected object type %T, expected *iamv1alpha1.User", obj), "unexpected object type")
		return nil
	}

	if user.Spec.Email == "" {
		log.Error(fmt.Errorf("user has no email"), "user has no email")
		return nil
	}

	// List UserInvitations matching this user's email (case-insensitive)
	var uiList iamv1alpha1.UserInvitationList
	if err := r.Client.List(ctx, &uiList, client.MatchingFields{userEmailIndexKey: strings.ToLower(user.Spec.Email)}); err != nil {
		log.Error(err, "failed to list UserInvitations by email")
		return nil
	}

	requests := make([]reconcile.Request, 0, len(uiList.Items))
	for i := range uiList.Items {
		ui := uiList.Items[i]
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Name: ui.GetName(), Namespace: ui.GetNamespace()}})
	}

	log.Info("Found UserInvitations for user", "Number of UserInvitations", len(requests), "user", user.GetName())

	return requests
}

// userCreateOnlyPredicate triggers only on User create events.
var userCreateOnlyPredicate = predicate.Funcs{
	CreateFunc:  func(e event.CreateEvent) bool { return true },
	UpdateFunc:  func(e event.UpdateEvent) bool { return false },
	DeleteFunc:  func(e event.DeleteEvent) bool { return false },
	GenericFunc: func(e event.GenericEvent) bool { return false },
}

// createInvitationEmail creates an email to the invitee user to accept the invitation.
// This is an idempotent operation.
func (r *UserInvitationController) createInvitationEmail(ctx context.Context, ui *iamv1alpha1.UserInvitation) error {
	log := logf.FromContext(ctx).WithName("userinvitation-create-invitation-email")
	log.Info("Creating invitation email to user", "userInvitation", ui.GetName())

	emailName := getDeterministicEmailName(*ui)
	log.Info("Email name", "emailName", emailName)

	// Check if the Email already exists (idempotency)
	existingEmail := &notificationv1alpha1.Email{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: emailName, Namespace: ui.GetNamespace()}, existingEmail); err == nil {
		log.Info("Email already exists, skipping creation", "email", emailName)
		return nil
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check existing Email: %w", err)
	}

	variables := []notificationv1alpha1.EmailVariable{
		{
			Name:  "OrganizationDisplayName",
			Value: ui.Status.Organization.DisplayName,
		},
		{
			Name:  "UserInvitationName",
			Value: ui.GetName(),
		},
		{
			Name:  "InviterDisplayName",
			Value: ui.Status.InviterUser.DisplayName,
		},
	}

	// Compose the Email resource
	email := &notificationv1alpha1.Email{
		TypeMeta: metav1.TypeMeta{
			Kind: "Email",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      emailName,
			Namespace: ui.GetNamespace(),
		},
		Spec: notificationv1alpha1.EmailSpec{
			TemplateRef: notificationv1alpha1.TemplateReference{
				Name: r.UserInvitationEmailTemplateName,
			},
			Recipient: notificationv1alpha1.EmailRecipient{
				EmailAddress: ui.Spec.Email,
			},
			Variables: variables,
			Priority:  notificationv1alpha1.EmailPriorityNormal,
		},
	}

	if err := r.Client.Create(ctx, email); err != nil {
		log.Error(err, "failed to create Email resource", "email", email)
		return fmt.Errorf("failed to create Email resource: %w", err)
	}

	log.Info("Email resource created", "email", emailName)

	return nil
}

func (r *UserInvitationController) getUsersByEmail(ctx context.Context, email string) (*iamv1alpha1.UserList, error) {
	log := logf.FromContext(ctx).WithName("userinvitation-get-user-by-email")
	// Get the User that was invited by the UserInvitation
	var users iamv1alpha1.UserList
	if err := r.Client.List(ctx, &users, client.MatchingFields{userEmailIndexKey: strings.ToLower(email)}); err != nil {
		log.Error(err, "Failed to list Users by email")
		return nil, fmt.Errorf("failed to list Users by email: %w", err)
	}
	return &users, nil
}

// getInviteeUser returns the User identified by the invitation email. It returns (nil, nil)
// when the User resource does not yet exist so that the caller can decide to requeue
// without treating it as an error.
func (r *UserInvitationController) getInviteeUser(ctx context.Context, email string) (*iamv1alpha1.User, error) {
	log := logf.FromContext(ctx).WithName("userinvitation-get-invitee-user")
	log.Info("Getting Invitee User by email", "email", email)

	users, err := r.getUsersByEmail(ctx, email)
	if err != nil {
		log.Error(err, "Failed to get Invitee User by email")
		return nil, fmt.Errorf("failed to get Invitee User by email: %w", err)
	}
	if len(users.Items) == 0 {
		log.Info("Invitee User not found, skipping reconciliation. Reconciliation will be triggered again when the User is created.")
		return nil, nil
	}
	return &users.Items[0], nil
}

// getReferencedInviterUserInfo gets the display name and email address of the user who invited the user in the invitation.
func (r *UserInvitationController) getReferencedInviterUserInfo(ctx context.Context, inviterUserRef iamv1alpha1.UserReference) (string, string, error) {
	user := &iamv1alpha1.User{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: inviterUserRef.Name}, user); err != nil {
		return "", "", fmt.Errorf("failed to get inviterUser: %w", err)
	}

	displayName := strings.TrimSpace(user.Spec.GivenName + " " + user.Spec.FamilyName)
	if displayName == "" {
		displayName = user.Name
	}

	return displayName, user.Spec.Email, nil
}

// getOrganizationDisplayName gets the display name of the Organization referenced by the UserInvitation.
func (r *UserInvitationController) getReferencedOrganizationDisplayName(ctx context.Context, organizationRef resourcemanagerv1alpha1.OrganizationReference) (string, error) {
	// OrganizationDisplayName: fetch Organization resource to get display name
	org := &resourcemanagerv1alpha1.Organization{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: organizationRef.Name}, org); err != nil {
		return "", fmt.Errorf("failed to get Organization: %w", err)
	}
	organizationDisplayName := org.Annotations["kubernetes.io/display-name"]
	if organizationDisplayName == "" {
		organizationDisplayName = org.Name
	}

	return organizationDisplayName, nil
}

// getDeterministicEmailName generates a deterministic name for the Email resource to create based on the UserInvitation.
func getDeterministicEmailName(ui iamv1alpha1.UserInvitation) string {
	// We do not use the email, givenName or FamilyName as the may include forbidden characters for the Email resource name
	return getDeterministicResourceName("user-invitation", ui)
}

// getDeterministicRoleName generates a deterministic name for the Role resource to create based on the UserInvitation.
func getDeterministicRoleName(role *iamv1alpha1.RoleReference, ui iamv1alpha1.UserInvitation) string {
	return getDeterministicResourceName(role.Name, ui)
}

// isUserInvitationAccepted returns true if the UserInvitation is accepted
func isUserInvitationAccepted(ui *iamv1alpha1.UserInvitation) bool {
	return ui.Spec.State == iamv1alpha1.UserInvitationStateAccepted
}

// isUserInvitationDeclined returns true if the UserInvitation is declined
func isUserInvitationDeclined(ui *iamv1alpha1.UserInvitation) bool {
	return ui.Spec.State == iamv1alpha1.UserInvitationStateDeclined
}

// isUserInvitationExpired returns true if the UserInvitation is expired
func isUserInvitationExpired(ui *iamv1alpha1.UserInvitation) bool {
	now := metav1.NewTime(time.Now().UTC())
	if ui.Spec.ExpirationDate != nil && ui.Spec.ExpirationDate.Before(&now) {
		return true
	}
	return false
}

// grantAccessApproval grants the access to the invitee user for the organization.
func (r *UserInvitationController) grantAccessApproval(ctx context.Context, user *iamv1alpha1.User, ui *iamv1alpha1.UserInvitation) error {
	log := logf.FromContext(ctx).WithName("userinvitation-grant-access-approval").WithValues("userInvitation", ui.GetName())
	log.Info("Granting access approval to user", "userInvitation", ui.GetName(), "user")

	// ================================
	// WARNING: User may by nil
	// ================================

	// Look for existant PlatformAccessRejection for the invitee user
	if user != nil {
		// Only existant users can have a PlatformAccessRejection
		platformAccessRejection := &iamv1alpha1.PlatformAccessRejectionList{}
		if err := r.Client.List(ctx, platformAccessRejection, client.MatchingFields{uiPlatformAccessRejectionKey: user.GetName()}); err != nil {
			log.Error(err, "Failed to list PlatformAccessRejections")
			return fmt.Errorf("failed to list PlatformAccessRejections: %w", err)
		}
		if len(platformAccessRejection.Items) > 0 {
			// Remove the PlatformAccessRejection
			log.Info("PlatformAccessRejection already exists, removing it")
			if err := r.Client.Delete(ctx, &platformAccessRejection.Items[0]); err != nil && !errors.IsNotFound(err) {
				log.Error(err, "Failed to delete PlatformAccessRejection")
				return fmt.Errorf("failed to delete PlatformAccessRejection: %w", err)
			}
		}
	}

	// Look for existant PlatformAccessApproval for the invitee user
	// The user can already have a PlatformAccessApproval for the email address or the user reference
	userReferences := []string{strings.ToLower(ui.Spec.Email)}
	if user != nil {
		userReferences = append(userReferences, user.Name)
	}
	for _, reference := range userReferences {
		platformAccessApproval := &iamv1alpha1.PlatformAccessApprovalList{}
		if err := r.Client.List(ctx, platformAccessApproval, client.MatchingFields{uiPlatformAccessApprovalKey: reference}); err != nil {
			log.Error(err, "Failed to list PlatformAccessApprovals")
			return fmt.Errorf("failed to list PlatformAccessApprovals: %w", err)
		}
		if len(platformAccessApproval.Items) > 0 {
			log.Info("PlatformAccessApproval already exists, skipping grant access approval")
			return nil
		}
	}

	// Build the SubjectReference for the PlatformAccessApproval
	subjectRef := iamv1alpha1.SubjectReference{}
	if user == nil {
		// Invitee hasn't created an account yet – approve the email address
		subjectRef.Email = strings.ToLower(ui.Spec.Email)
	} else {
		// User exists – approve the specific user object
		subjectRef.UserRef = &iamv1alpha1.UserReference{
			Name: user.Name,
		}
	}

	// If here, no PlatformAccessApproval exists for the invitee user, create a new one
	platformAccessApproval := &iamv1alpha1.PlatformAccessApproval{
		ObjectMeta: metav1.ObjectMeta{
			Name: getDeterministicResourceName("platform-access-approval", *ui),
		},
		Spec: iamv1alpha1.PlatformAccessApprovalSpec{
			SubjectRef: subjectRef,
		},
	}
	if err := r.Client.Create(ctx, platformAccessApproval); err != nil {
		log.Error(err, "Failed to create PlatformAccessApproval")
		return fmt.Errorf("failed to create PlatformAccessApproval: %w", err)
	}

	return nil
}

func (r *UserInvitationController) updateUserInvitationInviteeUserStatus(ctx context.Context, ui *iamv1alpha1.UserInvitation) error {
	log := logf.FromContext(ctx).WithName("userinvitation-update-invitee-user-status")
	log.Info("Updating UserInvitationInviteeUserStatus status", "name", ui.GetName())

	if ui.Status.InviteeUser != nil {
		log.Info("Invitee User status already set, skipping update", "name", ui.GetName())
		return nil
	}

	// Attempt to resolve the invitee user
	user, err := r.getInviteeUser(ctx, ui.Spec.Email)
	if err != nil {
		return fmt.Errorf("failed to get Invitee User: %w", err)
	}

	// Condition to update the UserInvitation status
	var cond metav1.Condition

	if user == nil {
		// Invitee user not found yet
		cond = metav1.Condition{
			Type:    inviteeUserStatusUpdateConditionType,
			Status:  metav1.ConditionFalse,
			Reason:  "InvitedUserNotRegistered",
			Message: "The invited user has not registered their account yet.",
		}
	} else {
		// Invitee user found – update the Status field on the invitation
		ui.Status.InviteeUser = &iamv1alpha1.UserInvitationInviteeUserStatus{
			Name: user.Name,
		}
		cond = metav1.Condition{
			Type:    inviteeUserStatusUpdateConditionType,
			Status:  metav1.ConditionTrue,
			Reason:  "InvitedUserRegistered",
			Message: "Confirmed the invited user has an account registered with the platform.",
		}
	}

	if updateErr := r.updateUserInvitationStatus(ctx, ui, cond); updateErr != nil {
		return fmt.Errorf("failed to update UserInvitation status: %w", updateErr)
	}

	return nil
}
