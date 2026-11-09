package v1alpha1

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"go.miloapis.com/milo/internal/emaildomain"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var userinvitationlog = logf.Log.WithName("userinvitation-resource")

const userInvitationCompositeKey = "userInvitationByEmailAndOrg"
const organizationMembershipCompositeKey = "organizationMembershipByOrgAndUser"
const userEmailKey = "userByEmail"

// emailVerifiedExtraKey is the userInfo.extra key zitadel-provider stamps with
// the requester's email verification verdict. The emailverification admission
// plugin reads the same key; "true" is the only verified value.
const emailVerifiedExtraKey = "iam.miloapis.com/emailVerified"

const systemMastersGroup = "system:masters"

// buildUserInvitationCompositeKey returns a composite key of lowercased email and organization name
func buildUserInvitationCompositeKey(ui iamv1alpha1.UserInvitation) string {
	return fmt.Sprintf("%s|%s", strings.ToLower(ui.Spec.Email), ui.Spec.OrganizationRef.Name)
}

// buildOrganizationMembershipCompositeKey returns a composite key of organization name and user name
func buildOrganizationMembershipCompositeKey(organizationName, userName string) string {
	return fmt.Sprintf("%s|%s", organizationName, userName)
}

// SetupUserInvitationWebhooksWithManager sets up the webhooks for UserInvitation resources.
func SetupUserInvitationWebhooksWithManager(mgr ctrl.Manager, systemNamespace, assignableRolesNamespace string) error {
	userinvitationlog.Info("Setting up iam.miloapis.com userinvitation webhooks")

	// Index UserInvitation by composite key (lowercased email + organization name) for efficient duplicate checks
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.UserInvitation{}, userInvitationCompositeKey, func(raw client.Object) []string {
		ui := raw.(*iamv1alpha1.UserInvitation)
		return []string{buildUserInvitationCompositeKey(*ui)}
	}); err != nil {
		return fmt.Errorf("failed to set field index on UserInvitation by composite key: %w", err)
	}

	// Index OrganizationMembership by composite key (organization name + user name) for lookup
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &resourcemanagerv1alpha1.OrganizationMembership{}, organizationMembershipCompositeKey, func(raw client.Object) []string {
		om := raw.(*resourcemanagerv1alpha1.OrganizationMembership)
		return []string{buildOrganizationMembershipCompositeKey(om.Spec.OrganizationRef.Name, om.Spec.UserRef.Name)}
	}); err != nil {
		return fmt.Errorf("failed to set field index on OrganizationMembership by composite key: %w", err)
	}

	// Index User by email for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.User{}, userEmailKey, func(raw client.Object) []string {
		user := raw.(*iamv1alpha1.User)
		return []string{strings.ToLower(user.Spec.Email)}
	}); err != nil {
		return fmt.Errorf("failed to set field index on User by email: %w", err)
	}

	return ctrl.NewWebhookManagedBy(mgr, &iamv1alpha1.UserInvitation{}).
		WithDefaulter(&UserInvitationMutator{
			client: mgr.GetClient(),
		}).
		WithValidator(&UserInvitationValidator{
			client:                   mgr.GetClient(),
			systemNamespace:          systemNamespace,
			assignableRolesNamespace: assignableRolesNamespace,
			emailDomains:             emaildomain.NewVerifier(mgr.GetClient(), mgr.GetConfig()),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-iam-miloapis-com-v1alpha1-userinvitation,mutating=true,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=userinvitations,verbs=create,versions=v1alpha1,name=muserinvitation.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// UserInvitationMutator sets default values for UserInvitation resources.
type UserInvitationMutator struct {
	client client.Client
}

// Default sets the InvitedBy field to the requesting user if not already set.
func (m *UserInvitationMutator) Default(ctx context.Context, ui *iamv1alpha1.UserInvitation) error {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		userinvitationlog.Error(err, "failed to get admission request from context", "name", ui.GetName())
		return fmt.Errorf("failed to get request from context: %w", err)
	}

	// The platform, such as the email domain joining controller, makes offers
	// that no user sent, so it has no User to record as the inviter.
	if slices.Contains(req.UserInfo.Groups, systemMastersGroup) {
		return nil
	}

	inviterUser := &iamv1alpha1.User{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: string(req.UserInfo.UID)}, inviterUser); err != nil {
		userinvitationlog.Error(err, "failed to get user '%s' from iam.miloapis.com API", string(req.UserInfo.UID))
		return errors.NewInternalError(fmt.Errorf("failed to get user '%s' from iam.miloapis.com API: %w", string(req.UserInfo.UID), err))
	}

	ui.Spec.InvitedBy = iamv1alpha1.UserReference{
		Name: inviterUser.Name,
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-iam-miloapis-com-v1alpha1-userinvitation,mutating=false,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=userinvitations,verbs=create;update,versions=v1alpha1,name=vuserinvitation.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// UserInvitationValidator validates UserInvitation resources.
type UserInvitationValidator struct {
	client                   client.Client
	systemNamespace          string
	assignableRolesNamespace string
	emailDomains             *emaildomain.Verifier
}

// ValidateCreate ensures the expiration date, if provided, is not already expired.
func (v *UserInvitationValidator) ValidateCreate(ctx context.Context, ui *iamv1alpha1.UserInvitation) (admission.Warnings, error) {
	userinvitationlog.Info("Validating UserInvitation", "name", ui.Name)

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		userinvitationlog.Error(err, "failed to get admission request from context", "name", ui.GetName())
		return nil, fmt.Errorf("failed to get request from context: %w", err)
	}

	var errs field.ErrorList

	if isEmailDomainOffer(ui) && !slices.Contains(req.UserInfo.Groups, systemMastersGroup) {
		errs = append(errs, field.Forbidden(
			field.NewPath("metadata", "labels").Key(emaildomain.JoinMethodLabel),
			"only the platform can create email domain offers",
		))
	}

	// Ensure the expiration date is in the future
	if ui.Spec.ExpirationDate != nil {
		now := metav1.NewTime(time.Now().UTC())
		if ui.Spec.ExpirationDate.Before(&now) {
			errs = append(errs, field.Invalid(field.NewPath("spec").Child("expirationDate"), ui.Spec.ExpirationDate.String(), "expirationDate must be in the future"))
		}
	}

	// Ensure the ui OrganizationRef is in the organization's namespace
	if fmt.Sprintf("organization-%s", ui.Spec.OrganizationRef.Name) != req.Namespace {
		errs = append(errs, field.Invalid(field.NewPath("spec").Child("organizationRef"), ui.Spec.OrganizationRef.Name, "organizationRef must be the same as the requesting user's organization"))
	}

	// Ensure there is no live, pending UserInvitation for the same email and organization.
	// Terminal invitations (declined, expired, or accepted-but-stale) do not block a
	// fresh invite: re-inviting someone whose invitation lapsed is ordinary onboarding.
	var existing iamv1alpha1.UserInvitationList
	if err := v.client.List(ctx, &existing,
		client.MatchingFields{userInvitationCompositeKey: buildUserInvitationCompositeKey(*ui)}); err != nil {
		userinvitationlog.Error(err, "failed to list existing UserInvitations by email", "email", ui.Spec.Email)
		return nil, errors.NewInternalError(fmt.Errorf("failed to list existing UserInvitations: %w", err))
	}
	for i := range existing.Items {
		if existing.Items[i].Spec.State == iamv1alpha1.UserInvitationStatePending && !existing.Items[i].IsExpired() {
			errs = append(errs, field.Duplicate(
				field.NewPath("spec").Child("organizationRef"),
				ui.Spec.OrganizationRef.Name,
			))
			break
		}
	}

	for i, role := range ui.Spec.Roles {
		canGetRole := true
		if role.Name == "" {
			canGetRole = false
			errs = append(errs, field.Invalid(field.NewPath("spec").Child("roles").Index(i).Child("name"), role.Name, "name is required"))
		}
		allowedNamespaces := []string{req.Namespace, v.systemNamespace, v.assignableRolesNamespace}
		if !slices.Contains(allowedNamespaces, role.Namespace) {
			canGetRole = false
			errs = append(errs, field.Invalid(field.NewPath("spec").Child("roles").Index(i).Child("namespace"), role.Namespace, "namespace is invalid"))
		}
		if !canGetRole {
			continue
		}

		foundRole := &iamv1alpha1.Role{}
		if err := v.client.Get(ctx, client.ObjectKey{Name: role.Name, Namespace: role.Namespace}, foundRole); err != nil {
			if errors.IsNotFound(err) {
				errs = append(errs, field.NotFound(field.NewPath("spec").Child("roles").Index(i).Child("name"), fmt.Sprintf("%s/%s", role.Namespace, role.Name)))
				continue
			}
			userinvitationlog.Error(err, "failed to get role reference", "role", role)
			return nil, fmt.Errorf("failed to get role reference: %w", err)
		}
	}

	// Ensure the user is not already a member of the organization
	if err := v.validateOrganizationMembershipExists(ctx, ui); err != nil {
		errs = append(errs, field.Invalid(field.NewPath("spec").Child("email"), ui.Spec.Email, err.Error()))
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("UserInvitation").GroupKind(), ui.Name, errs)
	}

	return nil, nil
}

