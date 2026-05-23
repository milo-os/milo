package v1alpha1

import (
	"context"
	stderrors "errors"
	"fmt"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	"go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var projectlog = logf.Log.WithName("project-resource")

// SetupWebhooksWithManager sets up all resourcemanager.miloapis.com webhooks
func SetupProjectWebhooksWithManager(mgr ctrl.Manager, systemNamespace string, projectOwnerRoleName string, projectOwnerRoleNamespace string) error {
	projectlog.Info("Setting up resourcemanager.miloapis.com project webhooks")

	ctrl.NewWebhookManagedBy(mgr, &v1alpha1.Project{}).
		WithValidator(&ProjectValidator{
			Client:                    mgr.GetClient(),
			SystemNamespace:           systemNamespace,
			ProjectOwnerRoleName:      projectOwnerRoleName,
			ProjectOwnerRoleNamespace: projectOwnerRoleNamespace,
		}).
		WithDefaulter(&ProjectMutator{
			client: mgr.GetClient(),
		}).
		Complete()
	return nil
}

// +kubebuilder:webhook:path=/mutate-resourcemanager-miloapis-com-v1alpha1-project,mutating=true,failurePolicy=fail,sideEffects=NoneOnDryRun,groups=resourcemanager.miloapis.com,resources=projects,verbs=create,versions=v1alpha1,name=mproject.datum.net,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// ProjectMutator mutates Projects to add owner references and the owning
// organization based on the request context.
type ProjectMutator struct {
	client client.Client
}

func (m *ProjectMutator) Default(ctx context.Context, project *v1alpha1.Project) error {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get request from context: %w", err)
	}

	// The project webhook is always going to have the organization ID as the
	// parent context in the user's extra fields. Validate that the request
	// contains the required parent information and it's for an organization.
	parentName, parentNameOk := req.UserInfo.Extra[iamv1alpha1.ParentNameExtraKey]
	parentKind, parentKindOk := req.UserInfo.Extra[iamv1alpha1.ParentKindExtraKey]
	parentAPIGroup, parentAPIGroupOk := req.UserInfo.Extra[iamv1alpha1.ParentAPIGroupExtraKey]

	if !parentNameOk || !parentKindOk || !parentAPIGroupOk {
		const errMsg = "request context does not have the required parent information"
		projectlog.Error(stderrors.New(errMsg), errMsg)
		return stderrors.New(errMsg)
	}

	if len(parentKind) != 1 || parentKind[0] != "Organization" || parentAPIGroup[0] != v1alpha1.GroupVersion.Group {
		const errMsg = "request context has invalid parent information, must be Organization from the resourcemanager.miloapis.com API group"
		projectlog.Error(stderrors.New(errMsg), errMsg)
		return stderrors.New(errMsg)
	}

	requestContextOrgID := parentName[0]

	org := &v1alpha1.Organization{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: requestContextOrgID}, org); err != nil {
		return fmt.Errorf("failed to get organization '%s': %w", requestContextOrgID, err)
	}

	// Set a label on the project to indicate the organization it belongs to.
	metav1.SetMetaDataLabel(&project.ObjectMeta, v1alpha1.OrganizationNameLabel, requestContextOrgID)

	// If the request context has had an org id injected, default the parent to
	// the org. Once we introduce folders, this will need to change to leave the
	// value alone, and allow validation to ensure it's a valid parent folder.
	project.Spec.OwnerRef = v1alpha1.OwnerReference{
		Kind: "Organization",
		Name: org.Name,
	}

	project.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       "Organization",
			Name:       org.Name,
			UID:        org.GetUID(),
		},
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-resourcemanager-miloapis-com-v1alpha1-project,mutating=false,failurePolicy=fail,sideEffects=None,groups=resourcemanager.miloapis.com,resources=projects,verbs=create;update,versions=v1alpha1,name=vproject.datum.net,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// ProjectValidator validates Projects and creates associated PolicyBindings for owners.
type ProjectValidator struct {
	Client                    client.Client
	decoder                   admission.Decoder
	SystemNamespace           string
	ProjectOwnerRoleName      string
	ProjectOwnerRoleNamespace string
}

