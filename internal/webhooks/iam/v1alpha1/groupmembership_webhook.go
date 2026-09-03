package v1alpha1

import (
	"context"
	"fmt"

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
// Updates are rejected outright because a GroupMembership is a pure (user,
// group) link with no mutable spec. Delete and recreate the membership to
// change which user belongs to which group.
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
