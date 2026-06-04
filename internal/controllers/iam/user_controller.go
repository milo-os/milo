package iam

import (
	"context"
	"fmt"
	"strings"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// userMembershipCleanupFinalizer ensures OrganizationMembership resources are
	// deleted before the User object is removed from the API server.
	userMembershipCleanupFinalizer  = "iam.miloapis.com/user-membership-cleanup"
	userReadyConditionType          = "Ready"
	platformAccessApprovalIndexKey  = "iam.miloapis.com/platformaccessapprovalkey"
	platformAccessRejectionIndexKey = "iam.miloapis.com/platformaccessrejectionkey"
)

func buildPlatformAccessApprovalIndexKey(subject *iamv1alpha1.SubjectReference) string {
	if subject.UserRef != nil {
		return subject.UserRef.Name
	}
	return strings.ToLower(subject.Email)
}

// UserController reconciles a User object
type UserController struct {
	Client client.Client
}

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users/status,verbs=update
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=userdeactivations,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=policybindings,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=userpreferences,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=platformaccessapprovals,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=platformaccessrejections,verbs=get;list;watch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizationmemberships,verbs=list;delete

// Reconcile is the main reconciliation loop for the UserController.
func (r *UserController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithName("user-controller")
	log.Info("Starting reconciliation", "request", req.Name)

	user := &iamv1alpha1.User{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name}, user); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get User: %w", err)
	}
	log.Info("reconciling User", "user", user.Name)

	// When the user is being deleted, clean up OrganizationMembership resources
	// before the object is removed.
	if !user.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(user, userMembershipCleanupFinalizer) {
			if err := r.cleanupOrganizationMemberships(ctx, user); err != nil {
				log.Error(err, "failed to clean up OrganizationMemberships during user deletion")
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(user, userMembershipCleanupFinalizer)
			if err := r.Client.Update(ctx, user); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove membership cleanup finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the membership-cleanup finalizer is present on every active User so
	// that OrganizationMemberships are deleted before the User is removed.
	if !controllerutil.ContainsFinalizer(user, userMembershipCleanupFinalizer) {
		controllerutil.AddFinalizer(user, userMembershipCleanupFinalizer)
		if err := r.Client.Update(ctx, user); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add membership cleanup finalizer: %w", err)
		}
	}

	// Ensure owner references are set on PolicyBinding and UserPreference resources
	if err := r.ensureOwnerReferences(ctx, user); err != nil {
		log.Error(err, "Failed to ensure owner references")
		return ctrl.Result{}, err
	}

	// Reconcile the name-review annotation based on whether givenName and familyName match
	if err := r.reconcileNameReviewAnnotation(ctx, user); err != nil {
		log.Error(err, "Failed to reconcile name review annotation")
		return ctrl.Result{}, err
	}

	// Determine desired state based on existence of any UserDeactivation for this user
	var udList iamv1alpha1.UserDeactivationList
	if err := r.Client.List(ctx, &udList, client.MatchingFields{"spec.userRef.name": user.Name}); err != nil {
		log.Error(err, "failed to list UserDeactivations")
		return ctrl.Result{}, fmt.Errorf("failed to list UserDeactivations: %w", err)
	}

	// Capture the current status to detect changes later
	oldUserStatus := user.Status.DeepCopy()

	// Get the user access approval status
	registrationApproval, err := r.getUserAccessApprovalStatus(ctx, user)
	if err != nil {
		log.Error(err, "failed to get user access approval status")
		return ctrl.Result{}, fmt.Errorf("failed to get user access approval status: %w", err)
	}
	user.Status.RegistrationApproval = registrationApproval

	// Defining the desired user state
	var desiredState iamv1alpha1.UserState
	// Only mark the user Inactive if there is at least one processed (Ready=True) UserDeactivation
	hasProcessedDeactivation := false
	for i := range udList.Items {
		ud := udList.Items[i]
		if meta.IsStatusConditionTrue(ud.Status.Conditions, iamv1alpha1.UserDeactivationReadyCondition) {
			hasProcessedDeactivation = true
			break
		}
	}
	if hasProcessedDeactivation {
		desiredState = iamv1alpha1.UserStateInactive
	} else {
		desiredState = iamv1alpha1.UserStateActive
	}
	user.Status.State = desiredState

	// Also set/refresh Ready condition to reflect change
	userCondition := metav1.Condition{
		Type:               userReadyConditionType,
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            fmt.Sprintf("User state set to %s based on processed UserDeactivation presence", desiredState),
		LastTransitionTime: metav1.Now(),
	}
	meta.SetStatusCondition(&user.Status.Conditions, userCondition)
	// Update or set condition
	// Only update the status if it actually changed to avoid unnecessary API calls
	if !equality.Semantic.DeepEqual(oldUserStatus, &user.Status) {
		log.Info("Updating User status", "userName", user.GetName())
		if err := r.Client.Status().Update(ctx, user); err != nil {
			log.Error(err, "Failed to update User status")
			return ctrl.Result{}, fmt.Errorf("failed to update User status: %w", err)
		}
	} else {
		log.Info("User status unchanged, skipping update", "user", user.GetName())
	}

	return ctrl.Result{}, nil
}