// ValidateCreate validates the Project and creates the associated PolicyBinding
// to provide the authenticated user with ownership access to the project.
func (v *ProjectValidator) ValidateCreate(ctx context.Context, project *v1alpha1.Project) (admission.Warnings, error) {
	projectlog.Info("Validating Project", "name", project.Name)
	errs := field.ErrorList{}

	// Validate project name length
	if len(project.Name) > 55 {
		errs = append(errs, field.Invalid(
			field.NewPath("metadata", "name"),
			project.Name,
			"name exceeds maximum length of 55 characters. Choose a shorter name and try again",
		))
	}

	if project.Spec.OwnerRef.Kind == "" {
		errs = append(errs, field.Invalid(field.NewPath("spec.ownerRef.kind"), project.Spec.OwnerRef.Kind, "must be set"))
	}

	if project.Spec.OwnerRef.Kind != "Organization" {
		errs = append(errs, field.Invalid(field.NewPath("spec.ownerRef.kind"), project.Spec.OwnerRef.Kind, "must be 'Organization'"))
	}

	if project.Spec.OwnerRef.Name == "" {
		errs = append(errs, field.Invalid(field.NewPath("spec.ownerRef.name"), project.Spec.OwnerRef.Name, "must be set"))
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(project.GroupVersionKind().GroupKind(), project.Name, errs)
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get request from context: %w", err)
	}

	// Don't create policy binding for dry run requests.
	if req.DryRun != nil && *req.DryRun {
		return nil, nil
	}

	if err := v.createOwnerPolicyBinding(ctx, project); err != nil {
		return nil, fmt.Errorf("failed to create owner policy binding: %w", err)
	}

	return nil, nil
}

func (v *ProjectValidator) ValidateUpdate(ctx context.Context, oldProject, newProject *v1alpha1.Project) (admission.Warnings, error) {
	projectlog.Info("Validating Project update", "name", newProject.Name)
	errs := field.ErrorList{}

	// Validate project name length
	if len(newProject.Name) > 55 {
		errs = append(errs, field.Invalid(
			field.NewPath("metadata", "name"),
			newProject.Name,
			"name exceeds maximum length of 55 characters. Choose a shorter name and try again",
		))
	}

	// Existing projects always have the organization label, so prevent any changes to it
	oldOrgLabel := oldProject.Labels[v1alpha1.OrganizationNameLabel]
	newOrgLabel, newExists := newProject.Labels[v1alpha1.OrganizationNameLabel]
	// The organization label must not be removed or changed
	if !newExists {
		errs = append(errs, field.Forbidden(field.NewPath("metadata.labels").Key(v1alpha1.OrganizationNameLabel), "organization label cannot be removed"))
	} else if oldOrgLabel != newOrgLabel {
		errs = append(errs, field.Forbidden(field.NewPath("metadata.labels").Key(v1alpha1.OrganizationNameLabel), "organization label cannot be changed"))
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(newProject.GroupVersionKind().GroupKind(), newProject.Name, errs)
	}

	return nil, nil
}

func (v *ProjectValidator) ValidateDelete(ctx context.Context, obj *v1alpha1.Project) (admission.Warnings, error) {
	return nil, nil
}

// lookupUser retrieves the User resource from the iam.miloapis.com API
func (v *ProjectValidator) lookupUser(ctx context.Context, username string) (*iamv1alpha1.User, error) {
	foundUser := &iamv1alpha1.User{}
	if err := v.Client.Get(ctx, client.ObjectKey{Name: username}, foundUser); err != nil {
		return nil, fmt.Errorf("failed to get user '%s' from iam.miloapis.com API: %w", username, err)
	}

	return foundUser, nil
}

// createOwnerPolicyBinding creates a PolicyBinding for the project owner
func (v *ProjectValidator) createOwnerPolicyBinding(ctx context.Context, project *v1alpha1.Project) error {
	projectlog.Info("Attempting to create PolicyBinding for new project", "project", project.Name)
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get request from context: %w", err)
	}

	// Look up the user in the iam API
	foundUser, err := v.lookupUser(ctx, req.UserInfo.UID)
	if err != nil {
		return fmt.Errorf("failed to lookup user: %w", err)
	}

	// Build the PolicyBinding
	policyBinding := &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			// Generate a unique name for the policy binding in case there's a
			// conflict with other policy bindings that were created by the user.
			GenerateName: fmt.Sprintf("project-%s-owner-", project.Name),
			// Create the policy binding in the organization's namespace that the
			// project belongs to.
			//
			// TODO: Will need to re-consider this when the folder type can be
			//       introduced as a parent. Maybe we have an Owner field in the spec?
			Namespace: fmt.Sprintf("organization-%s", project.Spec.OwnerRef.Name),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: v1alpha1.GroupVersion.String(),
					Kind:       "Project",
					Name:       project.Name,
					UID:        project.UID,
				},
			},
		},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef: iamv1alpha1.RoleReference{
				Name:      v.ProjectOwnerRoleName,
				Namespace: v.ProjectOwnerRoleNamespace,
			},
			Subjects: []iamv1alpha1.Subject{
				{
					Kind: "User",
					Name: foundUser.Name,
					UID:  string(foundUser.GetUID()),
				},
			},
			ResourceSelector: iamv1alpha1.ResourceSelector{
				ResourceRef: &iamv1alpha1.ResourceReference{
					APIGroup: v1alpha1.GroupVersion.Group,
					Kind:     "Project",
					Name:     project.Name,
					UID:      string(project.UID),
				},
			},
		},
	}

	// Create the PolicyBinding resource
	if err := v.Client.Create(ctx, policyBinding); err != nil {
		return fmt.Errorf("failed to create policy binding resource: %w", err)
	}

	return nil
}
