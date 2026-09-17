package iam

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

// groupMembershipUserRefIndexKey indexes GroupMemberships by the User they
// reference so a User change can be mapped back to the memberships to refresh.
const groupMembershipUserRefIndexKey = "spec.userRef.name"

// GroupMembershipController keeps the user-email annotation on a GroupMembership
// in sync with the email of the User referenced by spec.userRef.
//
// The mutating webhook stamps the annotation at admission, which is what puts
// the email on the create audit event, but a defaulter only runs on write. This
// controller owns the annotation from then on: when a User changes their email
// address, every GroupMembership that references that User is patched so the
// annotation never goes stale.
type GroupMembershipController struct {
	Client client.Client
}

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=groupmemberships,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get;list;watch

func (r *GroupMembershipController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var groupMembership iamv1alpha1.GroupMembership
	if err := r.Client.Get(ctx, req.NamespacedName, &groupMembership); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get group membership: %w", err)
	}

	if groupMembership.GetDeletionTimestamp() != nil {
		return ctrl.Result{}, nil
	}

	userName := groupMembership.Spec.UserRef.Name
	if userName == "" {
		return ctrl.Result{}, nil
	}

	// Mirror the webhook's decorate-never-reject behaviour: a missing User or a
	// User without an email leaves the membership untouched rather than failing
	// the reconcile, so a half-populated User cannot wedge the controller.
	var user iamv1alpha1.User
	if err := r.Client.Get(ctx, types.NamespacedName{Name: userName}, &user); err != nil {
		if errors.IsNotFound(err) {
			log.Info("referenced user not found; leaving group membership email annotation untouched",
				"groupMembership", groupMembership.Name, "user", userName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get user %q: %w", userName, err)
	}

	if user.Spec.Email == "" {
		return ctrl.Result{}, nil
	}

	if groupMembership.Annotations[iamv1alpha1.UserEmailAnnotation] == user.Spec.Email {
		return ctrl.Result{}, nil
	}

	// Patch only the annotation. A full update would send the whole object back
	// and lose races against any other writer touching the membership.
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				iamv1alpha1.UserEmailAnnotation: user.Spec.Email,
			},
		},
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to build group membership email annotation patch: %w", err)
	}

	if err := r.Client.Patch(ctx, &groupMembership, client.RawPatch(types.MergePatchType, patch)); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to patch group membership email annotation: %w", err)
	}

	log.Info("refreshed member email on group membership",
		"groupMembership", groupMembership.Name, "user", userName)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GroupMembershipController) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.GroupMembership{}, groupMembershipUserRefIndexKey, func(rawObj client.Object) []string {
		obj := rawObj.(*iamv1alpha1.GroupMembership)
		if obj.Spec.UserRef.Name == "" {
			return nil
		}
		return []string{obj.Spec.UserRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to index group memberships by user reference: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&iamv1alpha1.GroupMembership{}).
		Watches(&iamv1alpha1.User{},
			handler.EnqueueRequestsFromMapFunc(r.findGroupMembershipsForUser)).
		Named("group-membership").
		Complete(r)
}

// findGroupMembershipsForUser finds all GroupMembership resources that reference a given User
func (r *GroupMembershipController) findGroupMembershipsForUser(ctx context.Context, obj client.Object) []reconcile.Request {
	user := obj.(*iamv1alpha1.User)

	var groupMemberships iamv1alpha1.GroupMembershipList
	if err := r.Client.List(ctx, &groupMemberships, client.MatchingFields{
		groupMembershipUserRefIndexKey: user.Name,
	}); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list group memberships for user", "user", user.Name)
		return nil
	}

	var requests []reconcile.Request
	for _, membership := range groupMemberships.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      membership.Name,
				Namespace: membership.Namespace,
			},
		})
	}

	return requests
}
