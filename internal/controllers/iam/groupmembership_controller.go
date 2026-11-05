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

	email, err := r.resolveUserEmail(ctx, userName)
	if err != nil {
		return ctrl.Result{}, err
	}

	current, hasAnnotation := groupMembership.Annotations[iamv1alpha1.UserEmailAnnotation]
	if email == "" && !hasAnnotation {
		return ctrl.Result{}, nil
	}
	if email != "" && current == email {
		return ctrl.Result{}, nil
	}

	// The annotation is client-writable, so an email that cannot be resolved
	// clears it rather than leaving a client-supplied value in the activity feed.
	// A JSON merge patch removes a key whose value is null.
	var annotationValue any
	if email != "" {
		annotationValue = email
	}

	// Patch only the annotation. A full update would send the whole object back
	// and lose races against any other writer touching the membership.
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				iamv1alpha1.UserEmailAnnotation: annotationValue,
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

	log.Info("synced member email annotation on group membership",
		"groupMembership", groupMembership.Name, "user", userName, "cleared", email == "")

	return ctrl.Result{}, nil
}

// resolveUserEmail returns the email of the named User, or "" when the name is
// empty, the User does not exist, or the User has no email. A missing User is
// not an error, so a dangling reference cannot wedge the controller.
func (r *GroupMembershipController) resolveUserEmail(ctx context.Context, userName string) (string, error) {
	if userName == "" {
		return "", nil
	}

	var user iamv1alpha1.User
	if err := r.Client.Get(ctx, types.NamespacedName{Name: userName}, &user); err != nil {
		if errors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to get user %q: %w", userName, err)
	}

	return user.Spec.Email, nil
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
