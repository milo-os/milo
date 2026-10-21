package resourcemanager

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"go.miloapis.com/milo/internal/controllers/projectpurge"
	infra "go.miloapis.com/milo/pkg/apis/infrastructure/v1alpha1"
	rm "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// These hooks pause controller-runtime requests before normal API operations.
// All fixtures, concurrent mutations and assertions use Kubernetes APIs.
type reclaimClient struct {
	client.Client
	get    func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error
	delete func(context.Context, client.Object, ...client.DeleteOption) error
	patch  func(context.Context, client.Object, client.Patch, ...client.PatchOption) error
}

func (c reclaimClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if c.get != nil {
		return c.get(ctx, key, obj, opts...)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}
func (c reclaimClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if c.delete != nil {
		return c.delete(ctx, obj, opts...)
	}
	return c.Client.Delete(ctx, obj, opts...)
}
func (c reclaimClient) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
	if c.patch != nil {
		return c.patch(ctx, obj, p, opts...)
	}
	return c.Client.Patch(ctx, obj, p, opts...)
}

func TestProjectReclamationAPI(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("KUBEBUILDER_ASSETS required in CI")
		}
		t.Skip("set KUBEBUILDER_ASSETS to run API reclamation tests")
	}
	env := &envtest.Environment{CRDDirectoryPaths: []string{
		filepath.Join("..", "..", "..", "config", "crd", "bases", "resourcemanager", "resourcemanager.miloapis.com_projects.yaml"),
		filepath.Join("..", "..", "..", "config", "crd", "bases", "infrastructure", "infrastructure.miloapis.com_projectcontrolplanes.yaml"),
	}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })
	scheme := runtime.NewScheme()
	require.NoError(t, rm.AddToScheme(scheme))
	require.NoError(t, infra.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()
	fixture := func(t *testing.T, extraFinalizers ...string) (*rm.Project, *infra.ProjectControlPlane) {
		t.Helper()
		p := &rm.Project{ObjectMeta: metav1.ObjectMeta{GenerateName: "reclaim-", Finalizers: append([]string{projectFinalizer}, extraFinalizers...)}, Spec: rm.ProjectSpec{OwnerRef: rm.OwnerReference{Kind: "Organization", Name: "test"}}}
		require.NoError(t, c.Create(ctx, p))
		p.Status.Conditions = []metav1.Condition{{Type: rm.ProjectResourceCleanup, Status: metav1.ConditionTrue, Reason: rm.ProjectCleanupAwaitingCompletionReason, Message: "test", LastTransitionTime: metav1.Now()}}
		require.NoError(t, c.Status().Update(ctx, p))
		require.NoError(t, c.Delete(ctx, p))
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(p), p))
		pcp := &infra.ProjectControlPlane{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Labels: map[string]string{rm.ProjectUIDLabel: string(p.UID)}}}
		require.NoError(t, c.Create(ctx, pcp))
		return p, pcp
	}
	markComplete := func(t *testing.T, p *rm.Project) {
		t.Helper()
		p.Status.Conditions = []metav1.Condition{{Type: rm.ProjectResourceCleanup, Status: metav1.ConditionFalse, Reason: rm.ProjectCleanupCompleteReason, Message: "test", LastTransitionTime: metav1.Now()}}
		require.NoError(t, c.Status().Update(ctx, p))
	}
	retained := func(t *testing.T, p *rm.Project) {
		t.Helper()
		got := &rm.Project{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(p), got))
		require.Equal(t, p.UID, got.UID)
		require.Contains(t, got.Finalizers, projectFinalizer)
	}
	// An empty project API response drives the real controller's existing purge
	// completion branch. Root Project and PCP operations still use envtest HTTPS.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"NamespaceList","metadata":{},"items":[]}`))
	}))
	defer empty.Close()
	controller := func(ic client.Client) *ProjectController {
		return &ProjectController{ControlPlaneClient: c, ControlPlaneReader: c, InfraClient: ic, InfraReader: ic, BaseConfig: &rest.Config{Host: empty.URL}, Purger: projectpurge.New()}
	}

	t.Run("reconcile waits for PCP absence", func(t *testing.T) {
		p, pcp := fixture(t)
		pcp.Finalizers = []string{"test.example/hold"}
		require.NoError(t, c.Update(ctx, pcp))
		r := controller(c)
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}
		result, err := r.Reconcile(ctx, req)
		require.NoError(t, err)
		require.Positive(t, result.RequeueAfter)
		retained(t, p)
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(pcp), pcp))
		require.False(t, pcp.DeletionTimestamp.IsZero())
		result, err = r.Reconcile(ctx, req)
		require.NoError(t, err)
		require.Positive(t, result.RequeueAfter)
		retained(t, p)
		pcp.Finalizers = nil
		require.NoError(t, c.Update(ctx, pcp))
		// Reclamation must not call the removed control plane again.
		r.BaseConfig = &rest.Config{Host: "http://127.0.0.1:1"}
		_, err = r.Reconcile(ctx, req)
		require.NoError(t, err)
		require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(p), &rm.Project{})))
	})
	t.Run("cleanup status conflict prevents PCP delete", func(t *testing.T) {
		p, pcp := fixture(t)
		r := controller(c)
		// Return an older root snapshot while a different client changes its RV.
		r.ControlPlaneClient = reclaimClient{Client: c, get: func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if err := c.Get(ctx, key, obj, opts...); err != nil {
				return err
			}
			current := &rm.Project{}
			require.NoError(t, c.Get(ctx, key, current))
			current.Annotations = map[string]string{"test.example/race": "status"}
			require.NoError(t, c.Update(ctx, current))
			return nil
		}}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
		require.True(t, apierrors.IsConflict(err), "%v", err)
		retained(t, p)
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(pcp), pcp))
		require.True(t, pcp.DeletionTimestamp.IsZero())
	})
	t.Run("successful patch preserves other root finalizers", func(t *testing.T) {
		p, _ := fixture(t, "test.example/other")
		markComplete(t, p)
		require.NoError(t, controller(c).removeProjectFinalizer(ctx, p))
		got := &rm.Project{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(p), got))
		require.Equal(t, []string{"test.example/other"}, got.Finalizers)
	})
	t.Run("mismatched or missing binding blocks reconcile", func(t *testing.T) {
		for _, label := range []string{"", "different-project-uid"} {
			p, pcp := fixture(t)
			pcp.Labels[rm.ProjectUIDLabel] = label
			require.NoError(t, c.Update(ctx, pcp))
			_, err := controller(c).Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
			require.Error(t, err)
			retained(t, p)
			got := &infra.ProjectControlPlane{}
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(pcp), got))
			require.True(t, got.DeletionTimestamp.IsZero())
			require.Equal(t, pcp.ResourceVersion, got.ResourceVersion)
		}
	})
	t.Run("GET and DELETE failures retain root", func(t *testing.T) {
		for _, op := range []string{"get", "delete"} {
			p, _ := fixture(t)
			failure := errors.New("API unavailable")
			hooked := reclaimClient{Client: c}
			if op == "get" {
				hooked.get = func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error { return failure }
			} else {
				hooked.delete = func(context.Context, client.Object, ...client.DeleteOption) error { return failure }
			}
			_, err := controller(hooked).Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
			require.ErrorIs(t, err, failure)
			retained(t, p)
		}
	})
	t.Run("cached absence cannot release root", func(t *testing.T) {
		p, pcp := fixture(t)
		pcp.Labels[rm.ProjectUIDLabel] = "replacement-owner"
		require.NoError(t, c.Update(ctx, pcp))
		cached := reclaimClient{Client: c, get: func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
			return apierrors.NewNotFound(schema.GroupResource{Group: "infrastructure.miloapis.com", Resource: "projectcontrolplanes"}, p.Name)
		}}
		r := controller(cached)
		r.InfraReader = c
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
		require.Error(t, err)
		retained(t, p)
	})
	t.Run("missing API readers fail closed", func(t *testing.T) {
		p, _ := fixture(t)
		r := controller(c)
		r.InfraReader = nil
		_, err := r.reclaimProjectControlPlane(ctx, p)
		require.Error(t, err)
		r.ControlPlaneReader = nil
		require.Error(t, r.removeProjectFinalizer(ctx, p))
		retained(t, p)
	})
	t.Run("PCP intervening update conflicts", func(t *testing.T) {
		p, pcp := fixture(t)
		hooked := reclaimClient{Client: c, delete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			pcp.Labels[rm.ProjectUIDLabel] = "changed-owner"
			require.NoError(t, c.Update(ctx, pcp))
			return c.Delete(ctx, obj, opts...)
		}}
		_, err := controller(hooked).Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
		require.True(t, apierrors.IsConflict(err), "%v", err)
		retained(t, p)
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(pcp), pcp))
		require.True(t, pcp.DeletionTimestamp.IsZero())
	})
	t.Run("PCP same-name replacement survives stale delete", func(t *testing.T) {
		p, pcp := fixture(t)
		oldUID := pcp.UID
		hooked := reclaimClient{Client: c, delete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			options := &client.DeleteOptions{}
			options.ApplyOptions(opts)
			require.NotNil(t, options.Preconditions)
			require.Equal(t, oldUID, *options.Preconditions.UID)
			require.Equal(t, pcp.ResourceVersion, *options.Preconditions.ResourceVersion)
			require.NoError(t, c.Delete(ctx, pcp))
			pcp = &infra.ProjectControlPlane{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Labels: map[string]string{rm.ProjectUIDLabel: string(p.UID)}}}
			require.NoError(t, c.Create(ctx, pcp))
			require.NotEqual(t, oldUID, pcp.UID)
			return c.Delete(ctx, obj, opts...)
		}}
		_, err := controller(hooked).Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
		require.True(t, apierrors.IsConflict(err), "%v", err)
		retained(t, p)
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(pcp), pcp))
		require.True(t, pcp.DeletionTimestamp.IsZero())
	})
	t.Run("root concurrent metadata changes conflict", func(t *testing.T) {
		p, _ := fixture(t)
		markComplete(t, p)
		hooked := reclaimClient{Client: c, patch: func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			current := &rm.Project{}
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(p), current))
			// A concurrent metadata write must not be overwritten by a stale patch.
			current.Annotations = map[string]string{"test.example/concurrent": "update"}
			require.NoError(t, c.Update(ctx, current))
			return c.Patch(ctx, obj, patch, opts...)
		}}
		r := &ProjectController{ControlPlaneClient: hooked, ControlPlaneReader: c}
		require.True(t, apierrors.IsConflict(r.removeProjectFinalizer(ctx, p)))
		retained(t, p)
		r.ControlPlaneClient = c
		require.NoError(t, r.removeProjectFinalizer(ctx, p))
		require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(p), &rm.Project{})))
	})
	t.Run("root replacement before GET and before PATCH is protected", func(t *testing.T) {
		for _, beforePatch := range []bool{false, true} {
			p, _ := fixture(t)
			markComplete(t, p)
			replacement := &rm.Project{}
			replace := func() {
				old := &rm.Project{}
				require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(p), old))
				old.Finalizers = nil
				require.NoError(t, c.Update(ctx, old))
				replacement = &rm.Project{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Finalizers: []string{projectFinalizer}}, Spec: p.Spec}
				require.NoError(t, c.Create(ctx, replacement))
				require.NoError(t, c.Delete(ctx, replacement))
				require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(p), replacement))
				require.NotEqual(t, p.UID, replacement.UID)
			}
			r := &ProjectController{ControlPlaneClient: c, ControlPlaneReader: c}
			if beforePatch {
				r.ControlPlaneClient = reclaimClient{Client: c, patch: func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					replace()
					return c.Patch(ctx, obj, patch, opts...)
				}}
			} else {
				replace()
			}
			require.Error(t, r.removeProjectFinalizer(ctx, p))
			retained(t, replacement)
		}
	})
	t.Run("fresh root completion controls reclamation and finalizer removal", func(t *testing.T) {
		for _, removeCondition := range []bool{false, true} {
			p, pcp := fixture(t)
			markComplete(t, p)
			cached := p.DeepCopy()
			if removeCondition {
				p.Status.Conditions = nil
			} else {
				p.Status.Conditions[0].Status = metav1.ConditionTrue
				p.Status.Conditions[0].Reason = rm.ProjectCleanupAwaitingCompletionReason
			}
			require.NoError(t, c.Status().Update(ctx, p))
			r := controller(c)
			r.ControlPlaneClient = reclaimClient{Client: c, get: func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				*obj.(*rm.Project) = *cached.DeepCopy()
				return nil
			}}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
			require.ErrorContains(t, err, "cleanup is not complete")
			retained(t, p)
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(pcp), pcp))
			require.True(t, pcp.DeletionTimestamp.IsZero())
			require.ErrorContains(t, r.removeProjectFinalizer(ctx, cached), "cleanup is not complete")
			retained(t, p)
		}
	})
	t.Run("completion changed after absence read retains finalizer", func(t *testing.T) {
		p, pcp := fixture(t)
		markComplete(t, p)
		require.NoError(t, c.Delete(ctx, pcp))
		r := controller(c)
		r.InfraReader = reclaimClient{Client: c, get: func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			err := c.Get(ctx, key, obj, opts...)
			require.True(t, apierrors.IsNotFound(err))
			p.Status.Conditions = nil
			require.NoError(t, c.Status().Update(ctx, p))
			return err
		}}
		_, err := r.finishProjectReclamation(ctx, p)
		require.ErrorContains(t, err, "cleanup is not complete")
		retained(t, p)
	})
	t.Run("nil infrastructure preserves existing deployment mode", func(t *testing.T) {
		p, _ := fixture(t)
		gone, err := (&ProjectController{}).reclaimProjectControlPlane(ctx, p)
		require.NoError(t, err)
		require.True(t, gone)
	})
	t.Run("invalid caller snapshots rejected", func(t *testing.T) {
		p, _ := fixture(t)
		p.UID = types.UID("")
		_, err := controller(c).reclaimProjectControlPlane(ctx, p)
		require.Error(t, err)
		require.Error(t, controller(c).removeProjectFinalizer(ctx, p))
	})
}