func (v *UserInvitationValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *iamv1alpha1.UserInvitation) (admission.Warnings, error) {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get request from context: %w", err)
	}

	// Removing the labels would turn an offer into an ordinary invitation and
	// skip the checks below.
	if !slices.Contains(req.UserInfo.Groups, systemMastersGroup) &&
		(oldObj.Labels[emaildomain.JoinMethodLabel] != newObj.Labels[emaildomain.JoinMethodLabel] ||
			oldObj.Labels[emaildomain.EmailDomainLabel] != newObj.Labels[emaildomain.EmailDomainLabel]) {
		return nil, errors.NewInvalid(
			iamv1alpha1.SchemeGroupVersion.WithKind("UserInvitation").GroupKind(),
			newObj.Name,
			field.ErrorList{field.Forbidden(field.NewPath("metadata", "labels"), "email domain offer labels cannot be changed")},
		)
	}

	accepting := oldObj.Spec.State == iamv1alpha1.UserInvitationStatePending && newObj.Spec.State == iamv1alpha1.UserInvitationStateAccepted
	if accepting && isEmailDomainOffer(newObj) {
		if err := v.validateEmailDomainOfferAcceptance(ctx, req, newObj); err != nil {
			return nil, err
		}
	}

	// Reject accepting an invitation whose expiration date has already passed.
	// Accepting an expired invitation used to silently succeed while granting no
	// membership, which left a stale "Accepted" record that blocked every future
	// invite to the same address. Rejecting here tells the invitee immediately
	// that the link is no longer valid so they can request a new invitation.
	if accepting && newObj.IsExpired() {
		return nil, errors.NewInvalid(
			iamv1alpha1.SchemeGroupVersion.WithKind("UserInvitation").GroupKind(),
			newObj.Name,
			field.ErrorList{
				field.Invalid(
					field.NewPath("spec").Child("state"),
					newObj.Spec.State,
					"the invitation has expired and can no longer be accepted; ask the inviter to send a new invitation",
				),
			},
		)
	}
	return nil, nil
}

