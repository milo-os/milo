package v1alpha1

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// systemGroupPrefix identifies Group subjects that are synthesized by the
// platform (for example "system:authenticated-users"). These groups have no
// backing object and therefore no uid to resolve.
const systemGroupPrefix = "system:"

func SetupPolicyBindingWebhooksWithManager(mgr ctrl.Manager, mcMgr mcmanager.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &iamv1alpha1.PolicyBinding{}).
		WithDefaulter(&PolicyBindingMutator{
			client: mgr.GetClient(),
		}).
		WithValidator(&PolicyBindingValidator{
			client:         mgr.GetClient(),
			projectClients: &multiclusterProjectClientGetter{mcMgr: mcMgr},
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-iam-miloapis-com-v1alpha1-policybinding,mutating=true,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=policybindings,verbs=create;update,versions=v1alpha1,name=mpolicybinding.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users;groups;serviceaccounts,verbs=get;list;watch

// +kubebuilder:webhook:path=/validate-iam-miloapis-com-v1alpha1-policybinding,mutating=false,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=policybindings,verbs=create;update,versions=v1alpha1,name=vpolicybinding.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

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
//   - Subjects must all be ServiceAccounts: organization holders may attach
//     roles to the org's service accounts, but not to Users or Groups (either
//     of which is a route to granting humans). This is what makes an org's
//     PolicyBindings safe to let service accounts act within the org.
//
//   - The resourceSelector must target a Project that belongs to the request
//     organization: cross-org targets (another Organization, or a Project in
//     another org) are rejected, closing the cross-tenant privilege escalation
//     where an org holder could bind a role to any other org's resources.
//     Kind-level (resourceKind / Root) targets are rejected too, since they
//     grant at a scope wider than the org.
//
//   - Each ServiceAccount subject must belong to the target Project. A
//     ServiceAccount lives in exactly one project's control-plane cluster (one
//     cluster per project), so a project-scoped read of the subject only
//     succeeds for the project that hosts it. If the read fails — the SA is in
//     another project, or cannot be verified at all — the binding is denied.
//     This stops an org holder from granting a project's roles to a Service
//     Account that belongs to a different project in the same (or another) org.
//
//   - The binding's namespace must match the organization's namespace.
//
// Requests that carry no organization parent context (platform scope, internal
// controllers) are unaffected: they may bind Users, Groups, and any target.
// Requests made by system:masters (the platform superuser, e.g. the controller
// manager or an operator) also bypass the org-restrictions entirely, matching
// the organization and user webhooks.
type PolicyBindingValidator struct {
	client client.Client

	// projectClients returns the control-plane client for a project name. It is
	// a narrowed view of the multicluster manager so the validator can be unit
	// tested with a stub; production wiring always provides it. A nil value
	// falls closed: the validator can verify nothing and denies.
	projectClients projectClientGetter
}

// projectClientGetter returns a client for a project's control-plane cluster.
// Each project is fronted by its own control plane, so reads through this
// client are scoped to the project: an object that lives in a different
// project's control plane is not visible here.
type projectClientGetter interface {
	GetClientForProject(ctx context.Context, projectName string) (client.Client, error)
}

// multiclusterProjectClientGetter adapts the multicluster manager to
// projectClientGetter. Cluster names in the multicluster runtime are the
// project names (the same names used in URL paths and parent extras).
type multiclusterProjectClientGetter struct {
	mcMgr mcmanager.Manager
}

func (g *multiclusterProjectClientGetter) GetClientForProject(ctx context.Context, projectName string) (client.Client, error) {
	cluster, err := g.mcMgr.GetCluster(ctx, multicluster.ClusterName(projectName))
	if err != nil {
		return nil, fmt.Errorf("failed to get project control plane %q: %w", projectName, err)
	}
	return cluster.GetClient(), nil
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
	return nil, nil
}

// validateOrgContextRestrictions applies the organization-context restrictions
// to a PolicyBinding. When the request is not made in an organization context it
// returns nil, nil so platform-scope and internal-controller bindings pass
// through unchanged.
func (v *PolicyBindingValidator) validateOrgContextRestrictions(ctx context.Context, pb *iamv1alpha1.PolicyBinding) (admission.Warnings, error) {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		// A missing admission request indicates a webhook-infrastructure
		// failure, not a validation failure: surface it as an internal error
		// (reason InternalError, HTTP 500) rather than a Forbidden denial.
		return nil, errors.NewInternalError(fmt.Errorf("failed to get request from context: %w", err))
	}

	// Superusers may create PolicyBindings in organization context without the
	// org-restrictions, mirroring the organization and user webhooks
	// (system:masters bypasses platform validation). This lets the system
	// itself route a binding through an org control plane when needed. Internal
	// controllers normally write at platform scope (no parent extras) and
	// already bypass the restrictions via the early return below; this bypass
	// covers the org-scoped superuser path explicitly.
	if slices.Contains(req.UserInfo.Groups, "system:masters") {
		return nil, nil
	}

	// Determine whether this is an organization-scoped request by looking for
	// the parent-context extras injected by OrganizationContextAuthorizationDecorator.
	parentName, parentNameOk := req.UserInfo.Extra[iamv1alpha1.ParentNameExtraKey]
	parentKind, parentKindOk := req.UserInfo.Extra[iamv1alpha1.ParentKindExtraKey]
	parentAPIGroup, parentAPIGroupOk := req.UserInfo.Extra[iamv1alpha1.ParentAPIGroupExtraKey]

	if !parentNameOk || !parentKindOk || !parentAPIGroupOk {
		// Not an org-scoped request (e.g. platform scope or an internal
		// controller). No organization restrictions apply.
		return nil, nil
	}

	if len(parentKind) != 1 || len(parentName) != 1 || len(parentAPIGroup) != 1 {
		return nil, errors.NewInternalError(fmt.Errorf("request context has malformed parent information"))
	}

	if parentKind[0] != "Organization" || parentAPIGroup[0] != resourcemanagerv1alpha1.GroupVersion.Group {
		// A different scope (e.g. the future project control plane). Not the
		// org context this webhook guards.
		return nil, nil
	}

	orgID := parentName[0]

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

	// Target: only a Project within the request organization may be referenced.
	// targetProjectName is set when the target resolves to a concrete in-org
	// Project; it gates the cross-project ServiceAccount check below.
	var targetProjectName string
	selector := pb.Spec.ResourceSelector
	switch {
	case selector.ResourceRef != nil:
		ref := selector.ResourceRef
		if ref.APIGroup != resourcemanagerv1alpha1.GroupVersion.Group || ref.Kind != "Project" {
			errs = append(errs, field.NotSupported(
				field.NewPath("spec", "resourceSelector", "resourceRef", "kind"),
				ref.Kind,
				[]string{fmt.Sprintf("%s Project", resourcemanagerv1alpha1.GroupVersion.Group)},
			))
			break
		}

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
			break
		}

		targetProjectName = ref.Name
	case selector.ResourceKind != nil:
		errs = append(errs, field.Forbidden(
			field.NewPath("spec", "resourceSelector", "resourceKind"),
			"resourceKind (kind-level) targets are not allowed in organization context; bind to a specific Project instead",
		))
	}

	// Subjects: only ServiceAccounts may be bound in organization context, and
	// each must belong to the target Project. A ServiceAccount lives in exactly
	// one project's control-plane cluster, so a project-scoped read of the
	// subject succeeds only for the project that hosts it. Any failure to
	// confirm membership — the SA is in another project, has no project, or the
	// check itself cannot run — denies the binding (fail closed).
	for i := range pb.Spec.Subjects {
		subject := &pb.Spec.Subjects[i]
		subjectNamePath := field.NewPath("spec", "subjects").Index(i).Child("name")

		if subject.Kind != "ServiceAccount" {
			notSupported := field.NotSupported(
				field.NewPath("spec", "subjects").Index(i).Child("kind"),
				subject.Kind,
				[]string{"ServiceAccount"},
			)
			notSupported.Detail = fmt.Sprintf("organization-context policybindings may only grant roles to ServiceAccounts, not to Subject kind %q", subject.Kind)
			errs = append(errs, notSupported)
			continue
		}

		// The cross-project check only applies when the target resolved to a
		// concrete in-org project. If it did not (wrong kind, missing or foreign
		// project), the target errors above already deny the binding.
		if targetProjectName == "" {
			continue
		}

		if _, err := v.serviceAccountInProject(ctx, subject.Name, targetProjectName); err != nil {
			errs = append(errs, crossProjectSubjectError(subjectNamePath, subject.Name, targetProjectName, err))
		}
	}

	if len(errs) > 0 {
		return nil, errors.NewInvalid(iamv1alpha1.SchemeGroupVersion.WithKind("PolicyBinding").GroupKind(), pb.Name, errs)
	}

	return nil, nil
}

