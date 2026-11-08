package iam

import (
	"context"
	"errors"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

const (
	// targetStoreWindow is how long after a binding's creation its target can still be stored. The API server stamps
	// a new object's uid before admission, and the Project webhook writes the creator's binding inside that
	// admission, so the binding can exist before its target. The target is stored within its create request's timeout
	// (60 s, the API server's default) or never; one more timeout covers clock error between the API server and this
	// controller and a write that commits after its request answered 504.
	targetStoreWindow = 120 * time.Second
	// targetPollInterval is how often an absent target is read again inside the window.
	targetPollInterval = time.Second
)

// errTargetNamespace is a resourceRef that names a namespaced kind without its namespace.
var errTargetNamespace = errors.New("resourceRef names a namespaced kind without a namespace")

// PolicyBindingAdopter makes a PolicyBinding a dependent of the one object its resourceRef names, so the garbage
// collector deletes the binding with that object.
type PolicyBindingAdopter struct {
	Client client.Client
	// APIReader reads the target from the API server, not the cache, so a re-created target is never mistaken for
	// the one the binding names.
	APIReader client.Reader
	// Mapper resolves the kind a resourceRef names, so any kind the API server serves can be a binding's owner.
	Mapper        meta.RESTMapper
	Scheme        *runtime.Scheme
	EventRecorder record.EventRecorder
	// now is the clock the target's creation window is measured with; nil means time.Now.
	now func() time.Time
}

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=policybindings,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users;userpreferences;userinvitations,verbs=get
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects;organizations;organizationmemberships,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile adds a binding's target as its owner once the target is stored with the uid the binding names.
func (r *PolicyBindingAdopter) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pb iamv1alpha1.PolicyBinding
	if err := r.Client.Get(ctx, req.NamespacedName, &pb); err != nil || !adoptable(&pb) {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return r.adoptLiveTarget(ctx, &pb)
}

// adoptable reports whether the binding names one target and has no owner. A binding with any owner is left alone:
// the garbage collector keeps a dependent while any owner exists, so a second owner would extend its life.
func adoptable(pb *iamv1alpha1.PolicyBinding) bool {
	return pb.Spec.ResourceSelector.ResourceRef != nil && pb.DeletionTimestamp == nil && len(pb.OwnerReferences) == 0
}

func (r *PolicyBindingAdopter) adoptLiveTarget(ctx context.Context, pb *iamv1alpha1.PolicyBinding) (ctrl.Result, error) {
	target, err := r.liveTarget(ctx, pb.Spec.ResourceSelector.ResourceRef)
	switch {
	case apierrors.IsNotFound(err) || meta.IsNoMatchError(err):
		return r.pollWhileStorable(pb), nil
	case errors.Is(err, errTargetNamespace):
		r.EventRecorder.Event(pb, corev1.EventTypeWarning, "OwnerNotAllowed", err.Error())
		return ctrl.Result{}, nil
	case err != nil:
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, r.adoptOnto(ctx, pb, target)
}

// liveTarget reads the target's metadata from the API server, through the REST mapper, so any served kind works.
func (r *PolicyBindingAdopter) liveTarget(ctx context.Context, ref *iamv1alpha1.ResourceReference) (*metav1.PartialObjectMetadata, error) {
	mapping, err := r.Mapper.RESTMapping(schema.GroupKind{Group: ref.APIGroup, Kind: ref.Kind})
	if err != nil {
		return nil, err
	}
	key := client.ObjectKey{Name: ref.Name}
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		if ref.Namespace == "" {
			return nil, errTargetNamespace
		}
		key.Namespace = ref.Namespace
	}
	target := &metav1.PartialObjectMetadata{}
	target.SetGroupVersionKind(mapping.GroupVersionKind)
	return target, r.APIReader.Get(ctx, key, target)
}

// adoptOnto sets the target as the binding's owner when it is the object the binding names and is not being deleted.
// A deleting target is never adopted onto: an orphaning delete has just stripped its dependents' references.
func (r *PolicyBindingAdopter) adoptOnto(ctx context.Context, pb *iamv1alpha1.PolicyBinding, target *metav1.PartialObjectMetadata) error {
	if string(target.GetUID()) != pb.Spec.ResourceSelector.ResourceRef.UID || target.GetDeletionTimestamp() != nil {
		return nil
	}
	base := pb.DeepCopy()
	if err := controllerutil.SetOwnerReference(target, pb, r.Scheme); err != nil {
		r.EventRecorder.Event(pb, corev1.EventTypeWarning, "OwnerNotAllowed", err.Error())
		return nil
	}
	return r.Client.Patch(ctx, pb, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// pollWhileStorable reads an absent target again while it can still be stored, and leaves the binding after that.
func (r *PolicyBindingAdopter) pollWhileStorable(pb *iamv1alpha1.PolicyBinding) ctrl.Result {
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	if now().Sub(pb.CreationTimestamp.Time) < targetStoreWindow {
		return ctrl.Result{RequeueAfter: targetPollInterval}
	}
	return ctrl.Result{}
}

// ownerReferencesChanged passes an update that changes the owner references, so a removed reference is set again.
var ownerReferencesChanged = predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
	return !equality.Semantic.DeepEqual(e.ObjectOld.GetOwnerReferences(), e.ObjectNew.GetOwnerReferences())
}}

// SetupWithManager sets up the controller with the Manager. A status-only update does not reconcile: the provider
// writes a binding's status on every reconcile of its own.
func (r *PolicyBindingAdopter) SetupWithManager(mgr ctrl.Manager) error {
	r.EventRecorder = mgr.GetEventRecorderFor("policybinding-adopter")
	return ctrl.NewControllerManagedBy(mgr).
		For(&iamv1alpha1.PolicyBinding{},
			builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, ownerReferencesChanged))).
		Named("policybinding-adopter").
		Complete(r)
}
