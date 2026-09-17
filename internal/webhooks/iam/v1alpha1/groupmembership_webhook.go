package v1alpha1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

func SetupGroupMembershipWebhooksWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &iamv1alpha1.GroupMembership{}).
		WithDefaulter(&GroupMembershipMutator{
			client: mgr.GetClient(),
		}).
		Complete()
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
