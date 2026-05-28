package v1alpha1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"
)

var useridentitylog = logf.Log.WithName("useridentity-resource")

// +kubebuilder:webhook:path=/validate-identity-miloapis-com-v1alpha1-useridentity,mutating=false,failurePolicy=fail,sideEffects=None,groups=identity.miloapis.com,resources=useridentities,verbs=delete,versions=v1alpha1,name=vuseridentity.identity.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// SetupUserIdentityWebhooksWithManager sets up the webhooks for UserIdentity
func SetupUserIdentityWebhooksWithManager(mgr ctrl.Manager) error {
	useridentitylog.Info("Setting up identity.miloapis.com useridentity webhooks")

	return ctrl.NewWebhookManagedBy(mgr, &identityv1alpha1.UserIdentity{}).
		WithCustomValidator(&UserIdentityValidator{}).
		Complete()
}

// UserIdentityValidator validates UserIdentity resources
type UserIdentityValidator struct{}

func (v *UserIdentityValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v *UserIdentityValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v *UserIdentityValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	userIdentity := obj.(*identityv1alpha1.UserIdentity)
	useridentitylog.Info("Blocking UserIdentity deletion", "name", userIdentity.Name)

	return nil, fmt.Errorf(
		"deleting UserIdentity resources is not currently supported. " +
			"Identity provider links must be managed through the authentication provider (e.g., Zitadel). " +
			"Automatic email synchronization logic is required before deletion can be enabled")
}
