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

var clusterNoteLog = logf.Log.WithName("clusternote-resource")

func SetupClusterNoteWebhooksWithManager(mgr ctrl.Manager, mcMgr mcmanager.Manager) error {
	clusterNoteLog.Info("Setting up notes.miloapis.com clusternote webhooks")
	return ctrl.NewWebhookManagedBy(mgr, &notesv1alpha1.ClusterNote{}).
		WithDefaulter(&ClusterNoteMutator{
			Client:         mgr.GetClient(),
			Scheme:         mgr.GetScheme(),
			RESTMapper:     mgr.GetRESTMapper(),
			ClusterManager: mcMgr,
		}).
		WithValidator(&ClusterNoteValidator{
			Client: mgr.GetClient(),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-notes-miloapis-com-v1alpha1-clusternote,mutating=true,failurePolicy=fail,sideEffects=None,groups=notes.miloapis.com,resources=clusternotes,verbs=create,versions=v1alpha1,name=mclusternote.notes.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

type ClusterNoteMutator struct {
	Client         client.Client
	Scheme         *runtime.Scheme
	RESTMapper     meta.RESTMapper
	ClusterManager mcmanager.Manager
}

var _ admission.Defaulter[*notesv1alpha1.ClusterNote] = &ClusterNoteMutator{}

func (m *ClusterNoteMutator) Default(ctx context.Context, clusterNote *notesv1alpha1.ClusterNote) error {
	clusterNoteLog.Info("Defaulting ClusterNote", "name", clusterNote.Name)

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get request from context: %w", err)
	}

	creatorUser := &iamv1alpha1.User{}
	if err := m.Client.Get(ctx, client.ObjectKey{Name: string(req.UserInfo.UID)}, creatorUser); err != nil {
		return errors.NewInternalError(fmt.Errorf("failed to get user '%s' from iam.miloapis.com API: %w", string(req.UserInfo.UID), err))
	}

	clusterNote.Spec.CreatorRef = iamv1alpha1.UserReference{
		Name: creatorUser.Name,
	}

	// Set owner reference to the subject resource for automatic garbage collection
	if err := m.setSubjectOwnerReference(ctx, clusterNote); err != nil {
		clusterNoteLog.Error(err, "Failed to set owner reference to subject", "clusternote", clusterNote.Name)
		return errors.NewInternalError(fmt.Errorf("failed to set owner reference to subject: %w", err))
	}

	return nil
}

// setSubjectOwnerReference sets the owner reference to the subject resource if it's cluster-scoped.
// The cluster context is expected to be injected by the ClusterAwareServer wrapper.
func (m *ClusterNoteMutator) setSubjectOwnerReference(ctx context.Context, clusterNote *notesv1alpha1.ClusterNote) error {
	// ClusterNote can only have owner references to other cluster-scoped resources
	if clusterNote.Spec.SubjectRef.Namespace != "" {
		return nil // Subject is namespaced, can't set owner reference on cluster-scoped resource
	}

	// Resolve the GVK using REST mapper to discover the correct API version
	groupKind := schema.GroupKind{
		Group: clusterNote.Spec.SubjectRef.APIGroup,
		Kind:  clusterNote.Spec.SubjectRef.Kind,
	}

	mapping, err := m.RESTMapper.RESTMapping(groupKind)
	if err != nil {
		return fmt.Errorf("failed to get REST mapping for %s: %w", groupKind, err)
	}

	key := types.NamespacedName{
		Name: clusterNote.Spec.SubjectRef.Name,
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
			clusterNoteLog.V(1).Info("Using project control plane client", "cluster", clusterName)
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

	return controllerutil.SetOwnerReference(subject, clusterNote, m.Scheme)
}

// +kubebuilder:webhook:path=/validate-notes-miloapis-com-v1alpha1-clusternote,mutating=false,failurePolicy=fail,sideEffects=None,groups=notes.miloapis.com,resources=clusternotes,verbs=create;update,versions=v1alpha1,name=vclusternote.notes.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

type ClusterNoteValidator struct {
	Client client.Client
}

var _ admission.Validator[*notesv1alpha1.ClusterNote] = &ClusterNoteValidator{}

func (v *ClusterNoteValidator) ValidateCreate(ctx context.Context, clusterNote *notesv1alpha1.ClusterNote) (admission.Warnings, error) {
	clusterNoteLog.Info("Validating ClusterNote creation", "name", clusterNote.Name)

	return nil, v.validateClusterNote(ctx, clusterNote, false)
}

func (v *ClusterNoteValidator) ValidateUpdate(ctx context.Context, oldClusterNote, clusterNote *notesv1alpha1.ClusterNote) (admission.Warnings, error) {
	clusterNoteLog.Info("Validating ClusterNote update", "name", clusterNote.Name)

	skipNextActionTimeValidation := oldClusterNote.Spec.NextActionTime != nil &&
		clusterNote.Spec.NextActionTime != nil &&
		oldClusterNote.Spec.NextActionTime.Time.Equal(clusterNote.Spec.NextActionTime.Time)

	return nil, v.validateClusterNote(ctx, clusterNote, skipNextActionTimeValidation)
}

func (v *ClusterNoteValidator) ValidateDelete(ctx context.Context, obj *notesv1alpha1.ClusterNote) (admission.Warnings, error) {
	return nil, nil
}

func (v *ClusterNoteValidator) validateClusterNote(ctx context.Context, clusterNote *notesv1alpha1.ClusterNote, skipNextActionTimeValidation bool) error {
	var allErrs field.ErrorList

	// Validate that the subject reference is cluster-scoped (no namespace)
	if clusterNote.Spec.SubjectRef.Namespace != "" {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "subjectRef", "namespace"),
			clusterNote.Spec.SubjectRef.Namespace,
			"ClusterNote can only reference cluster-scoped resources (subjectRef.namespace must be empty)",
		))
	}

	if !skipNextActionTimeValidation && clusterNote.Spec.NextActionTime != nil {
		if clusterNote.Spec.NextActionTime.Time.Before(time.Now()) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "nextActionTime"), clusterNote.Spec.NextActionTime, "nextActionTime cannot be in the past"))
		}
	}

	if len(allErrs) == 0 {
		return nil
	}
	return errors.NewInvalid(notesv1alpha1.SchemeGroupVersion.WithKind("ClusterNote").GroupKind(), clusterNote.Name, allErrs)
}
