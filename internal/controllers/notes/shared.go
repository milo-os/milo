package notes

import (
	"context"
	"fmt"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	noteReadyConditionType   = "Ready"
	noteReadyConditionReason = "Reconciled"

	noteManagedByLabel = "notes.miloapis.com/managed-by"
	noteManagedByValue = "note-controller"
)

// NoteResource is an interface that both Note and ClusterNote implement
type NoteResource interface {
	client.Object
	GetCreatorRef() iamv1alpha1.UserReference
	GetNoteKind() string
}

// ensureCreatorEditorPolicyBinding creates or checks a PolicyBinding for the note creator
func ensureCreatorEditorPolicyBinding(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	noteResource NoteResource,
	creator *iamv1alpha1.User,
	creatorEditorRoleName string,
	creatorEditorRoleNamespace string,
) (bool, string, error) {
	log := log.FromContext(ctx)

	bindingName := fmt.Sprintf("note-creator-editor-%s", noteResource.GetName())
	if noteResource.GetNamespace() != "" {
		bindingName = fmt.Sprintf("note-creator-editor-%s-%s", noteResource.GetNamespace(), noteResource.GetName())
	}

	// Determine the namespace for the PolicyBinding:
	// - For Note (namespaced): use the Note's namespace
	// - For ClusterNote: use milo-system (creatorEditorRoleNamespace)
	policyBindingNamespace := creatorEditorRoleNamespace
	if noteResource.GetNamespace() != "" {
		policyBindingNamespace = noteResource.GetNamespace()
	}

	var existing iamv1alpha1.PolicyBinding
	if err := c.Get(ctx, types.NamespacedName{Name: bindingName, Namespace: policyBindingNamespace}, &existing); err == nil {
		return isPolicyBindingReady(&existing)
	} else if !apierrors.IsNotFound(err) {
		return false, "", fmt.Errorf("failed to get existing creator PolicyBinding: %w", err)
	}

	policyBinding := &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingName,
			Namespace: policyBindingNamespace,
			Labels: map[string]string{
				noteManagedByLabel: noteManagedByValue,
			},
		},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef: iamv1alpha1.RoleReference{
				Name:      creatorEditorRoleName,
				Namespace: creatorEditorRoleNamespace,
			},
			Subjects: []iamv1alpha1.Subject{
				{
					Kind: "User",
					Name: creator.Name,
					UID:  string(creator.UID),
				},
			},
			ResourceSelector: iamv1alpha1.ResourceSelector{
				ResourceRef: &iamv1alpha1.ResourceReference{
					APIGroup: "notes.miloapis.com",
					Kind:     noteResource.GetNoteKind(),
					Name:     noteResource.GetName(),
					UID:      string(noteResource.GetUID()),
				},
			},
		},
	}

	// Set owner reference for automatic garbage collection
	// This works because:
	// - For Note: PolicyBinding is in the same namespace as the Note
	// - For ClusterNote: Both are cluster-scoped resources
	if err := controllerutil.SetOwnerReference(noteResource, policyBinding, scheme); err != nil {
		return false, "", fmt.Errorf("failed to set owner reference on PolicyBinding: %w", err)
	}

	log.Info("Creating creator PolicyBinding", "policyBinding", bindingName, "namespace", policyBindingNamespace, "user", creator.Name)
	if err := c.Create(ctx, policyBinding); err != nil {
		return false, "", fmt.Errorf("failed to create creator PolicyBinding: %w", err)
	}

	return false, "Waiting for PolicyBinding to become ready", nil
}

// isPolicyBindingReady checks if a PolicyBinding is ready
func isPolicyBindingReady(binding *iamv1alpha1.PolicyBinding) (bool, string, error) {
	for _, condition := range binding.Status.Conditions {
		if condition.Type == "Ready" {
			if condition.Status == metav1.ConditionTrue {
				return true, "", nil
			}
			return false, fmt.Sprintf("PolicyBinding not ready: %s", condition.Message), nil
		}
	}
	return false, "Waiting for PolicyBinding to be reconciled", nil
}
