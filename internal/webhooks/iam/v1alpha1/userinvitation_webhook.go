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

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var userinvitationlog = logf.Log.WithName("userinvitation-resource")

const userInvitationCompositeKey = "userInvitationByEmailAndOrg"
const organizationMembershipCompositeKey = "organizationMembershipByOrgAndUser"
const userEmailKey = "userByEmail"

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

// +kubebuilder:webhook:path=/validate-iam-miloapis-com-v1alpha1-userinvitation,mutating=false,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=userinvitations,verbs=create,versions=v1alpha1,name=vuserinvitation.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// UserInvitationValidator validates UserInvitation resources.
type UserInvitationValidator struct {
	client                   client.Client
	systemNamespace          string
	assignableRolesNamespace string
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

	// Ensure there is no existing UserInvitation for the same email and organization
	var existing iamv1alpha1.UserInvitationList
	if err := v.client.List(ctx, &existing,
		client.MatchingFields{userInvitationCompositeKey: buildUserInvitationCompositeKey(*ui)}); err != nil {
		userinvitationlog.Error(err, "failed to list existing UserInvitations by email", "email", ui.Spec.Email)
		return nil, errors.NewInternalError(fmt.Errorf("failed to list existing UserInvitations: %w", err))
	}
	if len(existing.Items) > 0 {
		errs = append(errs, field.Duplicate(
			field.NewPath("spec").Child("organizationRef"),
			ui.Spec.OrganizationRef.Name,
		))
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
	return nil, nil
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
