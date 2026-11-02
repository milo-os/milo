package milo_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontroller "sigs.k8s.io/multicluster-runtime/pkg/controller"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.miloapis.com/milo/pkg/multicluster-runtime/milo"
)

// waitFor bounds every wait for something that must happen.
const waitFor = 60 * time.Second

// TestParallelEngagementSyncsProjectsTogether checks that the provider syncs as
// many project caches at once as MaxConcurrentReconciles allows. The syncs are
// held until all three wait together, or for 10 s.
func TestParallelEngagementSyncsProjectsTogether(t *testing.T) {
	gates := newSyncGates()
	go gates.releaseWhen(3, 10*time.Second)
	rig := startParallel(t, 3, 0, gates, "p-1", "p-2", "p-3")

	rig.requireEngaged(t, "p-1", "p-2", "p-3")
	require.Equal(t, 3, gates.peakWaiting(), "project caches synced at the same time")
}

// TestHungProjectDoesNotHoldTheOthers checks that a project whose cache never
// syncs holds one worker, not all of them: the projects created after it
// engage long before its sync times out.
func TestHungProjectDoesNotHoldTheOthers(t *testing.T) {
	gates := newSyncGates()
	gates.hang("a-hung")
	close(gates.release)
	rig := startParallel(t, 2, 30*time.Second, gates, "a-hung")
	gates.requireArrived(t, "a-hung")

	rig.create(t, "b-1", "b-2", "b-3")
	rig.requireEngagedWithin(t, 15*time.Second, "b-1", "b-2", "b-3")
}

// TestLeaseLostWithProjectsInFlight checks the lease lost during the warm-up
// with three registrations in their syncs: every one ends, every project cache
// stops, nothing stays registered, and the manager returns.
func TestLeaseLostWithProjectsInFlight(t *testing.T) {
	gates := newSyncGates()
	gates.hang("p-1", "p-2", "p-3")
	rig := startParallel(t, 3, 5*time.Minute, gates, "p-1", "p-2", "p-3")
	gates.requireArrived(t, "p-1", "p-2", "p-3")

	rig.cancel()

	select {
	case <-rig.done:
	case <-time.After(15 * time.Second):
		t.Fatal("the manager did not return after the lease was lost")
	}
	gates.requireStopped(t, 15*time.Second, gates.cacheOf("p-1"), gates.cacheOf("p-2"), gates.cacheOf("p-3"))
	rig.requireUnregistered(t, "p-1", "p-2", "p-3")
}

// TestProjectDeletedWhileOthersEngage checks that a project deleted during its
// sync is torn down at once, while the other projects' syncs still wait, and
// that the others then engage.
func TestProjectDeletedWhileOthersEngage(t *testing.T) {
	gates := newSyncGates()
	rig := startParallel(t, 3, 5*time.Minute, gates, "p-1", "p-2", "p-3")
	gates.requireArrived(t, "p-1", "p-2", "p-3")

	rig.delete(t, "p-2")
	gates.requireStopped(t, 15*time.Second, gates.cacheOf("p-2"))
	close(gates.release)

	rig.requireEngaged(t, "p-1", "p-3")
	rig.requireUnregistered(t, "p-2")
	require.False(t, rig.wasEngaged("p-2"), "the deleted project was engaged")
}

// TestProjectRecreatedUnderTheSameName checks a project deleted and created
// again during its sync, while others engage: the first registration's cache
// stops, and the new project is engaged with a cache of its own.
func TestProjectRecreatedUnderTheSameName(t *testing.T) {
	gates := newSyncGates()
	rig := startParallel(t, 3, 5*time.Minute, gates, "p-1", "p-2", "p-3")
	gates.requireArrived(t, "p-1", "p-2", "p-3")
	first := gates.cacheOf("p-2")

	rig.delete(t, "p-2")
	rig.create(t, "p-2")
	gates.requireStopped(t, 15*time.Second, first)
	close(gates.release)

	rig.requireEngaged(t, "p-1", "p-2", "p-3")
	require.NotSame(t, first, gates.cacheOf("p-2"), "the new project reuses the deleted project's cache")
}

// parallelRig runs the provider under a real manager against envtest. Every
// project cluster's cache is wrapped by gates, and a fan-out controller records
// each project it is engaged for.
type parallelRig struct {
	setup    client.Client
	provider *milo.Provider
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	engaged  map[string]bool
}

func startParallel(t *testing.T, workers int, syncTimeout time.Duration, gates *syncGates, projects ...string) *parallelRig {
	t.Helper()
	cfg := startEnvtest(t)
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	setup, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	rig := &parallelRig{setup: setup, cancel: cancel, done: make(chan struct{}), engaged: map[string]bool{}}
	t.Cleanup(func() { cancel(); <-rig.done })

	require.NoError(t, setup.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "probe", Namespace: "default"}}))
	rig.create(t, projects...)

	localMgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             ctrlconfig.Controller{SkipNameValidation: ptr.To(true)},
	})
	require.NoError(t, err)
	rig.provider, err = milo.New(localMgr, milo.Options{
		ControllerName:          "parallel-engagement-test",
		MaxConcurrentReconciles: workers,
		ProjectCacheSyncTimeout: syncTimeout,
		ClusterOptions:          []cluster.Option{projectClusterAt(cfg, gates.wrap)},
	})
	require.NoError(t, err)
	mcMgr, err := mcmanager.WithMultiCluster(localMgr, milo.WithoutAutoStart(rig.provider))
	require.NoError(t, err)
	require.NoError(t, milo.EngageAlways(mcMgr, rig.provider))
	require.NoError(t, rig.recordEngagements(mcMgr))

	go func() { defer close(rig.done); _ = mcMgr.Start(ctx) }()
	return rig
}

