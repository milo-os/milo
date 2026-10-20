package iam

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

// PolicyBindingAdopter makes a PolicyBinding a dependent of the one object its resourceRef names, so the garbage
// collector deletes the binding with that object.
type PolicyBindingAdopter struct {
	Client client.Client
	// APIReader reads the target from the API server, not the cache, so a re-created target is never mistaken for
	// the one the binding names.
	APIReader client.Reader
	Scheme    *runtime.Scheme
}

// Reconcile adopts nothing yet.
func (r *PolicyBindingAdopter) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyBindingAdopter) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iamv1alpha1.PolicyBinding{}).
		Named("policybinding-adopter").
		Complete(r)
}
