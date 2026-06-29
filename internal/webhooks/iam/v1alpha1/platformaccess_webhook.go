package v1alpha1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

const platformAccessUserIndexKey = "iam.miloapis.com/platformaccess-user"

var platformaccesslog = logf.Log.WithName("platformaccess-resource")

func SetupPlatformAccessWebhooksWithManager(mgr ctrl.Manager) error {
	platformaccesslog.Info("Setting up iam.miloapis.com platformaccess webhooks")

	// Index platformaccesses by userRef.name
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.PlatformAccess{}, platformAccessUserIndexKey, func(rawObj client.Object) []string {
		pa := rawObj.(*iamv1alpha1.PlatformAccess)
		return []string{pa.Spec.UserRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to index platformaccess user key: %w", err)
	}

	return ctrl.NewWebhookManagedBy(mgr, &iamv1alpha1.PlatformAccess{}).
		WithDefaulter(&PlatformAccessMutator{client: mgr.GetClient(), scheme: mgr.GetScheme()}).
		WithValidator(&PlatformAccessValidator{client: mgr.GetClient()}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-iam-miloapis-com-v1alpha1-platformaccess,mutating=true,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=platformaccesses,verbs=create,versions=v1alpha1,name=mplatformaccess.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// PlatformAccessMutator sets default values and owner references on PlatformAccess resources.
type PlatformAccessMutator struct {
	client client.Client
	scheme *runtime.Scheme
}

// Default sets the owner reference so the PlatformAccess is garbage collected when the User is deleted.
func (m *PlatformAccessMutator) Default(ctx context.Context, pa *iamv1alpha1.PlatformAccess) error {
	platformaccesslog.Info("Defaulting PlatformAccess", "name", pa.GetName())

	user := &iamv1alpha1.User{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: pa.Spec.UserRef.Name}, user); err != nil {
		platformaccesslog.Error(err, "failed to fetch referenced User while setting owner reference", "userName", pa.Spec.UserRef.Name)
		return errors.NewInternalError(fmt.Errorf("failed to fetch referenced User while setting owner reference: %w", err))
	}
	if err := controllerutil.SetOwnerReference(user, pa, m.scheme); err != nil {
		return errors.NewInternalError(fmt.Errorf("failed to set owner reference for platform access: %w", err))
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-iam-miloapis-com-v1alpha1-platformaccess,mutating=false,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=platformaccesses,verbs=create;delete,versions=v1alpha1,name=vplatformaccess.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// PlatformAccessValidator validates PlatformAccess resources.
type PlatformAccessValidator struct {
	client client.Client
}

func (v *PlatformAccessValidator) ValidateCreate(ctx context.Context, pa *iamv1alpha1.PlatformAccess) (admission.Warnings, error) {
	platformaccesslog.Info("Validating PlatformAccess", "name", pa.Name)

	var errs field.ErrorList

	if err := v.validateUserExists(ctx, pa); err != nil {
		errs = append(errs, err)
	}

	if err := v.validateUniquePlatformAccess(ctx, pa); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("PlatformAccess").GroupKind(), pa.Name, errs)
	}

	platformaccesslog.Info("User reference validation successful", "userName", pa.Spec.UserRef.Name)
	return nil, nil
}

func (v *PlatformAccessValidator) validateUserExists(ctx context.Context, pa *iamv1alpha1.PlatformAccess) *field.Error {
	userName := pa.Spec.UserRef.Name
	if userName == "" {
		return field.Required(field.NewPath("spec").Child("userRef").Child("name"), "userRef.name is required")
	}

	user := &iamv1alpha1.User{}
	err := v.client.Get(ctx, client.ObjectKey{Name: userName}, user)
	if errors.IsNotFound(err) {
		platformaccesslog.Error(err, "referenced user does not exist", "userName", userName)
		return field.NotFound(field.NewPath("spec").Child("userRef").Child("name"), userName)
	} else if err != nil {
		platformaccesslog.Error(err, "failed to validate user reference", "userName", userName)
		return field.InternalError(field.NewPath("spec").Child("userRef").Child("name"), fmt.Errorf("failed to validate user reference: %w", err))
	}
	return nil
}

func (v *PlatformAccessValidator) validateUniquePlatformAccess(ctx context.Context, pa *iamv1alpha1.PlatformAccess) *field.Error {
	userName := pa.Spec.UserRef.Name
	if userName == "" {
		return nil
	}

	var existingPAList iamv1alpha1.PlatformAccessList
	if err := v.client.List(ctx, &existingPAList, client.MatchingFields{platformAccessUserIndexKey: userName}); err != nil {
		platformaccesslog.Error(err, "failed to list existing PlatformAccesses", "userName", userName)
		return field.InternalError(field.NewPath("spec").Child("userRef"), fmt.Errorf("failed to list existing PlatformAccesses: %w", err))
	}

	if len(existingPAList.Items) > 0 {
		platformaccesslog.Error(fmt.Errorf("a PlatformAccess already exists for user '%s'", userName), "existing PlatformAccess found")
		return field.Invalid(field.NewPath("spec").Child("userRef"), userName, fmt.Sprintf("a PlatformAccess already exists for user '%s'", userName))
	}
	return nil
}

func (v *PlatformAccessValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *iamv1alpha1.PlatformAccess) (admission.Warnings, error) {
	return nil, nil
}

func (v *PlatformAccessValidator) ValidateDelete(ctx context.Context, obj *iamv1alpha1.PlatformAccess) (admission.Warnings, error) {
	// Allow deletion if the user is deleted or being deleted (garbage collection / cleanup)
	user := &iamv1alpha1.User{}
	err := v.client.Get(ctx, client.ObjectKey{Name: obj.Spec.UserRef.Name}, user)
	if errors.IsNotFound(err) || (err == nil && user.DeletionTimestamp != nil) {
		return nil, nil
	}

	return nil, errors.NewForbidden(iamv1alpha1.SchemeGroupVersion.WithResource("platformaccesses").GroupResource(), obj.Name, fmt.Errorf("deletion of PlatformAccess resources is not allowed; a user must always have a PlatformAccess resource"))
}
