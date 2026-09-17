package v1alpha1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

// groupmembershiplog is for logging in this package.
var groupmembershiplog = logf.Log.WithName("groupmembership-resource")

const groupMembershipCompositeKey = "iam.miloapis.com/groupmembership-composite"

func buildGroupMembershipCompositeKey(userRef iamv1alpha1.UserReference, groupRef iamv1alpha1.GroupReference) string {
	return fmt.Sprintf("%s|%s|%s", userRef.Name, groupRef.Namespace, groupRef.Name)
}

// +kubebuilder:webhook:path=/validate-iam-miloapis-com-v1alpha1-groupmembership,mutating=false,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=groupmemberships,verbs=create;update,versions=v1alpha1,name=vgroupmembership.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=groupmemberships,verbs=list
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=groups,verbs=get

// SetupGroupMembershipWebhooksWithManager sets up the groupmembership webhook.
func SetupGroupMembershipWebhooksWithManager(mgr ctrl.Manager) error {
	groupmembershiplog.Info("Setting up iam.miloapis.com groupmembership webhooks")

	// Composite index for exact membership tuple (user name + group ns + group name)
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.GroupMembership{}, groupMembershipCompositeKey, func(rawObj client.Object) []string {
		gm := rawObj.(*iamv1alpha1.GroupMembership)
		return []string{buildGroupMembershipCompositeKey(gm.Spec.UserRef, gm.Spec.GroupRef)}
	}); err != nil {
		return fmt.Errorf("failed to index groupmembership composite key: %w", err)
	}

	return ctrl.NewWebhookManagedBy(mgr, &iamv1alpha1.GroupMembership{}).
		WithDefaulter(&GroupMembershipMutator{
			client: mgr.GetClient(),
		}).
		WithValidator(&GroupMembershipValidator{
			client: mgr.GetClient(),
		}).
		Complete()
}

// GroupMembershipValidator validates GroupMemberships.
//
// Invariants enforced on create:
//   - The referenced User must exist.
//   - The referenced Group must exist.
//   - A user may be a member of a given group at most once within a namespace.
//
// The spec is immutable: a GroupMembership is a pure (user, group) link, so
// changing it is rejected and the membership must be deleted and recreated to
// change which user belongs to which group. Metadata-only updates are allowed.
type GroupMembershipValidator struct {
	client client.Client
}

func (v *GroupMembershipValidator) ValidateCreate(ctx context.Context, membership *iamv1alpha1.GroupMembership) (admission.Warnings, error) {
	groupmembershiplog.Info("Validating GroupMembership create", "name", membership.Name, "namespace", membership.Namespace)

	var errs field.ErrorList

	// Validate referenced User exists
	user := &iamv1alpha1.User{}
	if err := v.client.Get(ctx, client.ObjectKey{Name: membership.Spec.UserRef.Name}, user); err != nil {
		if errors.IsNotFound(err) {
			errs = append(errs, field.NotFound(field.NewPath("spec", "userRef", "name"), membership.Spec.UserRef.Name))
		} else {
			return nil, errors.NewInternalError(fmt.Errorf("failed to get User %q: %w", membership.Spec.UserRef.Name, err))
		}
	}

	// Validate referenced Group exists
	group := &iamv1alpha1.Group{}
	if err := v.client.Get(ctx, client.ObjectKey{
		Namespace: membership.Spec.GroupRef.Namespace,
		Name:      membership.Spec.GroupRef.Name,
	}, group); err != nil {
		if errors.IsNotFound(err) {
			errs = append(errs, field.NotFound(field.NewPath("spec", "groupRef", "name"), membership.Spec.GroupRef.Name))
		} else {
			return nil, errors.NewInternalError(fmt.Errorf("failed to get Group %q in namespace %q: %w", membership.Spec.GroupRef.Name, membership.Spec.GroupRef.Namespace, err))
		}
	}

	// Check for duplicate membership in the same namespace
	key := buildGroupMembershipCompositeKey(membership.Spec.UserRef, membership.Spec.GroupRef)
	var existing iamv1alpha1.GroupMembershipList
	if err := v.client.List(ctx, &existing,
		client.InNamespace(membership.Namespace),
		client.MatchingFields{groupMembershipCompositeKey: key}); err != nil {
		return nil, errors.NewInternalError(fmt.Errorf("failed to list group memberships: %w", err))
	}
	if len(existing.Items) > 0 {
		dup := field.Duplicate(field.NewPath("spec"), key)
		dup.Detail = fmt.Sprintf("user %q is already a member of group %q in namespace %q",
			membership.Spec.UserRef.Name,
			membership.Spec.GroupRef.Name,
			membership.Spec.GroupRef.Namespace,
		)
		errs = append(errs, dup)
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("GroupMembership").GroupKind(), membership.Name, errs)
	}

	return nil, nil
}

func (v *GroupMembershipValidator) ValidateUpdate(ctx context.Context, oldMembership, newMembership *iamv1alpha1.GroupMembership) (admission.Warnings, error) {
	groupmembershiplog.Info("Validating GroupMembership update", "name", newMembership.Name, "namespace", newMembership.Namespace)

	// Only the spec is immutable. Metadata-only updates (finalizers, labels,
	// owner references) must pass: the OpenFGA controller adds and removes its
	// finalizer through a regular update, and rejecting that leaves memberships
	// without their authorization tuple and undeletable.
	if equality.Semantic.DeepEqual(oldMembership.Spec, newMembership.Spec) {
		return nil, nil
	}

	var errs field.ErrorList
	errs = append(errs, field.Forbidden(
		field.NewPath("spec"),
		fmt.Sprintf("cannot update group membership %q: group memberships are immutable. Delete and recreate the membership to change which user belongs to which group", newMembership.Name),
	))

	return nil, errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("GroupMembership").GroupKind(), newMembership.Name, errs)
}

func (v *GroupMembershipValidator) ValidateDelete(ctx context.Context, obj *iamv1alpha1.GroupMembership) (admission.Warnings, error) {
	return nil, nil
}

// +kubebuilder:webhook:path=/mutate-iam-miloapis-com-v1alpha1-groupmembership,mutating=true,failurePolicy=ignore,sideEffects=None,groups=iam.miloapis.com,resources=groupmemberships,verbs=create;update,versions=v1alpha1,name=mgroupmembership.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get;list;watch

// GroupMembershipMutator decorates a GroupMembership with the email of the
// User it references. It never rejects the object: a missing User leaves the
// membership untouched, and the webhook is registered with failurePolicy
// Ignore so an outage cannot block adding a member to a group.
type GroupMembershipMutator struct {
	client client.Client
}

func (m *GroupMembershipMutator) Default(ctx context.Context, gm *iamv1alpha1.GroupMembership) error {
	log := logf.FromContext(ctx).WithValues("groupmembership", gm.GetName(), "namespace", gm.GetNamespace())

	userName := gm.Spec.UserRef.Name
	if userName == "" {
		return nil
	}

	user := &iamv1alpha1.User{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: userName}, user); err != nil {
		if errors.IsNotFound(err) {
			log.Info("referenced user not found; leaving group membership without email annotation", "user", userName)
			return nil
		}
		return errors.NewInternalError(fmt.Errorf("failed to get User %q: %w", userName, err))
	}

	if user.Spec.Email == "" {
		return nil
	}

	if gm.Annotations == nil {
		gm.Annotations = map[string]string{}
	}
	gm.Annotations[iamv1alpha1.UserEmailAnnotation] = user.Spec.Email
	log.Info("stamped member email on group membership", "user", userName)

	return nil
}
