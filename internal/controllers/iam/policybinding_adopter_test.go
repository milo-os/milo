package iam

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// bindingCreated is the binding's creationTimestamp in these cases; the window is measured from it.
var bindingCreated = time.Date(2026, 10, 20, 18, 0, 0, 0, time.UTC)

// pollCase is one reconcile of a binding whose target the reader answers for.
type pollCase struct {
	age         time.Duration          // the adopter's clock, after the binding's creation
	absentReads int                    // how many reads of the target answer NotFound before it is found
	readErr     error                  // an error every read of the target returns instead
	unseenKinds int                    // how many mappings of the target's kind answer no match before the kind is known
	beforePatch func(client.WithWatch) // runs once, just before the adopter's patch reaches the API server
}

// unseenFirst is a REST mapper that has not yet seen the target's kind for its first misses lookups, as the shared
// DeferredDiscoveryRESTMapper answers until its next reset.
type unseenFirst struct {
	meta.RESTMapper
	misses int
}

func (m *unseenFirst) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	if m.misses > 0 {
		m.misses--
		return nil, &meta.NoKindMatchError{GroupKind: gk}
	}
	return m.RESTMapper.RESTMapping(gk, versions...)
}

// TestPolicyBindingAdopterWaitsForTheTarget covers rule 4 of the design: a target that is absent when its binding is
// first seen is polled while it can still be stored (the request timeout and a margin, 120 s from the binding's
// creation), and left after that; any other read error is returned, and nothing is written.
func TestPolicyBindingAdopterWaitsForTheTarget(t *testing.T) {
	t.Run("D16 absent at the first read, stored within the window: polled, then adopted", func(t *testing.T) {
		r, c, project := pollEnv(t, pollCase{age: 10 * time.Second, absentReads: 1})
		res, err := r.Reconcile(context.Background(), bindingRequest())
		require.NoError(t, err)
		require.Equal(t, time.Second, res.RequeueAfter, "an absent target inside the window is polled every second")
		require.Empty(t, ownersOf(t, c))
		res, err = r.Reconcile(context.Background(), bindingRequest())
		require.NoError(t, err)
		require.Zero(t, res.RequeueAfter)
		require.Equal(t, []types.UID{project.UID}, ownersOf(t, c))
	})

	t.Run("D20 a kind the mapper has not seen yet counts as absent: polled, then adopted", func(t *testing.T) {
		r, c, project := pollEnv(t, pollCase{age: 10 * time.Second, unseenKinds: 1})
		res, err := r.Reconcile(context.Background(), bindingRequest())
		require.NoError(t, err)
		require.Equal(t, time.Second, res.RequeueAfter)
		require.Empty(t, ownersOf(t, c))
		_, err = r.Reconcile(context.Background(), bindingRequest())
		require.NoError(t, err)
		require.Equal(t, []types.UID{project.UID}, ownersOf(t, c))
	})

	t.Run("D13 an owner written between the read and the patch is kept: the patch conflicts, the retry leaves it", func(t *testing.T) {
		other := metav1.OwnerReference{APIVersion: "v1", Kind: "ConfigMap", Name: "holder", UID: "holder-uid"}
		r, c, _ := pollEnv(t, pollCase{age: 10 * time.Second, beforePatch: func(cl client.WithWatch) {
			var pb iamv1alpha1.PolicyBinding
			require.NoError(t, cl.Get(context.Background(), bindingRequest().NamespacedName, &pb))
			pb.OwnerReferences = append(pb.OwnerReferences, other)
			require.NoError(t, cl.Update(context.Background(), &pb))
		}})
		_, err := r.Reconcile(context.Background(), bindingRequest())
		require.True(t, apierrors.IsConflict(err), "the stale patch conflicts: %v", err)
		_, err = r.Reconcile(context.Background(), bindingRequest())
		require.NoError(t, err)
		require.Equal(t, []types.UID{other.UID}, ownersOf(t, c), "the other owner stays, and no second owner is added")
	})

	t.Run("D3 absent 119 s after the binding's creation: still polled", func(t *testing.T) {
		r, c, _ := pollEnv(t, pollCase{age: 119 * time.Second, absentReads: 1 << 20})
		res, err := r.Reconcile(context.Background(), bindingRequest())
		require.NoError(t, err)
		require.Equal(t, time.Second, res.RequeueAfter)
		require.Empty(t, ownersOf(t, c))
	})

	t.Run("D3 absent 121 s after the binding's creation: left, not polled", func(t *testing.T) {
		r, c, _ := pollEnv(t, pollCase{age: 121 * time.Second, absentReads: 1 << 20})
		res, err := r.Reconcile(context.Background(), bindingRequest())
		require.NoError(t, err)
		require.Equal(t, ctrl.Result{}, res)
		require.Empty(t, ownersOf(t, c))
	})

	t.Run("D14 a failed read of the target is returned and writes nothing", func(t *testing.T) {
		readErr := apierrors.NewInternalError(context.DeadlineExceeded)
		r, c, _ := pollEnv(t, pollCase{age: 10 * time.Second, readErr: readErr})
		_, err := r.Reconcile(context.Background(), bindingRequest())
		require.Error(t, err)
		require.Empty(t, ownersOf(t, c))
	})
}

