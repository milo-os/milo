package v1alpha1

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/webhook"
)

// systemGroupPrefix identifies Group subjects that are synthesized by the
// platform (for example "system:authenticated-users"). These groups have no
// backing object and therefore no uid to resolve.
const systemGroupPrefix = "system:"

func SetupPolicyBindingWebhooksWithManager(mgr ctrl.Manager, assignableRolesNamespace string) error {
	return ctrl.NewWebhookManagedBy(mgr, &iamv1alpha1.PolicyBinding{}).
		WithDefaulter(&PolicyBindingMutator{
			client: mgr.GetClient(),
		}).
		WithValidator(&PolicyBindingValidator{
			client:                   mgr.GetClient(),
			assignableRolesNamespace: assignableRolesNamespace,
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-iam-miloapis-com-v1alpha1-policybinding,mutating=true,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=policybindings,verbs=create;update,versions=v1alpha1,name=mpolicybinding.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users;groups;serviceaccounts;roles,verbs=get;list;watch

// +kubebuilder:webhook:path=/validate-iam-miloapis-com-v1alpha1-policybinding,mutating=false,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=policybindings,verbs=create;update;delete,versions=v1alpha1,name=vpolicybinding.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects,verbs=get

// PolicyBindingMutator resolves the uid of each PolicyBinding subject from its
// name. Callers may reference a User, Group, or ServiceAccount by name without
// supplying a uid; the mutator looks up the named object and stamps its current
// uid into the subject. This makes bindings declaratively committable (a Group
// and its PolicyBinding can be applied together by GitOps) while preserving the
// instance-pinning guarantee: the stored uid still identifies one specific
// object instance, so a delete+recreate of a same-named subject yields a new
// uid and the binding no longer matches until it is re-applied.
type PolicyBindingMutator struct {
	client client.Client
}

func (m *PolicyBindingMutator) Default(ctx context.Context, pb *iamv1alpha1.PolicyBinding) error {
	log := logf.FromContext(ctx).WithValues("policybinding", pb.GetName(), "namespace", pb.GetNamespace())

	var errs field.ErrorList
	subjectsPath := field.NewPath("spec").Child("subjects")

	for i := range pb.Spec.Subjects {
		subject := &pb.Spec.Subjects[i]
		subjectPath := subjectsPath.Index(i)

		// A subject that already carries a uid is left untouched: the uid is
		// immutable in the stored spec and callers (including internal
		// controllers) may set it explicitly.
		if subject.UID != "" {
			continue
		}

		// System groups have no backing object, so there is nothing to resolve.
		// The validating CEL rule permits these without a uid.
		if subject.Kind == "Group" && strings.HasPrefix(subject.Name, systemGroupPrefix) {
			continue
		}

		uid, fieldErr := m.resolveSubjectUID(ctx, subject, subjectPath)
		if fieldErr != nil {
			errs = append(errs, fieldErr)
			continue
		}

		log.Info("resolved subject uid from name", "kind", subject.Kind, "name", subject.Name, "uid", uid)
		subject.UID = uid
	}

	if len(errs) > 0 {
		return errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("PolicyBinding").GroupKind(), pb.Name, errs)
	}

	return nil
}

// resolveSubjectUID looks up the object named by the subject and returns its
// uid. It returns a field error when the subject is malformed or the named
// object does not exist.
func (m *PolicyBindingMutator) resolveSubjectUID(ctx context.Context, subject *iamv1alpha1.Subject, subjectPath *field.Path) (string, *field.Error) {
	namePath := subjectPath.Child("name")

	switch subject.Kind {
	case "User":
		user := &iamv1alpha1.User{}
		if err := m.client.Get(ctx, client.ObjectKey{Name: subject.Name}, user); err != nil {
			return "", lookupFieldError(namePath, subject.Name, "User", err)
		}
		return string(user.GetUID()), nil
	case "ServiceAccount":
		sa := &iamv1alpha1.ServiceAccount{}
		if err := m.client.Get(ctx, client.ObjectKey{Name: subject.Name}, sa); err != nil {
			return "", lookupFieldError(namePath, subject.Name, "ServiceAccount", err)
		}
		return string(sa.GetUID()), nil
	case "Group":
		// Groups are namespaced, so a namespace is required to resolve a
		// non-system Group by name.
		if subject.Namespace == "" {
			return "", field.Required(subjectPath.Child("namespace"), "namespace is required to resolve a Group subject by name")
		}
		group := &iamv1alpha1.Group{}
		if err := m.client.Get(ctx, client.ObjectKey{Namespace: subject.Namespace, Name: subject.Name}, group); err != nil {
			return "", lookupFieldError(namePath, subject.Name, "Group", err)
		}
		return string(group.GetUID()), nil
	default:
		return "", field.NotSupported(subjectPath.Child("kind"), subject.Kind, []string{"User", "Group", "ServiceAccount"})
	}
}

// lookupFieldError converts a client.Get error into a field error suitable for
// an admission response.
func lookupFieldError(namePath *field.Path, name, kind string, err error) *field.Error {
	if errors.IsNotFound(err) {
		notFound := field.NotFound(namePath, name)
		notFound.Detail = fmt.Sprintf("%s %q does not exist; cannot resolve subject uid", kind, name)
		return notFound
	}
	return field.InternalError(namePath, fmt.Errorf("failed to get %s %q: %w", kind, name, err))
}

// PolicyBindingValidator validates PolicyBindings.
//
// When the request comes from an organization context (the parent-context
// extras for an Organization are present in the request user), it enforces:
//
//   - The resourceSelector must stay inside the request organization. It may
//     target either the Organization itself (an organization-wide grant), or a
//     Project within it. Cross-org targets (the Organization by another name, or
//     a Project in another org) are rejected, closing the cross-tenant privilege
//     escalation where an org holder could bind a role to any other org's
//     resources. Kind-level (resourceKind / Root) targets are rejected too,
//     since they grant at a scope wider than the org.
//
//   - The binding's namespace must match the organization's namespace. On
//     create and update this is checked alongside the target; on delete it is
//     the only check, since a binding reachable in the org's namespace was
//     already constrained to the organization at create/update time.
//
//   - The bound role (roleRef) must be an assignable role in the assignable
//     roles namespace and must actually exist there. This stops an org holder
//     from referencing an arbitrary or dangling role; the namespace is driven
//     by a flag so it can change over time.
//
// A ServiceAccount subject is not required to live in the target Project's
// control plane: an organization admin may grant a ServiceAccount access
// outside of its own project (for example, IAM admin access across the whole
// organization). The org-containment rules above are what keep those grants
// inside the organization.
//
// Requests that carry no organization parent context (platform scope, internal
// controllers) are unaffected: they may bind Users, Groups, and any target.
// Requests made by system:masters (the platform superuser, e.g. the controller
// manager or an operator) also bypass the org-restrictions entirely, matching
// the organization and user webhooks.
type PolicyBindingValidator struct {
	client client.Client

	// assignableRolesNamespace is the namespace that contains the assignable
	// roles an org holder may bind. It is configurable so the location of the
	// assignable-role catalog can change in the future.
	assignableRolesNamespace string
}

func (v *PolicyBindingValidator) ValidateCreate(ctx context.Context, pb *iamv1alpha1.PolicyBinding) (admission.Warnings, error) {
	return v.validateOrgContextRestrictions(ctx, pb)
}

func (v *PolicyBindingValidator) ValidateUpdate(ctx context.Context, oldPB, newPB *iamv1alpha1.PolicyBinding) (admission.Warnings, error) {
	// roleRef and resourceSelector are immutable via CEL, but subjects are not:
	// an org holder could update a binding to swap in a User. Re-run the same
	// org-context restrictions on the new object.
	return v.validateOrgContextRestrictions(ctx, newPB)
}

func (v *PolicyBindingValidator) ValidateDelete(ctx context.Context, pb *iamv1alpha1.PolicyBinding) (admission.Warnings, error) {
	orgID, isOrgContext, err := v.orgContextFromRequest(ctx)
	if err != nil {
		return nil, err
	}
	if !isOrgContext {
		return nil, nil
	}

	// A binding may only be deleted from its own organization's namespace. The
	// bindings that live there were already constrained to the organization at
	// create/update time, so this single check is enough to stop an org holder
	// from deleting a binding in another organization's namespace.
	if pb.Namespace != resourcemanagerv1alpha1.OrganizationNamespace(orgID) {
		return nil, errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("PolicyBinding").GroupKind(), pb.Name, field.ErrorList{
			field.Forbidden(
				field.NewPath("metadata", "namespace"),
				fmt.Sprintf("policybindings in organization scope may only be deleted from the organization's namespace %q", resourcemanagerv1alpha1.OrganizationNamespace(orgID)),
			),
		})
	}

	return nil, nil
}

// orgContextFromRequest extracts the request organization id when the request
// is made in the organization context this webhook guards, layering the
// PolicyBinding-specific project-context fail-closed behavior on top of the
// shared detection in pkg/webhook. The shared detection already handles the
// missing-request, system:masters, and malformed-parent cases; this method only
// adds the Project-scope rejection, whose error message names PolicyBinding.
//
// It returns:
//   - (orgID, true, nil) when the request carries the parent-context extras
//     injected by OrganizationContextAuthorizationDecorator for an
//     Organization (the org scope this webhook guards).
//   - ("", false, nil) when the request is not in that context (platform scope,
//     an internal controller, a different scope, or system:masters, which
//     bypasses the platform validation), so callers pass the object through
//     unchanged.
//   - ("", false, err) on webhook-infrastructure failures (a missing admission
//     request or malformed parent extras), which callers surface as an internal
//     error rather than a validation denial.
func (v *PolicyBindingValidator) orgContextFromRequest(ctx context.Context) (string, bool, error) {
	orgID, isOrgContext, err := webhook.OrgContextFromRequest(ctx)
	if err != nil || isOrgContext {
		return orgID, isOrgContext, err
	}

	// Not an organization context. The project control plane is a known future
	// scope but is not wired up yet, so fail closed: reject any project-context
	// PolicyBinding. A developer enabling that scope is forced to extend this
	// webhook before any binding can slip through. Any other scope (there is
	// none today) passes through unchanged.
	projectName, isProjectContext, err := webhook.ProjectContextFromRequest(ctx)
	if err != nil {
		return "", false, err
	}
	if isProjectContext {
		return "", false, errors.NewInvalid(
			iamv1alpha1.SchemeGroupVersion.WithKind("PolicyBinding").GroupKind(),
			projectName,
			field.ErrorList{
				field.InternalError(
					field.NewPath("metadata", "namespace"),
					fmt.Errorf("project-context PolicyBindings are not yet supported; extend PolicyBindingValidator before enabling the project control plane"),
				),
			},
		)
	}

	return "", false, nil
}

// validateOrgContextRestrictions applies the organization-context restrictions
// to a PolicyBinding. When the request is not made in an organization context it
// returns nil, nil so platform-scope and internal-controller bindings pass
// through unchanged.
func (v *PolicyBindingValidator) validateOrgContextRestrictions(ctx context.Context, pb *iamv1alpha1.PolicyBinding) (admission.Warnings, error) {
	orgID, isOrgContext, err := v.orgContextFromRequest(ctx)
	if err != nil {
		return nil, err
	}
	if !isOrgContext {
		return nil, nil
	}

	var errs field.ErrorList

	// The binding must live in the request organization's namespace. The
	// authorizer already enforces this, so a mismatch means the direct client is
	// trying to cross namespaces; fail closed regardless.
	if pb.Namespace != resourcemanagerv1alpha1.OrganizationNamespace(orgID) {
		errs = append(errs, field.Invalid(
			field.NewPath("metadata", "namespace"),
			pb.Namespace,
			fmt.Sprintf("policybindings in organization scope must be created in the organization's namespace %q", resourcemanagerv1alpha1.OrganizationNamespace(orgID)),
		))
	}

	// Target: the grant must stay inside the request organization. It may
	// reference the Organization itself (an organization-wide grant) or a
	// Project within it, but nothing in another organization and no kind-level
	// selector.
	selector := pb.Spec.ResourceSelector
	switch {
	case selector.ResourceRef != nil:
		ref := selector.ResourceRef
		if ref.APIGroup != resourcemanagerv1alpha1.GroupVersion.Group {
			errs = append(errs, field.NotSupported(
				field.NewPath("spec", "resourceSelector", "resourceRef", "apiGroup"),
				ref.APIGroup,
				[]string{resourcemanagerv1alpha1.GroupVersion.Group},
			))
			break
		}

		switch ref.Kind {
		case "Project":
			// A specific Project: it must belong to the request organization.
			project := &resourcemanagerv1alpha1.Project{}
			if err := v.client.Get(ctx, client.ObjectKey{Name: ref.Name}, project); err != nil {
				if errors.IsNotFound(err) {
					errs = append(errs, field.NotFound(
						field.NewPath("spec", "resourceSelector", "resourceRef", "name"),
						ref.Name,
					))
				} else {
					errs = append(errs, field.InternalError(
						field.NewPath("spec", "resourceSelector", "resourceRef", "name"),
						fmt.Errorf("failed to get project %q: %w", ref.Name, err),
					))
				}
				break
			}

			if project.Labels[resourcemanagerv1alpha1.OrganizationNameLabel] != orgID {
				errs = append(errs, field.Forbidden(
					field.NewPath("spec", "resourceSelector", "resourceRef", "name"),
					fmt.Sprintf("project %q does not belong to organization %q", ref.Name, orgID),
				))
			}
		case "Organization":
			// An organization-wide grant: the referenced organization must be
			// the request organization itself.
			if ref.Name != orgID {
				errs = append(errs, field.Forbidden(
					field.NewPath("spec", "resourceSelector", "resourceRef", "name"),
					fmt.Sprintf("organization %q does not match the request organization %q", ref.Name, orgID),
				))
			}
		default:
			errs = append(errs, field.NotSupported(
				field.NewPath("spec", "resourceSelector", "resourceRef", "kind"),
				ref.Kind,
				[]string{
					fmt.Sprintf("%s Project", resourcemanagerv1alpha1.GroupVersion.Group),
					fmt.Sprintf("%s Organization", resourcemanagerv1alpha1.GroupVersion.Group),
				},
			))
		}
	case selector.ResourceKind != nil:
		errs = append(errs, field.Forbidden(
			field.NewPath("spec", "resourceSelector", "resourceKind"),
			"resourceKind (kind-level) targets are not allowed in organization context; bind to a specific Project or the Organization instead",
		))
	}

	// The bound role must be an assignable role in the assignable-roles
	// namespace, and must actually exist there. This stops an org holder from
	// referencing an arbitrary or dangling role. See validateAssignableRole.
	roleRef := pb.Spec.RoleRef
	errs = append(errs, validateAssignableRole(ctx, v.client, roleRef.Name, roleRef.Namespace, v.assignableRolesNamespace, field.NewPath("spec", "roleRef"))...)

	if len(errs) > 0 {
		return nil, errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("PolicyBinding").GroupKind(), pb.Name, errs)
	}

	return nil, nil
}
