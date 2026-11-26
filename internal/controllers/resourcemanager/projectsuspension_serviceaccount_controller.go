package resourcemanager

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

// ProjectClientFunc returns a client for one project's control plane.
type ProjectClientFunc func(project string) (client.Client, error)

// NewProjectClientFunc returns a ProjectClientFunc that reaches each project's
// control plane through its endpoint on the core control plane at base.
func NewProjectClientFunc(base *rest.Config, scheme *runtime.Scheme) ProjectClientFunc {
	return func(project string) (client.Client, error) {
		cfg := rest.CopyConfig(base)
		host, err := url.Parse(cfg.Host)
		if err != nil {
			return nil, fmt.Errorf("parse API host %q: %w", cfg.Host, err)
		}
		host.Path = fmt.Sprintf("/apis/resourcemanager.miloapis.com/v1alpha1/projects/%s/control-plane", project)
		cfg.Host = host.String()
		return client.New(cfg, client.Options{Scheme: scheme})
	}
}

// ProjectServiceAccountSuspensionController marks the ServiceAccounts of a
// suspended project. While the project's Suspended condition is True, every
// ServiceAccount in the project's control plane carries a Suspended condition
// set to True. When the project is no longer suspended, the condition is
// removed. The auth provider reads the condition and stops the
// ServiceAccount from authenticating.
//
// It reacts only to Project changes. That is enough because a ServiceAccount
// cannot be created while its project is suspended: the
// ProjectSuspensionEnforcement admission plugin denies the create.
type ProjectServiceAccountSuspensionController struct {
	// Client reads Projects from the core control plane.
	Client client.Client
	// ProjectClient reaches a project's control plane.
	ProjectClient ProjectClientFunc
}

// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=serviceaccounts/status,verbs=get;update;patch

func (r *ProjectServiceAccountSuspensionController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !utilfeature.DefaultFeatureGate.Enabled(features.ProjectSuspension) {
		return ctrl.Result{}, nil
	}
	logger := log.FromContext(ctx)

	var project resourcemanagerv1alpha.Project
	if err := r.Client.Get(ctx, req.NamespacedName, &project); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !project.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	suspended := apimeta.IsStatusConditionTrue(project.Status.Conditions, resourcemanagerv1alpha.ProjectSuspended)

	// A project without a ready control plane has no ServiceAccounts to reach
	// yet. When it becomes Ready, the predicate reconciles it again.
	if !apimeta.IsStatusConditionTrue(project.Status.Conditions, resourcemanagerv1alpha.ProjectReady) {
		return ctrl.Result{}, nil
	}

	pc, err := r.ProjectClient(project.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("create client for project %q: %w", project.Name, err)
	}

	var serviceAccounts iamv1alpha1.ServiceAccountList
	if err := pc.List(ctx, &serviceAccounts); err != nil {
		return ctrl.Result{}, fmt.Errorf("list ServiceAccounts in project %q: %w", project.Name, err)
	}

	var desired *metav1.Condition
	if suspended {
		desired = &metav1.Condition{
			Type:    iamv1alpha1.ServiceAccountSuspended,
			Status:  metav1.ConditionTrue,
			Reason:  iamv1alpha1.ServiceAccountProjectSuspendedReason,
			Message: suspendedServiceAccountMessage(&project),
		}
	}

	var errs []error
	changed := 0
	for i := range serviceAccounts.Items {
		sa := &serviceAccounts.Items[i]
		if !sa.DeletionTimestamp.IsZero() {
			continue
		}
		updated, err := applyServiceAccountSuspension(ctx, pc, sa, desired)
		if err != nil {
			errs = append(errs, fmt.Errorf("ServiceAccount %q: %w", sa.Name, err))
			continue
		}
		if updated {
			changed++
		}
	}
	if changed > 0 {
		logger.Info("updated ServiceAccount suspension", "project", project.Name, "suspended", suspended, "serviceAccounts", changed)
	}
	return ctrl.Result{}, errors.Join(errs...)
}

