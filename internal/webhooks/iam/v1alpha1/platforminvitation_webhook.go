package v1alpha1

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

var platforminvitationlog = logf.Log.WithName("platforminvitation-resource")

const platformInvitationUserEmailIndexKey = "iam.miloapis.com/user-email-key"
const platformInvitationEmailIndexKey = "iam.miloapis.com/platforminvitation-email-key"

// SetupPlatformInvitationWebhooksWithManager sets up the webhooks for PlatformInvitation resources.
func SetupPlatformInvitationWebhooksWithManager(mgr ctrl.Manager) error {
	platforminvitationlog.Info("Setting up iam.miloapis.com platforminvitation webhooks")

	// Index users by lower-cased email for efficient lookups during validation
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.User{}, platformInvitationUserEmailIndexKey, func(rawObj client.Object) []string {
		user := rawObj.(*iamv1alpha1.User)
		return []string{strings.ToLower(user.Spec.Email)}
	}); err != nil {
		return fmt.Errorf("failed to index user email key: %w", err)
	}

	// Index platforminvitations by lower-cased email to prevent duplicates
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.PlatformInvitation{}, platformInvitationEmailIndexKey, func(rawObj client.Object) []string {
		pi := rawObj.(*iamv1alpha1.PlatformInvitation)
		return []string{strings.ToLower(pi.Spec.Email)}
	}); err != nil {
		return fmt.Errorf("failed to index platforminvitation email key: %w", err)
	}

	return ctrl.NewWebhookManagedBy(mgr, &iamv1alpha1.PlatformInvitation{}).
		WithCustomDefaulter(&PlatformInvitationMutator{client: mgr.GetClient()}).
		WithCustomValidator(&PlatformInvitationValidator{client: mgr.GetClient()}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-iam-miloapis-com-v1alpha1-platforminvitation,mutating=true,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=platforminvitations,verbs=create,versions=v1alpha1,name=mplatforminvitation.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// PlatformInvitationMutator sets default values for PlatformInvitation resources.
type PlatformInvitationMutator struct {
	client client.Client
}

// Default sets the InvitedBy field to the requesting user.
func (m *PlatformInvitationMutator) Default(ctx context.Context, obj runtime.Object) error {
	pi, ok := obj.(*iamv1alpha1.PlatformInvitation)
	if !ok {
		return fmt.Errorf("failed to cast object to PlatformInvitation")
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		platforminvitationlog.Error(err, "failed to get admission request from context", "name", pi.GetName())
		return fmt.Errorf("failed to get request from context: %w", err)
	}

	inviterUser := &iamv1alpha1.User{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: string(req.UserInfo.UID)}, inviterUser); err != nil {
		platforminvitationlog.Error(err, "failed to get user '%s' from iam.miloapis.com API", string(req.UserInfo.UID))
		return errors.NewInternalError(fmt.Errorf("failed to get user '%s' from iam.miloapis.com API: %w", string(req.UserInfo.UID), err))
	}

	pi.Spec.InvitedBy = iamv1alpha1.UserReference{
		Name: inviterUser.Name,
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-iam-miloapis-com-v1alpha1-platforminvitation,mutating=false,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=platforminvitations,verbs=create,versions=v1alpha1,name=vplatforminvitation.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// PlatformInvitationValidator validates PlatformInvitation resources.
type PlatformInvitationValidator struct {
	client client.Client
}

func (v *PlatformInvitationValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	pi, ok := obj.(*iamv1alpha1.PlatformInvitation)
	if !ok {
		return nil, fmt.Errorf("failed to cast object to PlatformInvitation")
	}
	platforminvitationlog.Info("Validating PlatformInvitation", "name", pi.Name)

	var errs field.ErrorList

	// Validate email address
	emailAddress := pi.Spec.Email
	if _, err := mail.ParseAddress(emailAddress); err != nil {
		platforminvitationlog.Info("invalid email address", "email", emailAddress)
		errs = append(errs, field.Invalid(field.NewPath("spec").Child("email"), emailAddress, fmt.Sprintf("invalid email address: %v", err)))
	}

	// Validate scheduleAt is not in the past
	if pi.Spec.ScheduleAt != nil {
		now := metav1.NewTime(time.Now().UTC())
		if pi.Spec.ScheduleAt.Before(&now) {
			errs = append(errs, field.Invalid(field.NewPath("spec").Child("scheduleAt"), pi.Spec.ScheduleAt.String(), "scheduleAt must be in the future"))
		}
	}

	// Ensure the email is not already associated with an existing user
	users := &iamv1alpha1.UserList{}
	if err := v.client.List(ctx, users, client.MatchingFields{platformInvitationUserEmailIndexKey: strings.ToLower(emailAddress)}); err != nil {
		platforminvitationlog.Error(err, "failed to list users by email", "email", emailAddress)
		return nil, errors.NewInternalError(fmt.Errorf("failed to list users: %w", err))
	}
	if len(users.Items) > 0 {
		errs = append(errs, field.Invalid(field.NewPath("spec").Child("email"), emailAddress, "a user with this email already exists"))
	}

	// Ensure there is no existing PlatformInvitation for the same email
	existingInvitations := &iamv1alpha1.PlatformInvitationList{}
	if err := v.client.List(ctx, existingInvitations, client.MatchingFields{platformInvitationEmailIndexKey: strings.ToLower(emailAddress)}); err != nil {
		platforminvitationlog.Error(err, "failed to list platforminvitations by email", "email", emailAddress)
		return nil, errors.NewInternalError(fmt.Errorf("failed to list platforminvitations: %w", err))
	}
	if len(existingInvitations.Items) > 0 {
		errs = append(errs, field.Invalid(field.NewPath("spec").Child("email"), emailAddress, "a platforminvitation with this email already exists"))
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("PlatformInvitation").GroupKind(), pi.Name, errs)
	}

	return nil, nil
}

func (v *PlatformInvitationValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v *PlatformInvitationValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
