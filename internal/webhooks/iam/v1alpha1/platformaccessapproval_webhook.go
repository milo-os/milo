package v1alpha1

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

const platformAccessApprovalIndexKey = "iam.miloapis.com/platformaccessapproval"
const userEmailIndexKey = "iam.miloapis.com/user-email"

func buildPlatformAccessIndexKey(subject *iamv1alpha1.SubjectReference) string {
	if subject.UserRef != nil {
		return subject.UserRef.Name
	}
	return subject.Email
}

func SetupPlatformAccessApprovalWebhooksWithManager(mgr ctrl.Manager) error {
	// Index platformaccessapprovals by subject
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.PlatformAccessApproval{}, platformAccessApprovalIndexKey, func(rawObj client.Object) []string {
		paa := rawObj.(*iamv1alpha1.PlatformAccessApproval)
		return []string{buildPlatformAccessIndexKey(&paa.Spec.SubjectRef)}
	}); err != nil {
		return fmt.Errorf("failed to index platformaccessapproval key: %w", err)
	}

	// Index platformaccessrejections by user
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.PlatformAccessRejection{}, platformAccessRejectionIndexKey, func(rawObj client.Object) []string {
		par := rawObj.(*iamv1alpha1.PlatformAccessRejection)
		return []string{par.Spec.UserRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to index platformaccessrejection key: %w", err)
	}

	// Index users by email
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.User{}, userEmailIndexKey, func(rawObj client.Object) []string {
		user := rawObj.(*iamv1alpha1.User)
		return []string{strings.ToLower(user.Spec.Email)}
	}); err != nil {
		return fmt.Errorf("failed to index user email key: %w", err)
	}

	return ctrl.NewWebhookManagedBy(mgr, &iamv1alpha1.PlatformAccessApproval{}).
		WithDefaulter(&PlatformAccessApprovalMutator{
			client: mgr.GetClient(),
		}).
		WithValidator(&PlatformAccessApprovalValidator{
			client: mgr.GetClient(),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-iam-miloapis-com-v1alpha1-platformaccessapproval,mutating=true,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=platformaccessapprovals,verbs=create,versions=v1alpha1,name=mplatformaccessapproval.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// PlatformAccessApprovalMutator mutates PlatformAccessApproval resources to set the approver to the user who is approving the access request.
type PlatformAccessApprovalMutator struct {
	client client.Client
}

func (m *PlatformAccessApprovalMutator) Default(ctx context.Context, paa *iamv1alpha1.PlatformAccessApproval) error {
	log := logf.FromContext(ctx).WithValues("Defaulting PlatformAccessApproval", "name", paa.GetName())

	// Approver is the user who is approving the access request.
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		log.Error(err, "failed to get admission request from context", "name", paa.GetName())
		return errors.NewInternalError(fmt.Errorf("failed to get request from context: %w", err))
	}

	// Get the approver user
	approverUser := &iamv1alpha1.User{}
	isSystemUser := false
	if err := m.client.Get(ctx, client.ObjectKey{Name: string(req.UserInfo.UID)}, approverUser); err != nil {
		if errors.IsNotFound(err) {
			isSystemUser = true
			log.Info("user not found, probably a system user", "username", req.UserInfo.Username)
		} else {
			log.Error(err, "failed to get user '%s' from iam.miloapis.com API", string(req.UserInfo.UID))
			return errors.NewInternalError(fmt.Errorf("failed to get user '%s' from iam.miloapis.com API: %w", string(req.UserInfo.UID), err))
		}
	}

	approverRefName := req.UserInfo.Username
	if !isSystemUser {
		approverRefName = approverUser.Name
	}

	// Set the approver user
	paa.Spec.ApproverRef = &iamv1alpha1.UserReference{
		Name: approverRefName,
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-iam-miloapis-com-v1alpha1-platformaccessapproval,mutating=false,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=platformaccessapprovals,verbs=create,versions=v1alpha1,name=vplatformaccessapproval.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// PlatformAccessApprovalValidator validates PlatformAccessApproval resources.
type PlatformAccessApprovalValidator struct {
	client client.Client
}

func (v *PlatformAccessApprovalValidator) ValidateCreate(ctx context.Context, paa *iamv1alpha1.PlatformAccessApproval) (admission.Warnings, error) {
	log := logf.FromContext(ctx).WithValues("Validating PlatformAccessApproval", "name", paa.GetName())

	var errs field.ErrorList

	// Exactly one of subjectRef.email or subjectRef.userRef is
	// validated at API level, so we don't need to validate it here.

	// Validate subjectRef.email is valid
	emailAddress := paa.Spec.SubjectRef.Email
	if emailAddress != "" {
		if _, err := mail.ParseAddress(emailAddress); err != nil {
			log.Info("invalid email address", "email", emailAddress)
			errs = append(errs, field.Invalid(field.NewPath("spec").Child("subjectRef").Child("email"), emailAddress, fmt.Sprintf("invalid email address: %v", err)))
		}

		// Validate that the email address is NOT associated with a user
		users := &iamv1alpha1.UserList{}
		if err := v.client.List(ctx, users, client.MatchingFields{userEmailIndexKey: strings.ToLower(emailAddress)}); err != nil {
			log.Error(err, "failed to list users", "email", emailAddress)
			errs = append(errs, field.InternalError(field.NewPath("spec").Child("subjectRef").Child("email"), fmt.Errorf("failed to list users: %w", err)))
		}
		if len(users.Items) > 0 {
			log.Error(nil, "email address subject reference must only be used for not yet created users (user found for email address)", "email", emailAddress)
			errs = append(errs, field.Invalid(field.NewPath("spec").Child("subjectRef").Child("email"), emailAddress, "email address subject must refernce must only be used for not yet created users (user found for email address)"))
		}

		// Non existen users cannot have pre-existing platformaccessrejections,
		// as they are referenced by name, so okay to not check.
	}

	// Validate subjectRef.userRef is valid
	userRef := paa.Spec.SubjectRef.UserRef
	if userRef != nil {
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
	}

	// Validate that another PlatformAccessApproval already exists for the same subject
	existingPaas := &iamv1alpha1.PlatformAccessApprovalList{}
	if err := v.client.List(ctx, existingPaas, client.MatchingFields{platformAccessApprovalIndexKey: buildPlatformAccessIndexKey(&paa.Spec.SubjectRef)}); err != nil {
		log.Error(err, "failed to list platformaccessapprovals", "subject", buildPlatformAccessIndexKey(&paa.Spec.SubjectRef))
		errs = append(errs, field.InternalError(field.NewPath("spec").Child("subjectRef"), fmt.Errorf("failed to list platformaccessapprovals: %w", err)))
	}
	if len(existingPaas.Items) > 0 {
		log.Info("an platformaccessapproval already exists for the same subject", "subject", buildPlatformAccessIndexKey(&paa.Spec.SubjectRef))
		errs = append(errs, field.Invalid(field.NewPath("spec").Child("subjectRef"), buildPlatformAccessIndexKey(&paa.Spec.SubjectRef), "an existing platformaccessapproval already exists for the same subject."))
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("PlatformAccessApproval").GroupKind(), paa.Name, errs)
	}

	return nil, nil
}

func (v *PlatformAccessApprovalValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *iamv1alpha1.PlatformAccessApproval) (admission.Warnings, error) {
	return nil, nil
}

func (v *PlatformAccessApprovalValidator) ValidateDelete(ctx context.Context, obj *iamv1alpha1.PlatformAccessApproval) (admission.Warnings, error) {
	return nil, nil
}
