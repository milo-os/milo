package v1alpha1

import (
	"context"
	"fmt"

	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var contactGroupLog = logf.Log.WithName("contactgroup-resource")

const contactGroupSpecKey = "contactGroupSpecKey"

// buildDisplayNameKey returns the composite key used for indexing contact group display names.
func buildContactGroupSpecKey(contactGroup notificationv1alpha1.ContactGroup) string {
	return fmt.Sprintf("%s|%s|%s", contactGroup.Spec.DisplayName, contactGroup.Spec.Visibility, contactGroup.Namespace)
}

// SetupContactGroupWebhooksWithManager sets up the webhooks for the ContactGroup resource.
func SetupContactGroupWebhooksWithManager(mgr ctrl.Manager) error {
	contactGroupLog.Info("Setting up notification.miloapis.com contactgroup webhooks")

	// Composite index for contact group spec (display name + visibility + namespace)
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &notificationv1alpha1.ContactGroup{}, contactGroupSpecKey, func(rawObj client.Object) []string {
		cg := rawObj.(*notificationv1alpha1.ContactGroup)
		return []string{buildContactGroupSpecKey(*cg)}
	}); err != nil {
		return fmt.Errorf("failed to set contactgroup field index: %w", err)
	}

	return ctrl.NewWebhookManagedBy(mgr, &notificationv1alpha1.ContactGroup{}).
		WithValidator(&ContactGroupValidator{
			Client: mgr.GetClient(),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-notification-miloapis-com-v1alpha1-contactgroup,mutating=false,failurePolicy=fail,sideEffects=None,groups=notification.miloapis.com,resources=contactgroups,verbs=create,versions=v1alpha1,name=vcontactgroup.notification.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

type ContactGroupValidator struct {
	Client client.Client
}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (v *ContactGroupValidator) ValidateCreate(ctx context.Context, cg *notificationv1alpha1.ContactGroup) (admission.Warnings, error) {
	contactGroupLog.Info("Validating ContactGroup", "name", cg.Name)

	var errs field.ErrorList

	// Ensure no other ContactGroup exists in the same display name and visibility in the same namespace.
	var existing notificationv1alpha1.ContactGroupList
	if err := v.Client.List(ctx, &existing,
		client.MatchingFields{contactGroupSpecKey: buildContactGroupSpecKey(*cg)}); err != nil {
		return nil, errors.NewInternalError(fmt.Errorf("failed to list contactgroups: %w", err))
	}
	if len(existing.Items) > 0 {
		errs = append(errs, field.Invalid(field.NewPath("spec"), cg.Spec, fmt.Sprintf("a ContactGroup named %s already has this display name and visibility in the same namespace", existing.Items[0].Name)))
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(notificationv1alpha1.SchemeGroupVersion.WithKind("ContactGroup").GroupKind(), cg.Name, errs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (v *ContactGroupValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *notificationv1alpha1.ContactGroup) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (v *ContactGroupValidator) ValidateDelete(ctx context.Context, obj *notificationv1alpha1.ContactGroup) (admission.Warnings, error) {
	return nil, nil
}
