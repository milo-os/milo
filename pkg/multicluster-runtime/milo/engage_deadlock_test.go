package milo_test

import (
	"context"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/source"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontroller "sigs.k8s.io/multicluster-runtime/pkg/controller"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.miloapis.com/milo/pkg/multicluster-runtime/milo"
)

const deadlockTestProject = "test-project"

// TestEngageDoesNotDeadlockControllerStartup forces the interleaving a
// production goroutine dump showed. A multicluster controller is starting: it
// holds its own lock while it waits for a root-cluster watch to deliver its
// initial events, and that watch's handler looks a project up through the
// provider. Meanwhile the provider registers a project and engages it, which
// needs the controller's lock. If the provider holds its lock across
// registration, the handler, the controller and the provider wait on each
// other forever.
//
// Two gates make the ordering deterministic: the provider does not finish its
// project cache sync until the handler is running, and the handler does not
// look the project up until the provider has reached that point.
func TestEngageDoesNotDeadlockControllerStartup(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("KUBEBUILDER_ASSETS must be set in CI; this test needs an envtest API server")
		}
		t.Skip("KUBEBUILDER_ASSETS is not set; run setup-envtest to provide an envtest API server")
	}

	env := &envtest.Environment{
		CRDs: []*apiextensionsv1.CustomResourceDefinition{projectCRD()},
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	setup, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	require.NoError(t, setup.Create(ctx, readyProject()))
	require.NoError(t, setup.Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "probe", Namespace: "default"},
	}))

	handlerRunning := make(chan struct{})
	projectCacheSynced := make(chan struct{})

	localMgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller: ctrlconfig.Controller{
			SkipNameValidation: ptr.To(true),
			CacheSyncTimeout:   5 * time.Minute,
		},
	})
	require.NoError(t, err)

	provider, err := milo.New(localMgr, milo.Options{
		ControllerName: "engage-deadlock-test",
		ClusterOptions: []cluster.Option{
			projectClusterAt(cfg, &gate{wait: handlerRunning, release: projectCacheSynced}),
		},
	})
	require.NoError(t, err)

	mcMgr, err := mcmanager.WithMultiCluster(localMgr, milo.WithoutAutoStart(provider))
	require.NoError(t, err)
	require.NoError(t, milo.EngageAlways(mcMgr, provider))

	engaged := make(chan struct{})
	var handlerOnce, engagedOnce sync.Once

	err = mcbuilder.ControllerManagedBy(mcMgr).
		Named("engage-deadlock-fanout").
		WithOptions(mccontroller.Options{SkipNameValidation: ptr.To(true)}).
		For(&corev1.ConfigMap{}, mcbuilder.WithEngageWithProviderClusters(true)).
		WatchesRawSource(source.TypedKind(
			localMgr.GetCache(),
			&corev1.Namespace{},
			handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, _ *corev1.Namespace) []mcreconcile.Request {
				handlerOnce.Do(func() {
					close(handlerRunning)
					<-projectCacheSynced
				})
				_, _ = mcMgr.GetCluster(ctx, deadlockTestProject)
				return nil
			}),
		)).
		Complete(mcreconcile.Func(func(_ context.Context, req mcreconcile.Request) (ctrl.Result, error) {
			if req.ClusterName == deadlockTestProject {
				engagedOnce.Do(func() { close(engaged) })
			}
			return ctrl.Result{}, nil
		}))
	require.NoError(t, err)

	go func() { _ = mcMgr.Start(ctx) }()

	select {
	case <-engaged:
	case <-time.After(60 * time.Second):
		t.Fatal("controller startup and project engagement deadlocked: the project was never reconciled")
	}
}

// gate holds the first project cache sync until wait is closed, then closes
// release so the waiting handler can proceed.
type gate struct {
	wait    <-chan struct{}
	release chan struct{}
	once    sync.Once
}

type gatedCache struct {
	cache.Cache
	gate *gate
}

func (c *gatedCache) WaitForCacheSync(ctx context.Context) bool {
	synced := c.Cache.WaitForCacheSync(ctx)
	c.gate.once.Do(func() {
		select {
		case <-c.gate.wait:
		case <-ctx.Done():
		}
		close(c.gate.release)
	})
	return synced
}

// projectClusterAt points every project cluster at cfg. The provider builds a
// per-project control plane URL that envtest does not serve, so the client,
// cache and REST mapper are built against the envtest API server instead.
func projectClusterAt(cfg *rest.Config, g *gate) cluster.Option {
	return func(o *cluster.Options) {
		o.MapperProvider = func(_ *rest.Config, _ *http.Client) (meta.RESTMapper, error) {
			httpClient, err := rest.HTTPClientFor(cfg)
			if err != nil {
				return nil, err
			}
			return apiutil.NewDynamicRESTMapper(cfg, httpClient)
		}
		o.NewCache = func(_ *rest.Config, opts cache.Options) (cache.Cache, error) {
			c, err := cache.New(cfg, cache.Options{Scheme: opts.Scheme})
			if err != nil {
				return nil, err
			}
			return &gatedCache{Cache: c, gate: g}, nil
		}
		o.NewClient = func(_ *rest.Config, opts client.Options) (client.Client, error) {
			return client.New(cfg, client.Options{Scheme: opts.Scheme, Cache: opts.Cache})
		}
	}
}

func readyProject() *unstructured.Unstructured {
	project := &unstructured.Unstructured{}
	project.SetAPIVersion("resourcemanager.miloapis.com/v1alpha1")
	project.SetKind("Project")
	project.SetName(deadlockTestProject)
	_ = unstructured.SetNestedSlice(project.Object, []interface{}{
		map[string]interface{}{
			"type":               "Ready",
			"status":             "True",
			"reason":             "Ready",
			"message":            "",
			"lastTransitionTime": "2026-01-01T00:00:00Z",
		},
	}, "status", "conditions")
	return project
}

// projectCRD serves Project as a cluster-scoped custom resource with no
// status subresource, so the Ready condition is stored on create.
func projectCRD() *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "projects.resourcemanager.miloapis.com"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "resourcemanager.miloapis.com",
			Scope: apiextensionsv1.ClusterScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   "projects",
				Singular: "project",
				Kind:     "Project",
				ListKind: "ProjectList",
			},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    "v1alpha1",
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type:                   "object",
						XPreserveUnknownFields: ptr.To(true),
					},
				},
			}},
		},
	}
}