// recordEngagements adds the fan-out controller: a project is engaged once a
// reconcile arrives for it.
func (r *parallelRig) recordEngagements(mcMgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mcMgr).
		Named("parallel-engagement-fanout").
		WithOptions(mccontroller.Options{SkipNameValidation: ptr.To(true)}).
		For(&corev1.ConfigMap{}, mcbuilder.WithEngageWithProviderClusters(true)).
		Complete(mcreconcile.Func(func(_ context.Context, req mcreconcile.Request) (ctrl.Result, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.engaged[req.ClusterName.String()] = true
			return ctrl.Result{}, nil
		}))
}

func (r *parallelRig) create(t *testing.T, projects ...string) {
	t.Helper()
	for _, name := range projects {
		require.NoError(t, r.setup.Create(context.Background(), readyProject(name)))
	}
}

func (r *parallelRig) delete(t *testing.T, project string) {
	t.Helper()
	require.NoError(t, r.setup.Delete(context.Background(), readyProject(project)))
}

func (r *parallelRig) wasEngaged(project string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.engaged[project]
}

func (r *parallelRig) requireEngaged(t *testing.T, projects ...string) {
	t.Helper()
	r.requireEngagedWithin(t, waitFor, projects...)
}

func (r *parallelRig) requireEngagedWithin(t *testing.T, limit time.Duration, projects ...string) {
	t.Helper()
	for _, name := range projects {
		require.Eventually(t, func() bool { return r.wasEngaged(name) }, limit, 50*time.Millisecond, "project %s not engaged", name)
	}
}

// requireUnregistered checks that the provider has no cluster for each project.
func (r *parallelRig) requireUnregistered(t *testing.T, projects ...string) {
	t.Helper()
	for _, name := range projects {
		require.Eventually(t, func() bool {
			_, err := r.provider.Get(context.Background(), multicluster.ClusterName(name))
			return err != nil
		}, waitFor, 50*time.Millisecond, "project %s still registered", name)
	}
}

// syncGates holds every project cache's WaitForCacheSync until release is
// closed, or for good for a hung project, and records how many syncs wait at
// once and when each project's cache stops. Each wait also ends with its
// context, as controller-runtime's does.
type syncGates struct {
	mu      sync.Mutex
	waiting int
	peak    int
	hung    map[string]bool
	arrived map[string]bool
	caches  map[string]*gatedSyncCache
	release chan struct{}
}

func newSyncGates() *syncGates {
	return &syncGates{hung: map[string]bool{}, arrived: map[string]bool{}, caches: map[string]*gatedSyncCache{}, release: make(chan struct{})}
}

func (g *syncGates) hang(projects ...string) {
	for _, name := range projects {
		g.hung[name] = true
	}
}

// releaseWhen closes release once n syncs wait together, or after limit.
func (g *syncGates) releaseWhen(n int, limit time.Duration) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) && g.peakWaiting() < n {
		time.Sleep(10 * time.Millisecond)
	}
	close(g.release)
}

func (g *syncGates) wrap(project string, c cache.Cache) cache.Cache {
	g.mu.Lock()
	defer g.mu.Unlock()
	gc := &gatedSyncCache{Cache: c, project: project, gates: g, stopped: make(chan struct{})}
	g.caches[project] = gc
	return gc
}

func (g *syncGates) cacheOf(project string) *gatedSyncCache {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.caches[project]
}

func (g *syncGates) enter(project string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.arrived[project] = true
	g.waiting++
	g.peak = max(g.peak, g.waiting)
}

func (g *syncGates) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.waiting--
}

func (g *syncGates) peakWaiting() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.peak
}

func (g *syncGates) hasArrived(project string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.arrived[project]
}

func (g *syncGates) requireArrived(t *testing.T, projects ...string) {
	t.Helper()
	for _, name := range projects {
		require.Eventually(t, func() bool { return g.hasArrived(name) }, waitFor, 10*time.Millisecond, "project %s never began its sync", name)
	}
}

// requireStopped checks that each cache stops within limit.
func (g *syncGates) requireStopped(t *testing.T, limit time.Duration, caches ...*gatedSyncCache) {
	t.Helper()
	for _, c := range caches {
		select {
		case <-c.stopped:
		case <-time.After(limit):
			t.Fatalf("project %s's cache still runs after %s", c.project, limit)
		}
	}
}

type gatedSyncCache struct {
	cache.Cache
	project string
	gates   *syncGates
	stopped chan struct{}
}

func (c *gatedSyncCache) Start(ctx context.Context) error {
	defer close(c.stopped)
	return c.Cache.Start(ctx)
}

func (c *gatedSyncCache) WaitForCacheSync(ctx context.Context) bool {
	c.gates.enter(c.project)
	defer c.gates.leave()
	if c.gates.hung[c.project] {
		<-ctx.Done()
		return false
	}
	select {
	case <-c.gates.release:
		return c.Cache.WaitForCacheSync(ctx)
	case <-ctx.Done():
		return false
	}
}