// applyServiceAccountSuspension sets the Suspended condition to desired, or
// removes it when desired is nil. It reports whether it wrote the
// ServiceAccount.
//
// The patch carries the resourceVersion it read, because the auth provider
// also writes status.conditions and a merge patch replaces the whole list. A
// conflict returns an error, so the project is reconciled again with a fresh
// read.
func applyServiceAccountSuspension(ctx context.Context, c client.Client, sa *iamv1alpha1.ServiceAccount, desired *metav1.Condition) (bool, error) {
	current := apimeta.FindStatusCondition(sa.Status.Conditions, iamv1alpha1.ServiceAccountSuspended)

	base := sa.DeepCopy()
	switch {
	case desired == nil && current == nil:
		return false, nil
	case desired == nil:
		apimeta.RemoveStatusCondition(&sa.Status.Conditions, iamv1alpha1.ServiceAccountSuspended)
	case current != nil && current.Status == desired.Status && current.Reason == desired.Reason && current.Message == desired.Message:
		return false, nil
	default:
		apimeta.SetStatusCondition(&sa.Status.Conditions, *desired)
	}

	if err := c.Status().Patch(ctx, sa, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// suspendedServiceAccountMessage names the project and its suspension reasons.
func suspendedServiceAccountMessage(project *resourcemanagerv1alpha.Project) string {
	seen := map[string]bool{}
	var reasons []string
	for _, s := range project.Status.Suspensions {
		if r := string(s.Reason); r != "" && !seen[r] {
			seen[r] = true
			reasons = append(reasons, r)
		}
	}
	if len(reasons) == 0 {
		return fmt.Sprintf("Project %q is suspended.", project.Name)
	}
	return fmt.Sprintf("Project %q is suspended (%s).", project.Name, strings.Join(reasons, ", "))
}

// suspensionChanged reports whether a Project event can change what its
// ServiceAccounts should carry: the Suspended condition, the suspension
// reasons, or the control plane becoming ready.
func suspensionChanged(oldProject, newProject *resourcemanagerv1alpha.Project) bool {
	condition := func(p *resourcemanagerv1alpha.Project, t string) string {
		c := apimeta.FindStatusCondition(p.Status.Conditions, t)
		if c == nil {
			return ""
		}
		return string(c.Status)
	}
	if condition(oldProject, resourcemanagerv1alpha.ProjectSuspended) != condition(newProject, resourcemanagerv1alpha.ProjectSuspended) ||
		condition(oldProject, resourcemanagerv1alpha.ProjectReady) != condition(newProject, resourcemanagerv1alpha.ProjectReady) {
		return true
	}
	return suspendedServiceAccountMessage(oldProject) != suspendedServiceAccountMessage(newProject)
}

// everSuspended reports whether a Project has a Suspended condition at all.
// The propagator sets it to False rather than removing it when a suspension
// lifts, so a project that was never suspended has no condition, and its
// ServiceAccounts have nothing to add or remove.
func everSuspended(p *resourcemanagerv1alpha.Project) bool {
	return apimeta.FindStatusCondition(p.Status.Conditions, resourcemanagerv1alpha.ProjectSuspended) != nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectServiceAccountSuspensionController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resourcemanagerv1alpha.Project{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				p, ok := e.Object.(*resourcemanagerv1alpha.Project)
				return ok && everSuspended(p)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldP, okOld := e.ObjectOld.(*resourcemanagerv1alpha.Project)
				newP, okNew := e.ObjectNew.(*resourcemanagerv1alpha.Project)
				return okOld && okNew && (everSuspended(oldP) || everSuspended(newP)) && suspensionChanged(oldP, newP)
			},
			DeleteFunc:  func(event.DeleteEvent) bool { return false },
			GenericFunc: func(event.GenericEvent) bool { return false },
		})).
		Named("project-suspension-serviceaccounts").
		Complete(r)
}
