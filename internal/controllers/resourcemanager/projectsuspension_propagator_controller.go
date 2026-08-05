package resourcemanager

import (
	"context"
	"fmt"
	"sort"
	"strings"

	equality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

// ProjectSuspensionPropagatorController reconciles a Project object's suspension status
// by aggregating the ProjectSuspension resources that target it.
type ProjectSuspensionPropagatorController struct {
	Client client.Client

	// EventEmitter creates the consumer-facing Suspended/Reinstated events.
	// It impersonates the project's parent context so the events proxy can
	// scope-tag them; see ProjectEventEmitter for why the manager's shared
	// EventRecorder cannot. Defaulted in SetupWithManager.
	EventEmitter projectEventEmitter
}

// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projectsuspensions,verbs=get;list;watch

// Consumer-facing Suspended/Reinstated events are created through a client
// that impersonates the controller's own service account plus the target
// project's parent context, so the events proxy can scope-tag them
// (see ProjectEventEmitter). That needs impersonate on the service account
// itself — a username of the form system:serviceaccount:<ns>:<name> is
// authorized against the serviceaccounts resource, not users — and on each
// parent-context extra key, which are checked individually as
// userextras/<key> subresources.
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=impersonate
// +kubebuilder:rbac:groups=authentication.k8s.io,resources=userextras/iam.miloapis.com/parent-type,verbs=impersonate
// +kubebuilder:rbac:groups=authentication.k8s.io,resources=userextras/iam.miloapis.com/parent-name,verbs=impersonate
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create

const projectRefNameIndex = "spec.projectRef.name"
const controllerOwnerName = "project-suspension-controller"

// defaultImpersonateUser is the principal the event emitter asserts when none
// is configured. The controller-manager must hold `impersonate` on it, plus on
// userextras/iam.miloapis.com/parent-type and .../parent-name.
const defaultImpersonateUser = "system:serviceaccount:milo-system:milo-controller-manager"

// getProjectSuspendedStatus returns true if the project is suspended, false otherwise.
func getProjectSuspendedStatus(project *resourcemanagerv1alpha.Project) bool {
	cond := apimeta.FindStatusCondition(project.Status.Conditions, resourcemanagerv1alpha.ProjectSuspended)
	return cond != nil && cond.Status == metav1.ConditionTrue
}

// getLatestActiveSuspensionTime returns the latest active suspension time for a project.
func getLatestActiveSuspensionTime(suspensions []resourcemanagerv1alpha.ProjectSuspension, projectName string) metav1.Time {
	var latestTime metav1.Time
	for _, ps := range suspensions {
		if (ps.Status.Phase == resourcemanagerv1alpha.PhaseActive || ps.Status.Phase == "") && (ps.Spec.ProjectRef.Name == projectName) {
			if latestTime.IsZero() || ps.CreationTimestamp.After(latestTime.Time) {
				latestTime = ps.CreationTimestamp
			}
		}
	}
	return latestTime
}

func (r *ProjectSuspensionPropagatorController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !utilfeature.DefaultFeatureGate.Enabled(features.ProjectSuspension) {
		return ctrl.Result{}, nil
	}

	logger := log.FromContext(ctx)

	var project resourcemanagerv1alpha.Project
	if err := r.Client.Get(ctx, req.NamespacedName, &project); apierrors.IsNotFound(err) {
		clearActiveSuspensions(req.Name)
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get project: %w", err)
	}

	before := project.DeepCopy()

	if !project.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	logger.Info("reconciling project suspension status", "project", project.Name)

	// List ProjectSuspensions targeting this project
	var suspensionList resourcemanagerv1alpha.ProjectSuspensionList
	if err := r.Client.List(ctx, &suspensionList, client.MatchingFields{
		projectRefNameIndex: project.Name,
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list project suspensions: %w", err)
	}

	// Filter suspensions related to this project. status.Suspensions only
	// ever holds active suspensions (see ProjectSuspensionInfo's doc
	// comment) — a ProjectSuspension whose phase has moved past Active
	// (e.g. Lifted) is excluded, mirroring the activeSuspensionNames/
	// activeReasons filtering below.
	var suspensions []resourcemanagerv1alpha.ProjectSuspensionInfo
	var activeReasons []string
	seenReasons := make(map[string]bool)

	for _, ps := range suspensionList.Items {
		// Active means the phase is explicitly Active, or still unset because
		// the suspension hasn't been reconciled yet. Any other phase (today
		// only Lifted) is treated as inactive.
		if ps.Status.Phase != resourcemanagerv1alpha.PhaseActive && ps.Status.Phase != "" {
			continue
		}

		suspensions = append(suspensions, resourcemanagerv1alpha.ProjectSuspensionInfo{
			Reason:             ps.Spec.Reason,
			SuspendedAt:        ps.CreationTimestamp,
			ReinstateAuthority: ps.Spec.ReinstateAuthority,
		})
		reason := string(ps.Spec.Reason)
		if !seenReasons[reason] {
			seenReasons[reason] = true
			activeReasons = append(activeReasons, reason)
		}
	}

	// Report metrics
	reportActiveSuspensions(project.Name, activeReasons)

	// Sort suspensions deterministically (by Reason first, then by SuspendedAt)
	sort.Slice(suspensions, func(i, j int) bool {
		if suspensions[i].Reason != suspensions[j].Reason {
			return suspensions[i].Reason < suspensions[j].Reason
		}
		return suspensions[i].SuspendedAt.Before(&suspensions[j].SuspendedAt)
	})
	sort.Strings(activeReasons)

	project.Status.Suspensions = suspensions

	// Update the Suspended condition.
	//
	// The message names only the suspension reason categories (e.g. "Abuse"),
	// never the ProjectSuspension resource names. Project.status is read by
	// project members, so this message is a tenant-facing surface and carries
	// the same redaction requirement as the Suspended event emitted by
	// recordTransition — resource names, requestedBy and description are
	// internal admin detail.
	isSuspended := len(suspensions) > 0
	var cond metav1.Condition
	if isSuspended {
		cond = metav1.Condition{
			Type:               resourcemanagerv1alpha.ProjectSuspended,
			Status:             metav1.ConditionTrue,
			Reason:             resourcemanagerv1alpha.ProjectSuspendedReason,
			Message:            fmt.Sprintf("Project is suspended (category: %s)", strings.Join(activeReasons, ", ")),
			ObservedGeneration: project.Generation,
		}
	} else {
		cond = metav1.Condition{
			Type:               resourcemanagerv1alpha.ProjectSuspended,
			Status:             metav1.ConditionFalse,
			Reason:             resourcemanagerv1alpha.ProjectActiveReason,
			Message:            "Project is active",
			ObservedGeneration: project.Generation,
		}
	}

	apimeta.SetStatusCondition(&project.Status.Conditions, cond)

	wasSuspended := getProjectSuspendedStatus(before)
	isSuspendedNow := cond.Status == metav1.ConditionTrue

	if !equality.Semantic.DeepEqual(project.Status, before.Status) {
		if err := r.Client.Status().Patch(ctx, &project, client.MergeFrom(before), client.FieldOwner(controllerOwnerName)); err != nil {
			if isSuspendedNow {
				recordPauseFailure()
			}
			return ctrl.Result{}, fmt.Errorf("failed to patch project status: %w", err)
		}

		var latestTime metav1.Time
		if !wasSuspended && isSuspendedNow {
			latestTime = getLatestActiveSuspensionTime(suspensionList.Items, project.Name)
		}
		recordTransition(ctx, r.EventEmitter, &project, wasSuspended, isSuspendedNow, activeReasons, latestTime)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectSuspensionPropagatorController) SetupWithManager(mgr ctrl.Manager) error {
	if r.EventEmitter == nil {
		// Events are emitted through an impersonating client rather than
		// mgr.GetEventRecorderFor: the recorder is backed by a single
		// broadcaster over one static identity, but scope requires per-Project
		// parent context on the request.
		r.EventEmitter = NewProjectEventEmitter(mgr.GetConfig(), defaultImpersonateUser, controllerOwnerName)
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &resourcemanagerv1alpha.ProjectSuspension{}, projectRefNameIndex, func(rawObj client.Object) []string {
		obj := rawObj.(*resourcemanagerv1alpha.ProjectSuspension)
		if obj.Spec.ProjectRef.Name == "" {
			return nil
		}
		return []string{obj.Spec.ProjectRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&resourcemanagerv1alpha.Project{}).
		Watches(
			&resourcemanagerv1alpha.ProjectSuspension{},
			handler.EnqueueRequestsFromMapFunc(r.findProjectsForProjectSuspension),
		).
		Named("project-suspension").
		Complete(r)
}

func (r *ProjectSuspensionPropagatorController) findProjectsForProjectSuspension(ctx context.Context, obj client.Object) []reconcile.Request {
	ps, ok := obj.(*resourcemanagerv1alpha.ProjectSuspension)
	if !ok {
		return nil
	}
	if ps.Spec.ProjectRef.Name == "" {
		return nil
	}
	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name: ps.Spec.ProjectRef.Name,
			},
		},
	}
}
