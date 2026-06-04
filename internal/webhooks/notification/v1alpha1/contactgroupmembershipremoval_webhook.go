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

const cgmrByContactGroupTupleKey = "contactMembershipRemovalByContactGroupTupleKey"

var cgrLog = logf.Log.WithName("contactgroupmembershipremoval-resource")

// SetupContactGroupMembershipRemovalWebhooksWithManager registers webhooks for ContactGroupMembershipRemoval.
func SetupContactGroupMembershipRemovalWebhooksWithManager(mgr ctrl.Manager) error {
	cgrLog.Info("Setting up notification.miloapis.com contactgroupmembershipremoval webhooks")

	// Field index on contact name for quick lookups
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &notificationv1alpha1.ContactGroupMembershipRemoval{}, cgmrByContactGroupTupleKey, func(raw client.Object) []string {
		obj := raw.(*notificationv1alpha1.ContactGroupMembershipRemoval)
		return []string{buildContactGroupTupleKey(obj.Spec.ContactRef, obj.Spec.ContactGroupRef)}
	}); err != nil {
		return fmt.Errorf("failed to index contactgroupmembershipremoval by contact name: %w", err)
	}

	return ctrl.NewWebhookManagedBy(mgr, &notificationv1alpha1.ContactGroupMembershipRemoval{}).
		WithValidator(&ContactGroupMembershipRemovalValidator{Client: mgr.GetClient()}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-notification-miloapis-com-v1alpha1-contactgroupmembershipremoval,mutating=false,failurePolicy=fail,sideEffects=None,groups=notification.miloapis.com,resources=contactgroupmembershipremovals,verbs=create;update,versions=v1alpha1,name=vcontactgroupmembershipremoval.notification.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

type ContactGroupMembershipRemovalValidator struct {
	Client client.Client
}

func (v *ContactGroupMembershipRemovalValidator) ValidateCreate(ctx context.Context, removal *notificationv1alpha1.ContactGroupMembershipRemoval) (admission.Warnings, error) {
	var errs field.ErrorList

	// Ensure Contact exists
	contact := &notificationv1alpha1.Contact{}
	if err := v.Client.Get(ctx, client.ObjectKey{Namespace: removal.Spec.ContactRef.Namespace, Name: removal.Spec.ContactRef.Name}, contact); err != nil {
		if errors.IsNotFound(err) {
			errs = append(errs, field.NotFound(field.NewPath("spec", "contactRef", "name"), removal.Spec.ContactRef.Name))
		} else {
			return nil, errors.NewInternalError(fmt.Errorf("failed to get Contact: %w", err))
		}
	} else {
		// Validate contact ownership when in user context
		if err := ValidateContactOwnership(ctx, contact, notificationv1alpha1.SchemeGroupVersion.WithResource("contactgroupmembershipremovals").GroupResource(), removal.Name, "create membership removal"); err != nil {
			return nil, err
		}
	}
	// Ensure ContactGroup exists
	contactGroup := &notificationv1alpha1.ContactGroup{}
	if err := v.Client.Get(ctx, client.ObjectKey{Namespace: removal.Spec.ContactGroupRef.Namespace, Name: removal.Spec.ContactGroupRef.Name}, contactGroup); err != nil {
		if errors.IsNotFound(err) {
			errs = append(errs, field.NotFound(field.NewPath("spec", "contactGroupRef", "name"), removal.Spec.ContactGroupRef.Name))
		} else {
			return nil, errors.NewInternalError(fmt.Errorf("failed to get ContactGroup: %w", err))
		}
	}

	// Prevent duplicate removals
	var existing notificationv1alpha1.ContactGroupMembershipRemovalList
	if err := v.Client.List(ctx, &existing, client.MatchingFields{cgmrByContactGroupTupleKey: buildContactGroupTupleKey(removal.Spec.ContactRef, removal.Spec.ContactGroupRef)}); err != nil {
		return nil, errors.NewInternalError(fmt.Errorf("failed to list removals: %w", err))
	}
	if len(existing.Items) > 0 {
		errs = append(errs, field.Duplicate(field.NewPath("spec"), fmt.Sprintf("membership removal already exists in ContactGroupMembershipRemoval %s", existing.Items[0].Name)))
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(notificationv1alpha1.SchemeGroupVersion.WithKind("ContactGroupMembershipRemoval").GroupKind(), removal.Name, errs)
	}
	return nil, nil
}

func (v *ContactGroupMembershipRemovalValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *notificationv1alpha1.ContactGroupMembershipRemoval) (admission.Warnings, error) {
	return nil, errors.NewBadRequest("ContactGroupMembershipRemoval is immutable; delete and recreate to modify")
}

func (v *ContactGroupMembershipRemovalValidator) ValidateDelete(ctx context.Context, removal *notificationv1alpha1.ContactGroupMembershipRemoval) (admission.Warnings, error) {
	// Validate contact ownership when in user context
	// Retrieve the referenced Contact to check its ownership
	contact := &notificationv1alpha1.Contact{}
	if err := v.Client.Get(ctx, client.ObjectKey{Namespace: removal.Spec.ContactRef.Namespace, Name: removal.Spec.ContactRef.Name}, contact); err != nil {
		if errors.IsNotFound(err) {
			return nil, errors.NewInternalError(fmt.Errorf("failed to get Contact for validation: %w", err))
		}
		return nil, errors.NewInternalError(fmt.Errorf("failed to get Contact: %w", err))
	}

	if err := ValidateContactOwnership(ctx, contact, notificationv1alpha1.SchemeGroupVersion.WithResource("contactgroupmembershipremovals").GroupResource(), removal.Name, "delete membership removal"); err != nil {
		return nil, err
	}
	return nil, nil
}
