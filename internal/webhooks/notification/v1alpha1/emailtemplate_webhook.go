package v1alpha1

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	"go.miloapis.com/milo/pkg/email/templating"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var emailTemplateLog = logf.Log.WithName("emailtemplate-resource")

func SetupEmailTemplateWebhooksWithManager(mgr ctrl.Manager, _ string) error {
	emailTemplateLog.Info("Setting up notification.miloapis.com emailtemplates webhooks")

	return ctrl.NewWebhookManagedBy(mgr, &notificationv1alpha1.EmailTemplate{}).
		WithValidator(&EmailTemplateValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-notification-miloapis-com-v1alpha1-emailtemplate,mutating=false,failurePolicy=fail,sideEffects=None,groups=notification.miloapis.com,resources=emailtemplates,verbs=create;update,versions=v1alpha1,name=vemailtemplate.notification.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

type EmailTemplateValidator struct{}

func (v *EmailTemplateValidator) ValidateCreate(ctx context.Context, emailTemplate *notificationv1alpha1.EmailTemplate) (admission.Warnings, error) {
	emailTemplateLog.Info("Validating EmailTemplate", "name", emailTemplate.Name)

	errs := field.ErrorList{}

	htmlBody := emailTemplate.Spec.HTMLBody
	textBody := emailTemplate.Spec.TextBody
	variables := emailTemplate.Spec.Variables

	errs = append(errs, templating.ValidateHTMLTemplate(htmlBody, variables)...)
	errs = append(errs, templating.ValidateTextTemplate(textBody, variables)...)

	if len(errs) > 0 {
		validationErr := errors.NewInvalid(notificationv1alpha1.SchemeGroupVersion.WithKind("EmailTemplate").GroupKind(), emailTemplate.Name, errs)
		emailTemplateLog.Error(validationErr, "Invalid EmailTemplate")
		return nil, validationErr
	}

	return nil, nil
}

func (v *EmailTemplateValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *notificationv1alpha1.EmailTemplate) (admission.Warnings, error) {
	// For updates we simply re-run the same validation against the new object.
	return v.ValidateCreate(ctx, newObj)
}

func (v *EmailTemplateValidator) ValidateDelete(ctx context.Context, obj *notificationv1alpha1.EmailTemplate) (admission.Warnings, error) {
	// No special validation on delete.
	return nil, nil
}