// serviceAccountInProject reports whether the named ServiceAccount exists in
// the given project's control-plane cluster. Because each project is fronted by
// its own control plane, the read succeeds only when the ServiceAccount
// actually lives in that project; a ServiceAccount that belongs to a different
// project (or to none) is not visible here and reads as NotFound.
//
// The project name is the cluster name the multicluster runtime keys clusters
// by — the same name used in URL paths and parent extras.
func (v *PolicyBindingValidator) serviceAccountInProject(ctx context.Context, saName, projectName string) (bool, error) {
	if v.projectClients == nil {
		// No project-cluster wiring: the validator can verify nothing, so it
		// reports failure and the caller falls closed.
		return false, fmt.Errorf("project client mapping is not configured")
	}

	projectClient, err := v.projectClients.GetClientForProject(ctx, projectName)
	if err != nil {
		return false, err
	}

	sa := &iamv1alpha1.ServiceAccount{}
	if err := projectClient.Get(ctx, client.ObjectKey{Name: saName}, sa); err != nil {
		return false, err
	}
	return true, nil
}

// crossProjectSubjectError converts a failed membership check into a field
// error. A NotFound means the ServiceAccount is not part of the target project;
// any other failure (missing wiring, control plane not reachable) is treated
// the same way: the subject cannot be verified as belonging to the target
// project, which fails closed.
func crossProjectSubjectError(path *field.Path, saName, projectName string, err error) *field.Error {
	if errors.IsNotFound(err) {
		return field.Forbidden(
			path,
			fmt.Sprintf("service account %q is not part of project %q and cannot be bound to it; service accounts live in exactly one project", saName, projectName),
		)
	}
	return field.Forbidden(
		path,
		fmt.Sprintf("cannot verify that service account %q is part of project %q: %v; the binding is denied because the service account's project could not be confirmed (fail closed)", saName, projectName, err),
	)
}
