package v1alpha1

import (
	"context"
	"fmt"
	"maps"
	"net/mail"
	"time"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	"go.miloapis.com/milo/pkg/email/templating"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var emailLog = logf.Log.WithName("email-resource")

// SetupEmailWebhooksWithManager sets up the webhooks for the Email resource.
func SetupEmailWebhooksWithManager(mgr ctrl.Manager) error {
	emailLog.Info("Setting up notification.miloapis.com email webhooks")

	return ctrl.NewWebhookManagedBy(mgr, &notificationv1alpha1.Email{}).
		WithDefaulter(&EmailMutator{Now: time.Now}).
		WithValidator(&EmailValidator{
			Client: mgr.GetClient(),
		}).
		Complete()
}

// createdDate formats t as a CreatedDateLabel value.
func createdDate(t time.Time) string {
	return t.UTC().Format(notificationv1alpha1.CreatedDateLayout)
}

// +kubebuilder:webhook:path=/mutate-notification-miloapis-com-v1alpha1-email,mutating=true,failurePolicy=fail,sideEffects=None,groups=notification.miloapis.com,resources=emails,verbs=create,versions=v1alpha1,name=memail.notification.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// EmailMutator stamps the created-date label on new Email resources.
type EmailMutator struct {
	// Now returns the current time. Tests replace it with a fixed clock.
	Now func() time.Time
}

// Default sets CreatedDateLabel to today's UTC date, replacing any value the
// client sent. The API server sets creationTimestamp after mutating admission,
// so the webhook's clock stands in for it.
func (m *EmailMutator) Default(ctx context.Context, email *notificationv1alpha1.Email) error {
	if email.Labels == nil {
		email.Labels = map[string]string{}
	}
	email.Labels[notificationv1alpha1.CreatedDateLabel] = createdDate(m.Now())
	return nil
}

// +kubebuilder:webhook:path=/validate-notification-miloapis-com-v1alpha1-email,mutating=false,failurePolicy=fail,sideEffects=None,groups=notification.miloapis.com,resources=emails,verbs=create;update;delete,versions=v1alpha1,name=vemail.notification.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// EmailValidator validates Email resources.
type EmailValidator struct {
	Client client.Client
}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (v *EmailValidator) ValidateCreate(ctx context.Context, email *notificationv1alpha1.Email) (admission.Warnings, error) {
	emailLog.Info("validate create", "name", email.Name)

	var errs field.ErrorList

	hasEmailAddress := email.Spec.Recipient.EmailAddress != ""
	hasUserRef := email.Spec.Recipient.UserRef.Name != ""

	// Validate that exactly one of emailAddress or userRef is provided
	if hasEmailAddress == hasUserRef {
		emailLog.Info("exactly one of emailAddress or userRef must be provided", "email", email.Name)
		errs = append(errs, field.Invalid(field.NewPath("spec"), "", "exactly one of emailAddress or userRef must be provided"))
	}

	// Validate User reference
	if hasUserRef {
		user := &iamv1alpha1.User{}
		err := v.Client.Get(ctx, client.ObjectKey{Name: email.Spec.Recipient.UserRef.Name}, user)
		if err != nil {
			if errors.IsNotFound(err) {
				emailLog.Info("user not found", "name", email.Spec.Recipient.UserRef.Name)
				errs = append(errs, field.NotFound(field.NewPath("spec", "userRef", "name"), email.Spec.Recipient.UserRef.Name))
			} else {
				emailLog.Error(err, "error getting user", "name", email.Spec.Recipient.UserRef.Name)
				errs = append(errs, field.InternalError(field.NewPath("spec", "userRef", "name"), fmt.Errorf("getting user: %w", err)))
			}
		}
	}

	// Validate EmailAddress format
	if hasEmailAddress {
		if _, err := mail.ParseAddress(email.Spec.Recipient.EmailAddress); err != nil {
			emailLog.Info("invalid email address", "email", email.Name, "emailAddress", email.Spec.Recipient.EmailAddress)
			errs = append(errs, field.Invalid(field.NewPath("spec", "emailAddress"), email.Spec.Recipient.EmailAddress, fmt.Sprintf("invalid email address: %v", err)))
		}
	}

	// Validate EmailTemplate reference
	template := &notificationv1alpha1.EmailTemplate{}
	err := v.Client.Get(ctx, client.ObjectKey{Name: email.Spec.TemplateRef.Name}, template)
	if err != nil {
		if errors.IsNotFound(err) {
			emailLog.Info("email template not found", "name", email.Spec.TemplateRef.Name)
			errs = append(errs, field.NotFound(field.NewPath("spec", "templateRef", "name"), email.Spec.TemplateRef.Name))
		} else {
			emailLog.Error(err, "error getting email template", "name", email.Spec.TemplateRef.Name)
			errs = append(errs, field.InternalError(field.NewPath("spec", "templateRef", "name"), fmt.Errorf("getting email template: %w", err)))
		}
	}

	// Validate Email variables against declared template variables
	if err == nil { // template successfully fetched
		varValidationErrs := templating.ValidateEmailVariables(email, template)
		errs = append(errs, varValidationErrs...)
	}

	if len(errs) > 0 {
		invalidErr := errors.NewInvalid(notificationv1alpha1.SchemeGroupVersion.WithKind("Email").GroupKind(), email.Name, errs)
		emailLog.Error(invalidErr, "invalid email")
		return nil, invalidErr
	}

	return nil, nil

}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
// We do not allow updates to Email resources as they are immutable. The one
// exception is adding CreatedDateLabel to an Email created before the label
// existed, which the backfill needs.
func (v *EmailValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *notificationv1alpha1.Email) (admission.Warnings, error) {
	if isCreatedDateBackfill(oldObj, newObj) {
		return nil, nil
	}
	return nil, errors.NewMethodNotSupported(notificationv1alpha1.SchemeGroupVersion.WithResource("emails").GroupResource(), "update")
}

// isCreatedDateBackfill reports whether newObj differs from oldObj only by
// adding CreatedDateLabel, set to the date of oldObj's creationTimestamp.
// Changing an existing value, or any other change alongside it, is not a
// backfill.
func isCreatedDateBackfill(oldObj, newObj *notificationv1alpha1.Email) bool {
	if _, labelled := oldObj.Labels[notificationv1alpha1.CreatedDateLabel]; labelled {
		return false
	}
	if newObj.Labels[notificationv1alpha1.CreatedDateLabel] != createdDate(oldObj.CreationTimestamp.Time) {
		return false
	}

	newLabels := maps.Clone(newObj.Labels)
	delete(newLabels, notificationv1alpha1.CreatedDateLabel)

	return maps.Equal(oldObj.Labels, newLabels) &&
		equality.Semantic.DeepEqual(oldObj.Annotations, newObj.Annotations) &&
		equality.Semantic.DeepEqual(oldObj.Finalizers, newObj.Finalizers) &&
		equality.Semantic.DeepEqual(oldObj.OwnerReferences, newObj.OwnerReferences) &&
		equality.Semantic.DeepEqual(oldObj.Spec, newObj.Spec)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (v *EmailValidator) ValidateDelete(ctx context.Context, obj *notificationv1alpha1.Email) (admission.Warnings, error) {
	return nil, nil
}