// pollEnv returns an adopter over a fake API server holding one Project and one binding naming it. The adopter's
// reads of the target, as metadata through the API reader, answer as the case says.
func pollEnv(t *testing.T, pc pollCase) (*PolicyBindingAdopter, client.Client, *resourcemanagerv1alpha1.Project) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, iamv1alpha1.AddToScheme(scheme))
	require.NoError(t, resourcemanagerv1alpha1.AddToScheme(scheme))
	project := &resourcemanagerv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "p", UID: "project-uid"}}
	pb := newBinding("pb", iamv1alpha1.ResourceReference{
		APIGroup: resourcemanagerv1alpha1.GroupVersion.Group, Kind: "Project", Name: project.Name, UID: string(project.UID),
	}, nil)
	pb.CreationTimestamp = metav1.NewTime(bindingCreated)
	// The fake's default mapper calls every kind namespaced; a Project is cluster-scoped, as Milo serves it.
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{resourcemanagerv1alpha1.GroupVersion, iamv1alpha1.SchemeGroupVersion})
	mapper.Add(resourcemanagerv1alpha1.GroupVersion.WithKind("Project"), meta.RESTScopeRoot)
	mapper.Add(iamv1alpha1.SchemeGroupVersion.WithKind("PolicyBinding"), meta.RESTScopeNamespace)
	reads, patched := 0, false
	c := fake.NewClientBuilder().WithScheme(scheme).WithRESTMapper(mapper).WithObjects(project, pb).
		WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, isTarget := obj.(*metav1.PartialObjectMetadata); isTarget {
				if pc.readErr != nil {
					return pc.readErr
				}
				if reads++; reads <= pc.absentReads {
					return apierrors.NewNotFound(schema.GroupResource{Group: resourcemanagerv1alpha1.GroupVersion.Group, Resource: "projects"}, key.Name)
				}
			}
			return cl.Get(ctx, key, obj, opts...)
		}, Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if pc.beforePatch != nil && !patched {
				patched = true
				pc.beforePatch(cl)
			}
			return cl.Patch(ctx, obj, patch, opts...)
		}}).Build()
	return &PolicyBindingAdopter{
		Client: c, APIReader: c, Mapper: &unseenFirst{RESTMapper: mapper, misses: pc.unseenKinds}, Scheme: scheme,
		EventRecorder: record.NewFakeRecorder(10),
		now:           func() time.Time { return bindingCreated.Add(pc.age) },
	}, c, project
}

func bindingRequest() ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: adopterNamespace, Name: "pb"}}
}

func ownersOf(t *testing.T, c client.Client) []types.UID {
	t.Helper()
	var pb iamv1alpha1.PolicyBinding
	require.NoError(t, c.Get(context.Background(), bindingRequest().NamespacedName, &pb))
	return ownerUIDs(&pb)
}
