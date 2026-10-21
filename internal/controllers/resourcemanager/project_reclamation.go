package resourcemanager

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	infrastructurev1alpha1 "go.miloapis.com/milo/pkg/apis/infrastructure/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// reclaimProjectControlPlane reports completion only after observing absence.
// Preconditions bind DELETE to the object inspected, including its Project UID
// label; a replacement or intervening update must be reconciled again.
func (r *ProjectController) reclaimProjectControlPlane(ctx context.Context, project *resourcemanagerv1alpha1.Project) (bool, error) {
	if project.UID == "" || project.DeletionTimestamp.IsZero() {
		return false, fmt.Errorf("control plane reclamation requires a terminating Project UID")
	}
	// Deployments without an infrastructure controller have no PCP to reclaim.
	if r.InfraClient == nil {
		return true, nil
	}
	if r.InfraReader == nil {
		return false, fmt.Errorf("uncached infrastructure reader is required for reclamation")
	}
	var pcp infrastructurev1alpha1.ProjectControlPlane
	if err := r.InfraReader.Get(ctx, client.ObjectKeyFromObject(project), &pcp); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("get projectcontrolplane for reclamation: %w", err)
	}
	if pcp.Labels[resourcemanagerv1alpha1.ProjectUIDLabel] != string(project.UID) || pcp.UID == "" || pcp.ResourceVersion == "" {
		return false, fmt.Errorf("projectcontrolplane identity does not match terminating Project")
	}
	if !pcp.DeletionTimestamp.IsZero() {
		return false, nil
	}
	uid, rv := pcp.UID, pcp.ResourceVersion
	if err := r.InfraClient.Delete(ctx, &pcp, &client.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv},
	}); err != nil {
		// Even NotFound is retried through GET before releasing the Project.
		return false, fmt.Errorf("delete projectcontrolplane: %w", err)
	}
	return false, nil
}

// completedProject revalidates the cleanup checkpoint against the root API.
// A cached completion condition cannot authorize a destructive operation after
// the authoritative condition changes. This read does not fence another object.
func (r *ProjectController) completedProject(ctx context.Context, original *resourcemanagerv1alpha1.Project) (*resourcemanagerv1alpha1.Project, error) {
	if original.UID == "" || original.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("reclamation requires a terminating Project UID")
	}
	if r.ControlPlaneReader == nil {
		return nil, fmt.Errorf("uncached root reader is required for reclamation")
	}
	current := &resourcemanagerv1alpha1.Project{}
	if err := r.ControlPlaneReader.Get(ctx, client.ObjectKeyFromObject(original), current); err != nil {
		return nil, fmt.Errorf("re-fetch project: %w", err)
	}
	if current.UID != original.UID || current.ResourceVersion == "" || current.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("project identity changed before reclamation")
	}
	condition := apimeta.FindStatusCondition(current.Status.Conditions, resourcemanagerv1alpha1.ProjectResourceCleanup)
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != resourcemanagerv1alpha1.ProjectCleanupCompleteReason {
		return nil, fmt.Errorf("current Project cleanup is not complete")
	}
	return current, nil
}

func (r *ProjectController) removeProjectFinalizer(ctx context.Context, original *resourcemanagerv1alpha1.Project) error {
	current, err := r.completedProject(ctx, original)
	if err != nil {
		return err
	}
	before := current.DeepCopy()
	controllerutil.RemoveFinalizer(current, projectFinalizer)
	if err := r.ControlPlaneClient.Patch(ctx, current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("remove project finalizer: %w", err)
	}
	return nil
}

// Cleanup is already durably recorded before entering this stage; the project
// API is allowed to stop responding when the infrastructure controller exits.
func (r *ProjectController) finishProjectReclamation(ctx context.Context, project *resourcemanagerv1alpha1.Project) (ctrl.Result, error) {
	current, err := r.completedProject(ctx, project)
	if err != nil {
		return ctrl.Result{}, err
	}
	gone, err := r.reclaimProjectControlPlane(ctx, current)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !gone {
		return ctrl.Result{RequeueAfter: cleanupPollInterval}, nil
	}
	return ctrl.Result{}, r.removeProjectFinalizer(ctx, project)
}
