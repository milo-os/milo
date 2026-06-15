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

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

// systemGroupPrefix identifies Group subjects that are synthesized by the
// platform (for example "system:authenticated-users"). These groups have no
// backing object and therefore no uid to resolve.
const systemGroupPrefix = "system:"

func SetupPolicyBindingWebhooksWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &iamv1alpha1.PolicyBinding{}).
		WithDefaulter(&PolicyBindingMutator{
			client: mgr.GetClient(),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-iam-miloapis-com-v1alpha1-policybinding,mutating=true,failurePolicy=fail,sideEffects=None,groups=iam.miloapis.com,resources=policybindings,verbs=create;update,versions=v1alpha1,name=mpolicybinding.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users;groups;serviceaccounts,verbs=get;list;watch

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
		return field.Invalid(namePath, name, fmt.Sprintf("%s %q not found; cannot resolve subject uid", kind, name))
	}
	return field.InternalError(namePath, fmt.Errorf("failed to get %s %q: %w", kind, name, err))
}
