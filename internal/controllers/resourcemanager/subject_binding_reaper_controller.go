package resourcemanager

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// SubjectBindingReaperController deletes PolicyBindings whose sole subject
// was a User that no longer exists. It exists to clean up the PolicyBinding
// that ProjectValidator's mutating webhook creates for a project's creator
// (see internal/webhooks/resourcemanager/v1alpha1/project_webhook.go): that
// binding is owned by the Project, not the User, so nothing previously
// reacted when the creating User was later deleted while the Project lived
// on, leaving the binding permanently stuck reporting SubjectValidationFailed.
type SubjectBindingReaperController struct {
	Client client.Client
}

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=policybindings,verbs=get;list;watch;delete

func (r *SubjectBindingReaperController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var user iamv1alpha1.User
	if err := r.Client.Get(ctx, client.ObjectKey{Name: req.Name}, &user); err == nil {
		// User still exists — nothing to reap.
		return ctrl.Result{}, nil
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get user: %w", err)
	}

	var bindings iamv1alpha1.PolicyBindingList
	if err := r.Client.List(ctx, &bindings, client.MatchingLabels{
		resourcemanagerv1alpha.SubjectUserNameLabel: req.Name,
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list policy bindings for deleted user: %w", err)
	}

	for i := range bindings.Items {
		binding := &bindings.Items[i]
		logger.Info("deleting policy binding for deleted user subject",
			"policyBinding", binding.Name,
			"namespace", binding.Namespace,
			"user", req.Name)
		if err := r.Client.Delete(ctx, binding); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete orphaned policy binding %s/%s: %w", binding.Namespace, binding.Name, err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SubjectBindingReaperController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iamv1alpha1.User{}).
		Named("subject-binding-reaper").
		Complete(r)
}
