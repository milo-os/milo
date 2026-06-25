package iam

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

// TODO joseszycho remove controller once the migratation to PlatformAccess is complete

// PlatformAccessMigrationReconciler reconciles a User object to sync its state to PlatformAccess.
type PlatformAccessMigrationReconciler struct {
	Client client.Client
}

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=platformaccesses,verbs=get;list;watch;create;update;patch

// Reconcile reads the User state and ensures a corresponding PlatformAccess exists and is in sync.
func (r *PlatformAccessMigrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithName("platformaccess-migration-controller")

	// 1. Fetch User resource
	user := &iamv1alpha1.User{}
	if err := r.Client.Get(ctx, req.NamespacedName, user); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get User: %w", err)
	}

	// Do not sync if the user is being deleted
	if !user.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// 2. Map User state to PlatformAccessState
	targetState := iamv1alpha1.PlatformAccessStatePending
	if user.Status.State == iamv1alpha1.UserStateInactive {
		targetState = iamv1alpha1.PlatformAccessStateSuspended
	} else {
		switch user.Status.RegistrationApproval {
		case iamv1alpha1.RegistrationApprovalStateApproved:
			targetState = iamv1alpha1.PlatformAccessStateApproved
		case iamv1alpha1.RegistrationApprovalStateRejected:
			targetState = iamv1alpha1.PlatformAccessStateRejected
		default:
			targetState = iamv1alpha1.PlatformAccessStatePending
		}
	}

	// 3. Fetch or Create PlatformAccess (named after the user)
	pa := &iamv1alpha1.PlatformAccess{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: user.Name}, pa)
	if apierrors.IsNotFound(err) {
		log.Info("creating PlatformAccess for user", "user", user.Name, "state", targetState)
		pa = &iamv1alpha1.PlatformAccess{
			ObjectMeta: metav1.ObjectMeta{
				Name: user.Name,
			},
			Spec: iamv1alpha1.PlatformAccessSpec{
				UserRef: iamv1alpha1.UserReference{
					Name: user.Name,
				},
				State: targetState,
			},
		}
		if err := r.Client.Create(ctx, pa); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create PlatformAccess: %w", err)
		}
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get PlatformAccess: %w", err)
	}

	// 4. Update state if out of sync
	if pa.Spec.State != targetState {
		log.Info("updating PlatformAccess state for user", "user", user.Name, "oldState", pa.Spec.State, "newState", targetState)
		pa.Spec.State = targetState
		if err := r.Client.Update(ctx, pa); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update PlatformAccess state: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PlatformAccessMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iamv1alpha1.User{}).
		Owns(&iamv1alpha1.PlatformAccess{}).
		Named("platformaccess-migration").
		Complete(r)
}
