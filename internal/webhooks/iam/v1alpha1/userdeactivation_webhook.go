package v1alpha1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var userdeactivationlog = logf.Log.WithName("userdeactivation-resource")

func SetupUserDeactivationWebhooksWithManager(mgr ctrl.Manager, systemNamespace string) error {
	userdeactivationlog.Info("Setting up iam.miloapis.com userdeactivation webhooks")

	return ctrl.NewWebhookManagedBy(mgr, &iamv1alpha1.UserDeactivation{}).
		WithDefaulter(&UserDeactivationMutator{client: mgr.GetClient(), scheme: mgr.GetScheme()}).
		WithValidator(&UserDeactivationValidator{
			client:          mgr.GetClient(),
			systemNamespace: systemNamespace,
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-iam-miloapis-com-v1alpha1-userdeactivation,mutating=true,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=userdeactivations,verbs=create,versions=v1alpha1,name=muserdeactivation.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// UserDeactivationMutator sets default values on UserDeactivation resources.
type UserDeactivationMutator struct {
	client client.Client
	scheme *runtime.Scheme
}

// Default sets the deactivatedBy field to the username of the requesting user if it is not already set.
func (m *UserDeactivationMutator) Default(ctx context.Context, ud *iamv1alpha1.UserDeactivation) error {
	userdeactivationlog.Info("Defaulting UserDeactivation", "name", ud.GetName())

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		userdeactivationlog.Error(err, "failed to get admission request from context", "name", ud.GetName())
		return fmt.Errorf("failed to get request from context: %w", err)
	}

	// Populate the field with the username present in the access token / UserInfo.
	ud.Spec.DeactivatedBy = req.UserInfo.Username

	userdeactivationlog.Info("Defaulting deactivatedBy complete", "name", ud.GetName(), "deactivatedBy", ud.Spec.DeactivatedBy)

	// Set the owner reference so the UserDeactivation is garbage collected when the User is deleted.
	// The user is guaranteed to exist, as ValidateCreate validates that the user exists
	user := &iamv1alpha1.User{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: ud.Spec.UserRef.Name}, user); err != nil {
		userdeactivationlog.Error(err, "failed to fetch referenced User while setting owner reference", "userName", ud.Spec.UserRef.Name)
		return errors.NewInternalError(fmt.Errorf("failed to fetch referenced User while setting owner reference, %w", err))
	}
	if err := controllerutil.SetOwnerReference(user, ud, m.scheme); err != nil {
		return errors.NewInternalError(fmt.Errorf("failed to set owner reference for user deactivation: %w", err))
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-iam-miloapis-com-v1alpha1-userdeactivation,mutating=false,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=userdeactivations,verbs=create,versions=v1alpha1,name=vuserdeactivation.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

type UserDeactivationValidator struct {
	client          client.Client
	systemNamespace string
}

func (v *UserDeactivationValidator) ValidateCreate(ctx context.Context, userDeactivation *iamv1alpha1.UserDeactivation) (admission.Warnings, error) {
	userdeactivationlog.Info("Validating UserDeactivation", "name", userDeactivation.Name)

	var errs field.ErrorList

	userName := userDeactivation.Spec.UserRef.Name
	if userName == "" {
		errs = append(errs, field.Required(field.NewPath("spec").Child("userRef").Child("name"), "userRef.name is required"))
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("UserDeactivation").GroupKind(), userDeactivation.Name, errs)
	}

	// Validate that the referenced user exists
	user := &iamv1alpha1.User{}
	err := v.client.Get(ctx, client.ObjectKey{Name: userName}, user)
	if errors.IsNotFound(err) {
		userdeactivationlog.Error(err, "referenced user does not exist", "userName", userName)
		errs = append(errs, field.NotFound(field.NewPath("spec").Child("userRef").Child("name"), userName))
		return nil, errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("UserDeactivation").GroupKind(), userDeactivation.Name, errs)
	} else if err != nil {
		userdeactivationlog.Error(err, "failed to validate user reference", "userName", userName)
		return nil, errors.NewInternalError(fmt.Errorf("failed to validate user reference"))
	}

	// Ensure there is no existing UserDeactivation for the same user
	var existingUDList iamv1alpha1.UserDeactivationList
	if err := v.client.List(ctx, &existingUDList); err != nil {
		userdeactivationlog.Error(err, "failed to list existing UserDeactivations", "userName", userName)
		return nil, fmt.Errorf("failed to list existing UserDeactivations for user '%s': %w", userName, err)
	}
	for _, existing := range existingUDList.Items {
		if existing.Spec.UserRef.Name == userName && existing.DeletionTimestamp == nil {
			userdeactivationlog.Error(fmt.Errorf("a UserDeactivation already exists for user '%s'", userName), "existing UserDeactivation", "name", existing.Name)
			return nil, errors.NewAlreadyExists(iamv1alpha1.SchemeGroupVersion.WithResource("userdeactivations").GroupResource(), userDeactivation.Name)
		}
	}

	userdeactivationlog.Info("User reference validation successful", "userName", userName)

	return nil, nil
}

func (v *UserDeactivationValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *iamv1alpha1.UserDeactivation) (admission.Warnings, error) {
	return nil, nil
}

func (v *UserDeactivationValidator) ValidateDelete(ctx context.Context, obj *iamv1alpha1.UserDeactivation) (admission.Warnings, error) {
	return nil, nil
}
