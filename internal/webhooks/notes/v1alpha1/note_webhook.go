package v1alpha1

import (
	"context"
	"fmt"
	"time"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notesv1alpha1 "go.miloapis.com/milo/pkg/apis/notes/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

var noteLog = logf.Log.WithName("note-resource")

func SetupNoteWebhooksWithManager(mgr ctrl.Manager, mcMgr mcmanager.Manager) error {
	noteLog.Info("Setting up notes.miloapis.com note webhooks")
	return ctrl.NewWebhookManagedBy(mgr, &notesv1alpha1.Note{}).
		WithCustomDefaulter(&NoteMutator{
			Client:         mgr.GetClient(),
			Scheme:         mgr.GetScheme(),
			RESTMapper:     mgr.GetRESTMapper(),
			ClusterManager: mcMgr,
		}).
		WithCustomValidator(&NoteValidator{
			Client: mgr.GetClient(),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-notes-miloapis-com-v1alpha1-note,mutating=true,failurePolicy=fail,sideEffects=None,groups=notes.miloapis.com,resources=notes,verbs=create,versions=v1alpha1,name=mnote.notes.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

type NoteMutator struct {
	Client         client.Client
	Scheme         *runtime.Scheme
	RESTMapper     meta.RESTMapper
	ClusterManager mcmanager.Manager
}

var _ admission.CustomDefaulter = &NoteMutator{}

func (m *NoteMutator) Default(ctx context.Context, obj runtime.Object) error {
	note, ok := obj.(*notesv1alpha1.Note)
	if !ok {
		return errors.NewInternalError(fmt.Errorf("failed to cast object to Note"))
	}
	noteLog.Info("Defaulting Note", "name", note.Name, "namespace", note.Namespace)

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get request from context: %w", err)
	}

	creatorUser := &iamv1alpha1.User{}
	if err := m.Client.Get(ctx, client.ObjectKey{Name: string(req.UserInfo.UID)}, creatorUser); err != nil {
		return errors.NewInternalError(fmt.Errorf("failed to get user '%s' from iam.miloapis.com API: %w", string(req.UserInfo.UID), err))
	}

	note.Spec.CreatorRef = iamv1alpha1.UserReference{
		Name: creatorUser.Name,
	}

	// Set owner reference to the subject resource for automatic garbage collection
	if err := m.setSubjectOwnerReference(ctx, note); err != nil {
		noteLog.Error(err, "Failed to set owner reference to subject", "note", note.Name)
		return errors.NewInternalError(fmt.Errorf("failed to set owner reference to subject: %w", err))
	}

	return nil
}

// setSubjectOwnerReference sets the owner reference to the subject resource if it's in the same namespace.
// The cluster context is expected to be injected by the ClusterAwareServer wrapper.
func (m *NoteMutator) setSubjectOwnerReference(ctx context.Context, note *notesv1alpha1.Note) error {
	// Only set owner reference if the subject is in the same namespace
	if note.Spec.SubjectRef.Namespace == "" || note.Spec.SubjectRef.Namespace != note.Namespace {
		return nil
	}

	// Resolve the GVK using REST mapper
	groupKind := schema.GroupKind{
		Group: note.Spec.SubjectRef.APIGroup,
		Kind:  note.Spec.SubjectRef.Kind,
	}

	mapping, err := m.RESTMapper.RESTMapping(groupKind)
	if err != nil {
		return fmt.Errorf("failed to get REST mapping for %s: %w", groupKind, err)
	}

	key := types.NamespacedName{
		Name:      note.Spec.SubjectRef.Name,
		Namespace: note.Spec.SubjectRef.Namespace,
	}

	// Determine which client to use based on cluster context (injected by ClusterAwareServer)
	subjectClient := m.Client

	if m.ClusterManager != nil {
		if clusterName, ok := mccontext.ClusterFrom(ctx); ok && clusterName != "" {
			cluster, err := m.ClusterManager.GetCluster(ctx, clusterName)
			if err != nil {
				return fmt.Errorf("failed to get project control plane %s: %w", clusterName, err)
			}
			subjectClient = cluster.GetClient()
			noteLog.V(1).Info("Using project control plane client", "cluster", clusterName)
		}
	}

	subject := &unstructured.Unstructured{}
	subject.SetGroupVersionKind(mapping.GroupVersionKind)
	if err := subjectClient.Get(ctx, key, subject); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("subject resource not found: %w", err)
		}
		return fmt.Errorf("failed to get subject resource: %w", err)
	}

	return controllerutil.SetOwnerReference(subject, note, m.Scheme)
}

// +kubebuilder:webhook:path=/validate-notes-miloapis-com-v1alpha1-note,mutating=false,failurePolicy=fail,sideEffects=None,groups=notes.miloapis.com,resources=notes,verbs=create;update,versions=v1alpha1,name=vnote.notes.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

type NoteValidator struct {
	Client client.Client
}

var _ admission.CustomValidator = &NoteValidator{}

func (v *NoteValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	note, ok := obj.(*notesv1alpha1.Note)
	if !ok {
		return nil, errors.NewInternalError(fmt.Errorf("failed to cast object to Note"))
	}
	noteLog.Info("Validating Note creation", "name", note.Name, "namespace", note.Namespace)

	return nil, v.validateNote(ctx, note, false)
}

func (v *NoteValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	note, ok := newObj.(*notesv1alpha1.Note)
	if !ok {
		return nil, errors.NewInternalError(fmt.Errorf("failed to cast object to Note"))
	}
	oldNote, ok := oldObj.(*notesv1alpha1.Note)
	if !ok {
		return nil, errors.NewInternalError(fmt.Errorf("failed to cast old object to Note"))
	}
	noteLog.Info("Validating Note update", "name", note.Name, "namespace", note.Namespace)

	skipNextActionTimeValidation := oldNote.Spec.NextActionTime != nil &&
		note.Spec.NextActionTime != nil &&
		oldNote.Spec.NextActionTime.Time.Equal(note.Spec.NextActionTime.Time)

	return nil, v.validateNote(ctx, note, skipNextActionTimeValidation)
}

func (v *NoteValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v *NoteValidator) validateNote(ctx context.Context, note *notesv1alpha1.Note, skipNextActionTimeValidation bool) error {
	var allErrs field.ErrorList

	// Validate that the subject reference is namespaced
	if note.Spec.SubjectRef.Namespace == "" {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "subjectRef", "namespace"),
			note.Spec.SubjectRef.Namespace,
			"Note can only reference namespaced resources (subjectRef.namespace must be set)",
		))
	} else if note.Spec.SubjectRef.Namespace != note.Namespace {
		// Validate that the subject is in the same namespace as the Note
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "subjectRef", "namespace"),
			note.Spec.SubjectRef.Namespace,
			fmt.Sprintf("Note must be in the same namespace as its subject (expected: %s)", note.Namespace),
		))
	}

	if !skipNextActionTimeValidation && note.Spec.NextActionTime != nil {
		if note.Spec.NextActionTime.Time.Before(time.Now()) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "nextActionTime"), note.Spec.NextActionTime, "nextActionTime cannot be in the past"))
		}
	}

	if len(allErrs) == 0 {
		return nil
	}
	return errors.NewInvalid(notesv1alpha1.SchemeGroupVersion.WithKind("Note").GroupKind(), note.Name, allErrs)
}
