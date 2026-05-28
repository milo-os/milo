package v1alpha1

import (
	"context"
	"fmt"

	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var cgmLog = logf.Log.WithName("contactgroupmembership-resource")

var contactMembershipCompositeKey = "contactMembershipKey"
var contactMembershipRemovalCompositeKey = "contactGroupMembershipRemovalKey"

// buildContactGroupTupleKey returns "<contact-ns>|<contact-name>|<group-ns>|<group-name>"
func buildContactGroupTupleKey(contactRef notificationv1alpha1.ContactReference, groupRef notificationv1alpha1.ContactGroupReference,
) string {
	return fmt.Sprintf("%s|%s|%s|%s", contactRef.Namespace, contactRef.Name, groupRef.Namespace, groupRef.Name)
}

// SetupContactGroupMembershipWebhooksWithManager sets up the webhooks for the ContactGroupMembership resource.
func SetupContactGroupMembershipWebhooksWithManager(mgr ctrl.Manager) error {
	cgmLog.Info("Setting up notification.miloapis.com contactgroupmembership webhooks")

	// Composite index for exact membership tuple (contact ns/name + group ns/name)
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &notificationv1alpha1.ContactGroupMembership{}, contactMembershipCompositeKey, func(rawObj client.Object) []string {
		cgm := rawObj.(*notificationv1alpha1.ContactGroupMembership)
		return []string{buildContactGroupTupleKey(cgm.Spec.ContactRef, cgm.Spec.ContactGroupRef)}
	}); err != nil {
		return fmt.Errorf("failed to index contactgroupmembership composite key: %w", err)
	}

	// Composite index for membership removal tuple (contact ns/name + group ns/name)
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &notificationv1alpha1.ContactGroupMembershipRemoval{}, contactMembershipRemovalCompositeKey, func(rawObj client.Object) []string {
		rm := rawObj.(*notificationv1alpha1.ContactGroupMembershipRemoval)
		return []string{buildContactGroupTupleKey(rm.Spec.ContactRef, rm.Spec.ContactGroupRef)}
	}); err != nil {
		return fmt.Errorf("failed to index contactgroupmembershipremoval composite key: %w", err)
	}

	return ctrl.NewWebhookManagedBy(mgr, &notificationv1alpha1.ContactGroupMembership{}).
		WithCustomValidator(&ContactGroupMembershipValidator{Client: mgr.GetClient()}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-notification-miloapis-com-v1alpha1-contactgroupmembership,mutating=false,failurePolicy=fail,sideEffects=None,groups=notification.miloapis.com,resources=contactgroupmemberships,verbs=create;update,versions=v1alpha1,name=vcontactgroupmembership.notification.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

type ContactGroupMembershipValidator struct {
	Client client.Client
}

// ValidateCreate implements webhook.Validator for ContactGroupMembership
func (v *ContactGroupMembershipValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	cgm, ok := obj.(*notificationv1alpha1.ContactGroupMembership)
	if !ok {
		return nil, errors.NewInternalError(fmt.Errorf("failed to cast object to ContactGroupMembership"))
	}

	cgmLog.Info("Validating ContactGroupMembership", "name", cgm.Name)
	var errs field.ErrorList

	// Validate referenced Contact exists
	contact := &notificationv1alpha1.Contact{}
	if err := v.Client.Get(ctx, client.ObjectKey{Namespace: cgm.Spec.ContactRef.Namespace, Name: cgm.Spec.ContactRef.Name}, contact); err != nil {
		if errors.IsNotFound(err) {
			errs = append(errs, field.NotFound(field.NewPath("spec", "contactRef", "name"), cgm.Spec.ContactRef.Name))
		} else {
			return nil, errors.NewInternalError(fmt.Errorf("failed to get Contact: %w", err))
		}
	} else {
		// Validate contact ownership when in user context
		if err := ValidateContactOwnership(ctx, contact, notificationv1alpha1.SchemeGroupVersion.WithResource("contactgroupmemberships").GroupResource(), cgm.Name, "create membership"); err != nil {
			return nil, err
		}
	}

	// Validate referenced ContactGroup exists
	group := &notificationv1alpha1.ContactGroup{}
	if err := v.Client.Get(ctx, client.ObjectKey{Namespace: cgm.Spec.ContactGroupRef.Namespace, Name: cgm.Spec.ContactGroupRef.Name}, group); err != nil {
		if errors.IsNotFound(err) {
			errs = append(errs, field.NotFound(field.NewPath("spec", "contactGroupRef", "name"), cgm.Spec.ContactGroupRef.Name))
		} else {
			return nil, errors.NewInternalError(fmt.Errorf("failed to get ContactGroup: %w", err))
		}
	}

	// Check for duplicate membership (same contact already in the target group)
	var existing notificationv1alpha1.ContactGroupMembershipList
	if err := v.Client.List(ctx, &existing,
		client.MatchingFields{contactMembershipCompositeKey: buildContactGroupTupleKey(cgm.Spec.ContactRef, cgm.Spec.ContactGroupRef)}); err != nil {
		return nil, errors.NewInternalError(fmt.Errorf("failed to list memberships: %w", err))
	}
	if len(existing.Items) > 0 {
		errs = append(errs, field.Duplicate(field.NewPath("spec"), fmt.Sprintf("membership already exists in ContactGroupMembership %s", existing.Items[0].Name)))
	}

	// Check for existing membership removal for the same contact and group
	var existingRemovals notificationv1alpha1.ContactGroupMembershipRemovalList
	if err := v.Client.List(ctx, &existingRemovals,
		client.MatchingFields{contactMembershipRemovalCompositeKey: buildContactGroupTupleKey(cgm.Spec.ContactRef, cgm.Spec.ContactGroupRef)}); err != nil {
		return nil, errors.NewInternalError(fmt.Errorf("failed to list membership removals: %w", err))
	}
	if len(existingRemovals.Items) > 0 {
		errs = append(errs, field.Invalid(field.NewPath("spec"), cgm.Spec, fmt.Sprintf("cannot create membership as a ContactGroupMembershipRemoval %s already exists", existingRemovals.Items[0].Name)))
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(notificationv1alpha1.SchemeGroupVersion.WithKind("ContactGroupMembership").GroupKind(), cgm.Name, errs)
	}
	return nil, nil
}

func (v *ContactGroupMembershipValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v *ContactGroupMembershipValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
