package v1alpha1

import (
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// log is for logging in this package.
var organizationlog = logf.Log.WithName("organization-resource")

// +kubebuilder:webhook:path=/validate-resourcemanager-miloapis-com-v1alpha1-organization,mutating=false,failurePolicy=fail,sideEffects=NoneOnDryRun,groups=resourcemanager.miloapis.com,resources=organizations,verbs=create,versions=v1alpha1,name=vorganization.datum.net,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// SetupWebhooksWithManager sets up all resourcemanager.miloapis.com webhooks
func SetupOrganizationWebhooksWithManager(mgr ctrl.Manager, systemNamespace string, organizationOwnerRoleName string, organizationOwnerRoleNamespace string) error {
	organizationlog.Info("Setting up resourcemanager.miloapis.com organization webhooks")

	return ctrl.NewWebhookManagedBy(mgr, &resourcemanagerv1alpha1.Organization{}).
		WithCustomValidator(&OrganizationValidator{
			client:             mgr.GetClient(),
			systemNamespace:    systemNamespace,
			ownerRoleName:      organizationOwnerRoleName,
			ownerRoleNamespace: organizationOwnerRoleNamespace,
		}).
		Complete()
}

// OrganizationValidator validates Organizations
type OrganizationValidator struct {
	client             client.Client
	decoder            admission.Decoder
	systemNamespace    string
	ownerRoleName      string
	ownerRoleNamespace string
}

func (v *OrganizationValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	org := obj.(*resourcemanagerv1alpha1.Organization)
	organizationlog.Info("Validating Organization", "name", org.Name)

	// Validate organization name length
	if len(org.Name) > 50 {
		return nil, apierrors.NewInvalid(
			resourcemanagerv1alpha1.Kind("Organization"),
			org.Name,
			field.ErrorList{
				field.Invalid(
					field.NewPath("metadata", "name"),
					org.Name,
					"name exceeds maximum length of 50 characters. Choose a shorter name and try again",
				),
			},
		)
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get request from context: %w", err)
	}

	// Don't create policy binding or organization membership for dry run
	// requests.
	if req.DryRun != nil && *req.DryRun {
		return nil, nil
	}

	// Create namespace and PolicyBinding on Organization Create operation
	if err := v.createOrganizationNamespace(ctx, org); err != nil {
		return nil, fmt.Errorf("failed to create organization namespace: %w", err)
	}

	// Organizations created by system:masters shouldn't have a policy binding
	// or organization membership created because they're creating the organization
	// for another user in the system. It's expected those organizations will
	// create the necessary policy binding and organization membership to provide
	// the user access.
	//
	// TODO: Convert this to use a SubjectAccessReview to check if the user has
	//       permission to create an organization without a policy binding or
	//       organization membership.
	if slices.Contains(req.UserInfo.Groups, "system:masters") {
		return nil, nil
	}

	// Look up the user in the iam API
	user, err := v.lookupUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup user: %w", err)
	}

	// Create OrganizationMembership with owner role for the organization owner
	if err := v.createOrganizationMembership(ctx, org, user); err != nil {
		return nil, fmt.Errorf("failed to create organization membership: %w", err)
	}

	return nil, nil
}

func (v *OrganizationValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	newOrg := newObj.(*resourcemanagerv1alpha1.Organization)

	// Validate organization name length
	if len(newOrg.Name) > 50 {
		return nil, apierrors.NewInvalid(
			resourcemanagerv1alpha1.Kind("Organization"),
			newOrg.Name,
			field.ErrorList{
				field.Invalid(
					field.NewPath("metadata", "name"),
					newOrg.Name,
					"name exceeds maximum length of 50 characters. Choose a shorter name and try again",
				),
			},
		)
	}

	return nil, nil
}

func (v *OrganizationValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// lookupUser retrieves the User resource from the iam.miloapis.com API
func (v *OrganizationValidator) lookupUser(ctx context.Context) (*iamv1alpha1.User, error) {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get request from context: %w", err)
	}

	foundUser := &iamv1alpha1.User{}
	if err := v.client.Get(ctx, client.ObjectKey{Name: string(req.UserInfo.UID)}, foundUser); err != nil {
		return nil, fmt.Errorf("failed to get user '%s' from iam.miloapis.com API: %w", string(req.UserInfo.UID), err)
	}

	return foundUser, nil
}

// createOrganizationNamespace creates a namespace for organization-scoped resources
func (v *OrganizationValidator) createOrganizationNamespace(ctx context.Context, org *resourcemanagerv1alpha1.Organization) error {
	namespaceName := fmt.Sprintf("organization-%s", org.Name)
	organizationlog.Info("Creating namespace for organization", "organization", org.Name, "namespace", namespaceName)

	// Build the namespace object
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceName,
			Labels: map[string]string{
				"resourcemanager.miloapis.com/organization": org.Name,
				"resourcemanager.miloapis.com/type":         "organization",
			},
		},
	}

	if err := v.client.Create(ctx, namespace); err != nil {
		return fmt.Errorf("failed to create namespace resource: %w", err)
	}

	return nil
}

func (v *OrganizationValidator) createOrganizationMembership(ctx context.Context, org *resourcemanagerv1alpha1.Organization, user *iamv1alpha1.User) error {
	organizationlog.Info("Creating OrganizationMembership for organization owner", "organization", org.Name, "user", user.Name)

	// Build the OrganizationMembership object with owner role
	organizationMembership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("member-%s", user.Name),
			Namespace: fmt.Sprintf("organization-%s", org.Name),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: iamv1alpha1.SchemeGroupVersion.String(),
					Kind:       "User",
					Name:       user.Name,
					UID:        user.UID,
				},
			},
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: org.Name,
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: user.Name,
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      v.ownerRoleName,
					Namespace: v.ownerRoleNamespace,
				},
			},
		},
	}

	if err := v.client.Create(ctx, organizationMembership); err != nil {
		return fmt.Errorf("failed to create organization membership resource: %w", err)
	}

	return nil
}
