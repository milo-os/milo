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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// ProjectSuspensionPropagatorController reconciles a Project object's suspension status
// by aggregating the ProjectSuspension resources that target it.
type ProjectSuspensionPropagatorController struct {
	Client client.Client
}

// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projectsuspensions,verbs=get;list;watch

const projectRefNameIndex = "spec.projectRef.name"
const controllerOwnerName = "project-suspension-controller"

func (r *ProjectSuspensionPropagatorController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var project resourcemanagerv1alpha.Project
	if err := r.Client.Get(ctx, req.NamespacedName, &project); apierrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get project: %w", err)
	}

	before := project.DeepCopy()

	logger.Info("reconciling project suspension status", "project", project.Name)

	// List ProjectSuspensions targeting this project
	var suspensionList resourcemanagerv1alpha.ProjectSuspensionList
	if err := r.Client.List(ctx, &suspensionList, client.MatchingFields{
		projectRefNameIndex: project.Name,
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list project suspensions: %w", err)
	}

	// Filter suspensions related to this project
	var suspensions []resourcemanagerv1alpha.ProjectSuspensionInfo
	var activeSuspensionNames []string

	for _, ps := range suspensionList.Items {
		suspensions = append(suspensions, resourcemanagerv1alpha.ProjectSuspensionInfo{
			Reason:             ps.Spec.Reason,
			SuspendedAt:        ps.CreationTimestamp,
			ReinstateAuthority: ps.Spec.ReinstateAuthority,
		})
		// Phase is active if it's explicitly Active, or not set to Lifted.
		if ps.Status.Phase == resourcemanagerv1alpha.PhaseActive || ps.Status.Phase == "" {
			activeSuspensionNames = append(activeSuspensionNames, ps.Name)
		}
	}

	// Sort suspensions deterministically (by Reason first, then by SuspendedAt)
	sort.Slice(suspensions, func(i, j int) bool {
		if suspensions[i].Reason != suspensions[j].Reason {
			return suspensions[i].Reason < suspensions[j].Reason
		}
		return suspensions[i].SuspendedAt.Before(&suspensions[j].SuspendedAt)
	})
	sort.Strings(activeSuspensionNames)

	project.Status.Suspensions = suspensions

	// Update the Suspended condition
	isSuspended := len(activeSuspensionNames) > 0
	var cond metav1.Condition
	if isSuspended {
		cond = metav1.Condition{
			Type:               resourcemanagerv1alpha.ProjectSuspended,
			Status:             metav1.ConditionTrue,
			Reason:             resourcemanagerv1alpha.ProjectSuspendedReason,
			Message:            fmt.Sprintf("Project is suspended due to active suspensions: %s", strings.Join(activeSuspensionNames, ", ")),
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

	if !equality.Semantic.DeepEqual(project.Status, before.Status) {
		if err := r.Client.Status().Patch(ctx, &project, client.MergeFrom(before), client.FieldOwner(controllerOwnerName)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to patch project status: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectSuspensionPropagatorController) SetupWithManager(mgr ctrl.Manager) error {
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
