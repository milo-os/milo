package milo_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/pkg/multicluster-runtime/milo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

// This runs two leader-electing replicas against a real envtest API server.
// A proxy makes one Ready project's List/Watch unavailable. Both replicas must
// engage a healthy project, then recover the failed project without restarting.
// It proves provider behavior, not Milo admission, Flux or application traffic.
func TestProviderUnavailableProjectAndReplicaRecovery(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("KUBEBUILDER_ASSETS must be set in CI")
		}
		t.Skip("KUBEBUILDER_ASSETS is not set")
	}
	env := &envtest.Environment{}
	env.CRDs = append(env.CRDs, projectCRD())
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	unavailable := readyProject()
	unavailable.SetName("a-unavailable")
	healthy := readyProject()
	healthy.SetName("b-healthy")
	require.NoError(t, server.Create(ctx, unavailable))
	require.NoError(t, server.Create(ctx, healthy))
	backendURL, err := url.Parse(cfg.Host)
	require.NoError(t, err)
	transport, err := rest.TransportFor(cfg)
	require.NoError(t, err)
	reverse := httputil.NewSingleHostReverseProxy(backendURL)
	reverse.Transport = transport
	var available atomic.Bool
	var failedLists atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/apis/resourcemanager.miloapis.com/v1alpha1/projects/"
		tail := strings.TrimPrefix(r.URL.Path, prefix)
		parts := strings.SplitN(tail, "/control-plane", 2)
		if len(parts) != 2 {
			http.Error(w, "invalid project path", 400)
			return
		}
		if parts[0] == "a-unavailable" && !available.Load() {
			failedLists.Add(1)
			http.Error(w, "project unavailable", http.StatusServiceUnavailable)
			return
		}
		r.URL.Path = parts[1]
		r.URL.RawPath = ""
		reverse.ServeHTTP(w, r)
	}))
	t.Cleanup(proxy.Close)
	projectConfig := &rest.Config{Host: proxy.URL}
	providers := make([]*milo.Provider, 0, 2)
	managers := make([]mcmanager.Manager, 0, 2)
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		local, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0",
			LeaderElection: true, LeaderElectionID: "provider-replica-test", LeaderElectionNamespace: "default",
			LeaseDuration: ptr.To(3 * time.Second), RenewDeadline: ptr.To(2 * time.Second), RetryPeriod: ptr.To(500 * time.Millisecond),
			LeaderElectionReleaseOnCancel: true,
			Controller:                    ctrlconfig.Controller{SkipNameValidation: ptr.To(true)},
		})
		require.NoError(t, err)
		provider, err := milo.New(local, milo.Options{
			ControllerName: fmt.Sprintf("availability-provider-%d", i), ProjectRestConfig: projectConfig,
			ProjectCacheSyncTimeout: 500 * time.Millisecond,
			ClusterOptions: []cluster.Option{func(o *cluster.Options) {
				o.Scheme = scheme
				// Known native mappings avoid making the discovery endpoint part of this
				// cache-sync test; List/Watch still goes through the real API server.
				o.MapperProvider = func(_ *rest.Config, _ *http.Client) (meta.RESTMapper, error) {
					mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{corev1.SchemeGroupVersion})
					mapper.Add(corev1.SchemeGroupVersion.WithKind("ConfigMap"), meta.RESTScopeNamespace)
					return mapper, nil
				}
				o.Cache = cache.Options{Scheme: scheme, ReaderFailOnMissingInformer: true}
			}},
		})
		require.NoError(t, err)
		require.NoError(t, provider.IndexField(ctx, &corev1.ConfigMap{}, "apps-test-name", func(o client.Object) []string { return []string{o.GetName()} }))
		manager, err := mcmanager.WithMultiCluster(local, milo.WithoutAutoStart(provider))
		require.NoError(t, err)
		require.NoError(t, milo.EngageAlways(manager, provider))
		providers = append(providers, provider)
		managers = append(managers, manager)
		go func() { done <- manager.Start(ctx) }()
	}
	t.Cleanup(func() {
		cancel()
		for range managers {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("manager failed to stop")
			}
		}
	})
	require.Eventually(t, func() bool {
		for _, p := range providers {
			if _, err := p.Get(ctx, "b-healthy"); err != nil {
				return false
			}
		}
		return failedLists.Load() > 0
	}, 15*time.Second, 50*time.Millisecond, "unavailable project prevented healthy engagement on either replica")
	for _, p := range providers {
		_, err := p.Get(ctx, "a-unavailable")
		require.Error(t, err)
	}
	available.Store(true)
	require.Eventually(t, func() bool {
		for _, p := range providers {
			if _, err := p.Get(ctx, "a-unavailable"); err != nil {
				return false
			}
		}
		return true
	}, 15*time.Second, 50*time.Millisecond, "project did not reengage after List/Watch recovered")
	// A successfully synced cache must outlive its synchronization context.
	probe := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "after-recovery", Namespace: "default"}}
	require.NoError(t, server.Create(ctx, probe))
	require.Eventually(t, func() bool {
		for _, p := range providers {
			cl, err := p.Get(ctx, "a-unavailable")
			if err != nil {
				return false
			}
			if err := cl.GetCache().Get(ctx, client.ObjectKeyFromObject(probe), &corev1.ConfigMap{}); err != nil {
				return false
			}
		}
		return true
	}, 10*time.Second, 50*time.Millisecond, "recovered caches did not continue observing events")
	// Once engaged, NotReady must disengage, then Ready must create a new cache.
	require.NoError(t, server.Get(ctx, client.ObjectKeyFromObject(unavailable), unavailable))
	setProjectReady(t, unavailable, "False")
	require.NoError(t, server.Update(ctx, unavailable))
	require.Eventually(t, func() bool {
		for _, p := range providers {
			if _, err := p.Get(ctx, "a-unavailable"); err == nil {
				return false
			}
		}
		return true
	}, 10*time.Second, 50*time.Millisecond)
	require.NoError(t, server.Get(ctx, client.ObjectKeyFromObject(unavailable), unavailable))
	setProjectReady(t, unavailable, "True")
	require.NoError(t, server.Update(ctx, unavailable))
	require.Eventually(t, func() bool {
		for _, p := range providers {
			if _, err := p.Get(ctx, "a-unavailable"); err != nil {
				return false
			}
		}
		return true
	}, 10*time.Second, 50*time.Millisecond)
}

func setProjectReady(t *testing.T, project *unstructured.Unstructured, value string) {
	t.Helper()
	conditions, found, err := unstructured.NestedSlice(project.Object, "status", "conditions")
	require.NoError(t, err)
	require.True(t, found)
	conditions[0].(map[string]interface{})["status"] = value
	require.NoError(t, unstructured.SetNestedSlice(project.Object, conditions, "status", "conditions"))
}
