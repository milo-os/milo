package v1alpha1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

const platformAccessRejectionIndexKey = "iam.miloapis.com/platformaccessrejection"

func SetupPlatformAccessRejectionWebhooksWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &iamv1alpha1.PlatformAccessRejection{}).
		WithDefaulter(&PlatformAccessRejectionMutator{
			client: mgr.GetClient(),
		}).
		WithValidator(&PlatformAccessRejectionValidator{
			client: mgr.GetClient(),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-iam-miloapis-com-v1alpha1-platformaccessrejection,mutating=true,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=platformaccessrejections,verbs=create,versions=v1alpha1,name=mplatformaccessrejection.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// PlatformAccessRejectionMutator mutates PlatformAccessRejection resources to set the rejecter to the user who is rejecting the access request.
type PlatformAccessRejectionMutator struct {
	client client.Client
}

func (m *PlatformAccessRejectionMutator) Default(ctx context.Context, par *iamv1alpha1.PlatformAccessRejection) error {
	log := logf.FromContext(ctx).WithValues("Defaulting PlatformAccessRejection", "name", par.GetName())

	// Rejecter is the user who is rejecting the access request.
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		log.Error(err, "failed to get admission request from context", "name", par.GetName())
		return errors.NewInternalError(fmt.Errorf("failed to get request from context: %w", err))
	}

	// Get the rejecter user
	rejecterUser := &iamv1alpha1.User{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: string(req.UserInfo.UID)}, rejecterUser); err != nil {
		// Rejecter should be found
		log.Error(err, "failed to get user '%s' from iam.miloapis.com API", string(req.UserInfo.UID))
		return errors.NewInternalError(fmt.Errorf("failed to get user '%s' from iam.miloapis.com API: %w", string(req.UserInfo.UID), err))
	}

	// Set the rejecter user
	par.Spec.RejecterRef = &iamv1alpha1.UserReference{
		Name: rejecterUser.Name,
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-iam-miloapis-com-v1alpha1-platformaccessrejection,mutating=false,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=platformaccessrejections,verbs=create,versions=v1alpha1,name=vplatformaccessrejection.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// PlatformAccessRejectionValidator validates PlatformAccessRejection resources.
type PlatformAccessRejectionValidator struct {
	client client.Client
}

func (v *PlatformAccessRejectionValidator) ValidateCreate(ctx context.Context, par *iamv1alpha1.PlatformAccessRejection) (admission.Warnings, error) {
	log := logf.FromContext(ctx).WithValues("Validating PlatformAccessRejection", "name", par.GetName())

	var errs field.ErrorList

	// Validate userRef
	userRef := par.Spec.UserRef
	user := &iamv1alpha1.User{}
	if err := v.client.Get(ctx, client.ObjectKey{Name: userRef.Name}, user); err != nil {
		if errors.IsNotFound(err) {
			log.Info("user not found", "name", userRef.Name)
			errs = append(errs, field.NotFound(field.NewPath("spec").Child("subjectRef").Child("userRef").Child("name"), userRef.Name))
		} else {
			log.Error(err, "failed to get user", "name", userRef.Name)
			errs = append(errs, field.InternalError(field.NewPath("spec").Child("subjectRef").Child("userRef").Child("name"), fmt.Errorf("failed to get user: %w", err)))
		}
	}

	// Validate that a PlatformAccessRejection already exists for the same subject
	existingPres := &iamv1alpha1.PlatformAccessRejectionList{}
	if err := v.client.List(ctx, existingPres, client.MatchingFields{platformAccessRejectionIndexKey: userRef.Name}); err != nil {
		log.Error(err, "failed to list platformaccessrejections", "subject", userRef.Name)
		errs = append(errs, field.InternalError(field.NewPath("spec").Child("subjectRef"), fmt.Errorf("failed to list platformaccessrejections: %w", err)))
	}
	if len(existingPres.Items) > 0 {
		log.Info("an platformaccessrejection already exists for the same subject", "subject", userRef.Name)
		errs = append(errs, field.Invalid(field.NewPath("spec").Child("subjectRef"), userRef.Name, "an existing platformaccessrejection already exists for the same subject."))
	}

	// Validate that a PlatformAccessApproval already exists for the same subject
	existingPaas := &iamv1alpha1.PlatformAccessApprovalList{}
	if err := v.client.List(ctx, existingPaas, client.MatchingFields{platformAccessApprovalIndexKey: userRef.Name}); err != nil {
		log.Error(err, "failed to list platformaccessapprovals", "subject", userRef.Name)
		errs = append(errs, field.InternalError(field.NewPath("spec").Child("subjectRef"), fmt.Errorf("failed to list platformaccessapprovals: %w", err)))
	}
	if len(existingPaas.Items) > 0 {
		log.Info("an platformaccessapproval already exists for the same subject", "subject", userRef.Name)
		errs = append(errs, field.Invalid(field.NewPath("spec").Child("subjectRef"), userRef.Name, "an existing platformaccessapproval already exists for the same subject."))
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("PlatformAccessRejection").GroupKind(), par.Name, errs)
	}

	return nil, nil
}

func (v *PlatformAccessRejectionValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *iamv1alpha1.PlatformAccessRejection) (admission.Warnings, error) {
	return nil, nil
}

func (v *PlatformAccessRejectionValidator) ValidateDelete(ctx context.Context, obj *iamv1alpha1.PlatformAccessRejection) (admission.Warnings, error) {
	return nil, nil
}
