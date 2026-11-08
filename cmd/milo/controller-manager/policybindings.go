package app

import (
	"fmt"

	controllerruntime "sigs.k8s.io/controller-runtime"

	iamcontroller "go.miloapis.com/milo/internal/controllers/iam"
	resourcemanagercontroller "go.miloapis.com/milo/internal/controllers/resourcemanager"
)

// setupPolicyBindingLifecycle sets up the controllers that end a PolicyBinding with what it names: the subject reaper
// deletes a binding whose User is gone, and the adopter makes a binding a dependent of its target, so the garbage
// collector deletes it with that target.
func setupPolicyBindingLifecycle(mgr controllerruntime.Manager) error {
	reaper := resourcemanagercontroller.SubjectBindingReaperController{Client: mgr.GetClient()}
	if err := reaper.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("subject binding reaper: %w", err)
	}
	adopter := iamcontroller.PolicyBindingAdopter{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Mapper:    mgr.GetRESTMapper(),
		Scheme:    mgr.GetScheme(),
	}
	if err := adopter.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("policy binding adopter: %w", err)
	}
	return nil
}