// reconcileNameReviewAnnotation adds or removes the name-review-required annotation depending on
// whether givenName and familyName are identical. This situation arises when an identity provider
// (e.g. GitHub) supplies a single display name and the system copies it into both fields.
func (r *UserController) reconcileNameReviewAnnotation(ctx context.Context, user *iamv1alpha1.User) error {
	log := log.FromContext(ctx).WithName("reconcile-name-review-annotation")

	annotations := user.GetAnnotations()
	_, annotationPresent := annotations[iamv1alpha1.UserNameReviewRequiredAnnotation]
	namesAreEqual := user.Spec.GivenName != "" && user.Spec.GivenName == user.Spec.FamilyName

	switch {
	case namesAreEqual && !annotationPresent:
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[iamv1alpha1.UserNameReviewRequiredAnnotation] = "true"
		user.SetAnnotations(annotations)
		if err := r.Client.Update(ctx, user); err != nil {
			return fmt.Errorf("failed to add name-review annotation: %w", err)
		}
		log.Info("Added name-review-required annotation", "user", user.Name)

	case !namesAreEqual && annotationPresent:
		delete(annotations, iamv1alpha1.UserNameReviewRequiredAnnotation)
		user.SetAnnotations(annotations)
		if err := r.Client.Update(ctx, user); err != nil {
			return fmt.Errorf("failed to remove name-review annotation: %w", err)
		}
		log.Info("Removed name-review-required annotation", "user", user.Name)
	}

	return nil
}

// ensureOwnerReferences ensures that PolicyBinding and UserPreference resources have proper owner references
func (r *UserController) ensureOwnerReferences(ctx context.Context, user *iamv1alpha1.User) error {
	log := log.FromContext(ctx).WithName("ensure-owner-references")

	// Create owner reference for the user
	ownerRef := metav1.OwnerReference{
		APIVersion: iamv1alpha1.SchemeGroupVersion.String(),
		Kind:       "User",
		Name:       user.Name,
		UID:        user.UID,
	}

	// Update PolicyBinding for user self-management
	policyBindingName := fmt.Sprintf("user-self-manage-%s", user.Name)
	policyBinding := &iamv1alpha1.PolicyBinding{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: policyBindingName, Namespace: "milo-system"}, policyBinding)
	if apierrors.IsNotFound(err) {
		// PolicyBinding doesn't exist, webhook should have created it
		log.Info("PolicyBinding not found, skipping (webhook should create it)", "user", user.Name, "policyBinding", policyBindingName)
	} else if err != nil {
		return fmt.Errorf("failed to get policy binding: %w", err)
	} else if !hasOwnerReference(policyBinding.OwnerReferences, ownerRef) {
		policyBinding.OwnerReferences = append(policyBinding.OwnerReferences, ownerRef)
		if err := r.Client.Update(ctx, policyBinding); err != nil {
			return fmt.Errorf("failed to update policy binding with owner reference: %w", err)
		}
		log.Info("Updated PolicyBinding with owner reference", "user", user.Name)
	}

	// Update UserPreference
	userPreferenceName := fmt.Sprintf("userpreference-%s", user.Name)
	userPreference := &iamv1alpha1.UserPreference{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: userPreferenceName}, userPreference)
	if apierrors.IsNotFound(err) {
		// UserPreference doesn't exist, webhook should have created it
		log.Info("UserPreference not found, skipping (webhook should create it)", "user", user.Name, "userPreference", userPreferenceName)
	} else if err != nil {
		return fmt.Errorf("failed to get user preference: %w", err)
	} else if !hasOwnerReference(userPreference.OwnerReferences, ownerRef) {
		userPreference.OwnerReferences = append(userPreference.OwnerReferences, ownerRef)
		if err := r.Client.Update(ctx, userPreference); err != nil {
			return fmt.Errorf("failed to update user preference with owner reference: %w", err)
		}
		log.Info("Updated UserPreference with owner reference", "user", user.Name)
	}

	// Update UserPreference PolicyBinding for user preference management
	userPreferencePolicyBindingName := fmt.Sprintf("userpreference-self-manage-%s", user.Name)
	userPreferencePolicyBinding := &iamv1alpha1.PolicyBinding{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: userPreferencePolicyBindingName, Namespace: "milo-system"}, userPreferencePolicyBinding)
	if apierrors.IsNotFound(err) {
		// UserPreference PolicyBinding doesn't exist, webhook should have created it
		log.Info("UserPreference PolicyBinding not found, skipping (webhook should create it)", "user", user.Name, "policyBinding", userPreferencePolicyBindingName)
	} else if err != nil {
		return fmt.Errorf("failed to get user preference policy binding: %w", err)
	} else if !hasOwnerReference(userPreferencePolicyBinding.OwnerReferences, ownerRef) {
		userPreferencePolicyBinding.OwnerReferences = append(userPreferencePolicyBinding.OwnerReferences, ownerRef)
		if err := r.Client.Update(ctx, userPreferencePolicyBinding); err != nil {
			return fmt.Errorf("failed to update user preference policy binding with owner reference: %w", err)
		}
		log.Info("Updated UserPreference PolicyBinding with owner reference", "user", user.Name)
	}

	return nil
}

