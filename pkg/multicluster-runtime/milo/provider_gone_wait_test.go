package milo_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	"go.miloapis.com/milo/pkg/multicluster-runtime/milo"
)

const (
	stuckProject = "stuck"
	nextProject  = "next"
	// afterGone bounds the time from a waiting Project's removal to the next Project's registration: one poll of the
	// wait (1 s), the local cache seeing the removal, and the next cluster's own sync on envtest.
	afterGone = 5 * time.Second
	// heldFor is how long the next Project must stay unregistered while the first one waits: the one worker is held.
	heldFor = 2 * time.Second
)

// TestRegistrationWaitEndsWhenItsProjectGoes proves that a registration waiting on a cache that never syncs, as an
// unavailable project's cache never does, ends once its Project is deleted or re-created under its name, and stops
// that cache. The provider runs one worker, so until it ends no other Project registers.
func TestRegistrationWaitEndsWhenItsProjectGoes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		recreate bool
	}{
		{name: "deleted"},
		{name: "re-created under its name", recreate: true},
	} {
		t.Run(tc.name, func(t *testing.T) { runGoneWait(t, tc.recreate) })
	}
}

func runGoneWait(t *testing.T, recreate bool) {
	cfg := startEnvtest(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	setup, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	require.NoError(t, setup.Create(ctx, readyProject(stuckProject)))

	waiting, stopped := make(chan struct{}), make(chan struct{})
	provider := startProvider(t, ctx, cfg, scheme, firstStuck(stuckProject, waiting, stopped))
	select {
	case <-waiting:
	case <-time.After(30 * time.Second):
		t.Fatal("the stuck project's registration never began its wait")
	}
	require.NoError(t, setup.Create(ctx, readyProject(nextProject)))
	require.Never(t, registered(ctx, provider, nextProject), heldFor, 100*time.Millisecond,
		"the next project registered while the first one waited: the worker was not held")

	gone := time.Now()
	removeProject(t, ctx, setup, recreate)
	require.Eventually(t, registered(ctx, provider, nextProject), afterGone, 100*time.Millisecond,
		"the next project did not register within %s of the waiting project's removal: the wait did not end", afterGone)
	t.Logf("the next project registered %s after the waiting project's removal", time.Since(gone).Round(time.Millisecond))
	select {
	case <-stopped:
	case <-time.After(afterGone):
		t.Fatal("the removed project's cache was not stopped")
	}
	if recreate {
		require.Eventually(t, registered(ctx, provider, stuckProject), afterGone, 100*time.Millisecond,
			"the re-created project was not registered by a later reconcile")
	}
}

// startProvider runs a provider with one cluster option and its multicluster manager, until ctx ends.
func startProvider(t *testing.T, ctx context.Context, cfg *rest.Config, scheme *runtime.Scheme,
	wrap func(string, cache.Cache) cache.Cache) *milo.Provider {
	t.Helper()
	localMgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             ctrlconfig.Controller{SkipNameValidation: ptr.To(true)},
	})
	require.NoError(t, err)
	provider, err := milo.New(localMgr, milo.Options{
		ControllerName: "gone-wait-test",
		ClusterOptions: []cluster.Option{projectClusterAt(cfg, wrap)},
	})
	require.NoError(t, err)
	mcMgr, err := mcmanager.WithMultiCluster(localMgr, milo.WithoutAutoStart(provider))
	require.NoError(t, err)
	require.NoError(t, milo.EngageAlways(mcMgr, provider))
	go func() { _ = mcMgr.Start(ctx) }()
	return provider
}

// removeProject deletes the stuck Project and, with recreate, creates it again under its name once the API server no
// longer has it, so it carries a new UID.
func removeProject(t *testing.T, ctx context.Context, setup client.Client, recreate bool) {
	t.Helper()
	require.NoError(t, setup.Delete(ctx, readyProject(stuckProject)))
	if !recreate {
		return
	}
	require.Eventually(t, func() bool { return setup.Create(ctx, readyProject(stuckProject)) == nil },
		10*time.Second, 50*time.Millisecond, "the stuck project could not be re-created")
}

func registered(ctx context.Context, provider *milo.Provider, project string) func() bool {
	return func() bool {
		_, err := provider.Get(ctx, multicluster.ClusterName(project))
		return err == nil
	}
}

// firstStuck gives the first cache built for project a sync that never completes, closes waiting when the registration
// first waits on it, and closes stopped when it stops; every other cache is the real one, so a re-created project can
// register.
func firstStuck(project string, waiting, stopped chan struct{}) func(string, cache.Cache) cache.Cache {
	var built sync.Once
	return func(name string, c cache.Cache) cache.Cache {
		stuck := false
		if name == project {
			built.Do(func() { stuck = true })
		}
		if !stuck {
			return c
		}
		return &neverSynced{Cache: c, waiting: waiting, stopped: stopped}
	}
}

type neverSynced struct {
	cache.Cache
	waiting, stopped chan struct{}
	once             sync.Once
}

func (c *neverSynced) Start(ctx context.Context) error {
	err := c.Cache.Start(ctx)
	close(c.stopped)
	return err
}

func (c *neverSynced) WaitForCacheSync(ctx context.Context) bool {
	c.once.Do(func() { close(c.waiting) })
	<-ctx.Done()
	return false
}