// validateEmailDomainOfferAcceptance lets someone accept an email domain offer
// only with a verified email address, and only while a verified Domain in one
// of the organization's projects still covers the offer's domain.
func (v *UserInvitationValidator) validateEmailDomainOfferAcceptance(ctx context.Context, req admission.Request, ui *iamv1alpha1.UserInvitation) error {
	statePath := field.NewPath("spec").Child("state")
	invalid := func(msg string) error {
		return errors.NewInvalid(
			iamv1alpha1.SchemeGroupVersion.WithKind("UserInvitation").GroupKind(),
			ui.Name,
			field.ErrorList{field.Invalid(statePath, ui.Spec.State, msg)},
		)
	}

	verified := req.UserInfo.Extra[emailVerifiedExtraKey]
	if len(verified) == 0 || verified[0] != "true" {
		return invalid("verify your email address before joining an organization through your email domain")
	}

	domain := ui.Labels[emaildomain.EmailDomainLabel]
	if domain == "" || emaildomain.Of(ui.Spec.Email) != domain {
		return invalid("this offer is not for your email domain")
	}

	verifiedDomains, err := v.emailDomains.VerifiedDomains(ctx, ui.Spec.OrganizationRef.Name)
	if err != nil {
		userinvitationlog.Error(err, "failed to check verified Domains", "organization", ui.Spec.OrganizationRef.Name)
		return errors.NewInternalError(fmt.Errorf("failed to check verified Domains: %w", err))
	}
	if !emaildomain.Covers(verifiedDomains, domain) {
		return invalid("this organization no longer has a verified Domain for " + domain + "; ask an owner for an invitation")
	}
	return nil
}

func isEmailDomainOffer(ui *iamv1alpha1.UserInvitation) bool {
	return ui.Labels[emaildomain.JoinMethodLabel] == emaildomain.JoinMethodEmailDomain
}

func (v *UserInvitationValidator) ValidateDelete(ctx context.Context, obj *iamv1alpha1.UserInvitation) (admission.Warnings, error) {
	return nil, nil
}

func (v *UserInvitationValidator) validateOrganizationMembershipExists(ctx context.Context, ui *iamv1alpha1.UserInvitation) error {
	var existing iamv1alpha1.UserList
	if err := v.client.List(ctx, &existing,
		client.MatchingFields{userEmailKey: strings.ToLower(ui.Spec.Email)}); err != nil {
		userinvitationlog.Error(err, "failed to list existing Users by email", "email", ui.Spec.Email)
		return errors.NewInternalError(fmt.Errorf("failed to list existing UserInvitations: %w", err))
	}
	if len(existing.Items) == 0 {
		// No user found, so no organization memberships are related to this invitatiom
		return nil
	}

	var existingOrganizationMemberships resourcemanagerv1alpha1.OrganizationMembershipList
	if err := v.client.List(ctx, &existingOrganizationMemberships,
		client.MatchingFields{organizationMembershipCompositeKey: buildOrganizationMembershipCompositeKey(ui.Spec.OrganizationRef.Name, existing.Items[0].Name)}); err != nil {
		userinvitationlog.Error(err, "failed to list existing OrganizationMemberships by organization and user", "organization", ui.Spec.OrganizationRef.Name, "userName", existing.Items[0].Name)
		return errors.NewInternalError(fmt.Errorf("failed to list existing OrganizationMemberships: %w", err))
	}
	if len(existingOrganizationMemberships.Items) == 0 {
		return nil
	}
	return fmt.Errorf("the user is already a member of the organization")
}