// cleanupOrganizationMemberships deletes all OrganizationMembership resources that
// reference the given user. This is called when a user is being deleted so that
// memberships (including last-owner memberships) are removed before the User
// object is garbage-collected.
//
// Depends on the "spec.userRef.name" field index registered by
// OrganizationMembershipController.SetupWithManager. Both controllers share the
// same manager, so the index is available when this function runs.
func (r *UserController) cleanupOrganizationMemberships(ctx context.Context, user *iamv1alpha1.User) error {
	log := log.FromContext(ctx).WithName("cleanup-organization-memberships")

	var membershipList resourcemanagerv1alpha1.OrganizationMembershipList
	if err := r.Client.List(ctx, &membershipList, client.MatchingFields{"spec.userRef.name": user.Name}); err != nil {
		return fmt.Errorf("failed to list OrganizationMemberships for user %s: %w", user.Name, err)
	}

	for i := range membershipList.Items {
		membership := &membershipList.Items[i]
		log.Info("deleting OrganizationMembership for deleted user", "membership", membership.Name, "namespace", membership.Namespace)
		if err := r.Client.Delete(ctx, membership); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete OrganizationMembership %s/%s: %w", membership.Namespace, membership.Name, err)
		}
	}

	return nil
}

// hasOwnerReference checks if the owner reference already exists
func hasOwnerReference(refs []metav1.OwnerReference, ref metav1.OwnerReference) bool {
	for _, r := range refs {
		if r.UID == ref.UID {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *UserController) SetupWithManager(mgr ctrl.Manager) error {
	// Index PlatformAccessApproval for efficient lookups
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.PlatformAccessApproval{}, platformAccessApprovalIndexKey, func(obj client.Object) []string {
		paa, ok := obj.(*iamv1alpha1.PlatformAccessApproval)
		if !ok {
			return nil
		}
		return []string{buildPlatformAccessApprovalIndexKey(&paa.Spec.SubjectRef)}
	}); err != nil {
		return fmt.Errorf("failed to set field index on PlatformAccessApproval: %w", err)
	}

	// Index PlatformAccessRejection for efficient lookups
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.PlatformAccessRejection{}, platformAccessRejectionIndexKey, func(obj client.Object) []string {
		par, ok := obj.(*iamv1alpha1.PlatformAccessRejection)
		if !ok {
			return nil
		}
		return []string{par.Spec.UserRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to set field index on PlatformAccessRejection: %w", err)
	}

	// Index UserDeactivation by spec.userRef.name for efficient lookups
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.UserDeactivation{}, "spec.userRef.name", func(obj client.Object) []string {
		ud, ok := obj.(*iamv1alpha1.UserDeactivation)
		if !ok {
			return nil
		}
		if ud.Spec.UserRef.Name == "" {
			// This should never happen, as the there is a webhook that validates the UserDeactivation
			return nil
		}
		return []string{ud.Spec.UserRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to set field index on UserDeactivation: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&iamv1alpha1.User{}).
		Watches(&iamv1alpha1.UserDeactivation{}, handler.EnqueueRequestsFromMapFunc(r.findUserDeactivationsForUser)).
		Watches(&iamv1alpha1.PlatformAccessApproval{}, handler.EnqueueRequestsFromMapFunc(r.findPlatformAccessApprovalsForUser)).
		Watches(&iamv1alpha1.PlatformAccessRejection{}, handler.EnqueueRequestsFromMapFunc(r.findPlatformAccessRejectionsForUser)).
		Named("user").
		Complete(r)
}

// findUserDeactivationsForUser finds all UserDeactivation resources that reference a given User
func (r *UserController) findUserDeactivationsForUser(ctx context.Context, obj client.Object) []reconcile.Request {
	log := log.FromContext(ctx).WithName("find-user-deactivations-for-user")

	userDeactivation, ok := obj.(*iamv1alpha1.UserDeactivation)
	if !ok {
		log.Error(fmt.Errorf("unexpected object type %T, expected *iamv1alpha1.UserDeactivation", obj), "unexpected object type")
		return nil
	}
	if userDeactivation.Spec.UserRef.Name == "" {
		// This should never happen, as the there is a webhook that validates the UserDeactivation
		log.Error(fmt.Errorf("user deactivation has no user reference"), "user deactivation has no user reference")
		return nil
	}
	log.Info("found UserDeactivation for user", "user", userDeactivation.Spec.UserRef.Name, "userDeactivation", userDeactivation.Name)

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      userDeactivation.Spec.UserRef.Name,
				Namespace: userDeactivation.Namespace,
			},
		},
	}
}

// findPlatformAccessApprovalsForUser finds all PlatformAccessApproval resources that reference a given User
func (r *UserController) findPlatformAccessApprovalsForUser(ctx context.Context, obj client.Object) []reconcile.Request {
	log := log.FromContext(ctx).WithName("find-platform-access-approval-for-user")
	paa, ok := obj.(*iamv1alpha1.PlatformAccessApproval)
	if !ok {
		log.Error(fmt.Errorf("unexpected object type %T, expected *iamv1alpha1.PlatformAccessApproval", obj), "unexpected object type")
		return nil
	}

	userRef := paa.Spec.SubjectRef.UserRef
	if userRef == nil {
		log.Info("platform access approval has no user reference, skipping as probably is for an user invitation", "platformAccessApproval", paa.Name)
		return nil
	}
	log.Info("found PlatformAccessApproval for user", "user", userRef.Name, "platformAccessApproval", paa.Name)

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name: userRef.Name,
			},
		},
	}
}

// findPlatformAccessRejectionsForUser finds all PlatformAccessRejection resources that reference a given User
func (r *UserController) findPlatformAccessRejectionsForUser(ctx context.Context, obj client.Object) []reconcile.Request {
	log := log.FromContext(ctx).WithName("find-platform-access-rejection-for-user")
	par, ok := obj.(*iamv1alpha1.PlatformAccessRejection)
	if !ok {
		log.Error(fmt.Errorf("unexpected object type %T, expected *iamv1alpha1.PlatformAccessRejection", obj), "unexpected object type")
		return nil
	}
	log.Info("found PlatformAccessRejection for user", "user", par.Spec.UserRef.Name, "platformAccessRejection", par.Name)

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name: par.Spec.UserRef.Name,
			},
		},
	}
}

func (r *UserController) getUserAccessApprovalStatus(ctx context.Context, user *iamv1alpha1.User) (iamv1alpha1.RegistrationApprovalState, error) {
	log := log.FromContext(ctx).WithName("get-user-access-approval-status")

	// Webhooks validations warranties that there is only one PlatformAccessApproval or PlatformAccessRejection related to the user

	// Check if it has a PlatformAccessApproval related to email address or user reference
	userReferences := []string{user.Spec.Email, user.Name}
	for _, reference := range userReferences {
		paas := &iamv1alpha1.PlatformAccessApprovalList{}
		if err := r.Client.List(ctx, paas, client.MatchingFields{platformAccessApprovalIndexKey: reference}); err != nil {
			log.Error(err, "failed to list platformaccessapprovals", "reference", reference)
			return "", fmt.Errorf("failed to list platformaccessapprovals: %w", err)
		}
		if len(paas.Items) > 0 {
			return iamv1alpha1.RegistrationApprovalStateApproved, nil
		}
	}

	// Check if it has a PlatformAccessRejection related to user reference
	par := &iamv1alpha1.PlatformAccessRejectionList{}
	if err := r.Client.List(ctx, par, client.MatchingFields{platformAccessRejectionIndexKey: user.Name}); err != nil {
		log.Error(err, "failed to list platformaccessrejections", "user", user.Name)
		return "", fmt.Errorf("failed to list platformaccessrejections: %w", err)
	}
	if len(par.Items) > 0 {
		return iamv1alpha1.RegistrationApprovalStateRejected, nil
	}

	// If no PlatformAccessApproval or PlatformAccessRejection is found, return Pending
	return iamv1alpha1.RegistrationApprovalStatePending, nil

}
