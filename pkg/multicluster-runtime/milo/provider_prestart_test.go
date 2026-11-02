package milo

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

// TestPrestartedInformersStartWithTheProjectCache checks that every informer the
// engaged controllers watch exists before the project's cache starts. An
// informer created after the start waits for its own sync inside Engage, one
// after another, while the provider's single worker holds every other project.
func TestPrestartedInformersStartWithTheProjectCache(t *testing.T) {
	watched := []client.Object{&corev1.ConfigMap{}, &corev1.Secret{}}
	p, project := newTestProvider(metav1.ConditionTrue, nil)
	p.opts.PrestartInformers = watched
	recorder := withRecordingCache(p)
	p.mcMgr = &watchingEngageManager{watches: watched}

	_, err := p.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(project)})
	require.NoError(t, err)

	require.Empty(t, recorder.createdAfterStart(), "informers created after the project's cache started")
}

// TestPrestartFailureRegistersNothing checks that an informer the cache cannot
// create fails the registration before the project's cache starts, and leaves
// nothing registered or running.
func TestPrestartFailureRegistersNothing(t *testing.T) {
	p, project := newTestProvider(metav1.ConditionTrue, nil)
	p.opts.PrestartInformers = []client.Object{&corev1.ConfigMap{}}
	recorder := withRecordingCache(p)
	recorder.failInformers = true

	_, err := p.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(project)})
	require.ErrorContains(t, err, "failed to create informer")

	require.False(t, recorder.wasStarted(), "the project's cache started after a failed prestart")
	require.Empty(t, p.projects)
	require.Empty(t, p.cancelFns)
}

// TestPrestartWithCanceledContextRegistersNothing checks the lease lost during
// the warm-up: the leader's context ends, the registration fails, and the
// project's cache stops.
func TestPrestartWithCanceledContextRegistersNothing(t *testing.T) {
	p, project := newTestProvider(metav1.ConditionTrue, nil)
	p.opts.PrestartInformers = []client.Object{&corev1.ConfigMap{}}
	recorder := withRecordingCache(p)
	recorder.neverSync = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(project)})
	require.Error(t, err)

	require.Empty(t, p.projects)
	require.Empty(t, p.cancelFns)
	<-recorder.stopped
}

// withRecordingCache gives every project cluster of p a recordingCache and returns it.
func withRecordingCache(p *Provider) *recordingCache {
	c := &recordingCache{created: map[string]bool{}, started: make(chan struct{}), stopped: make(chan struct{})}
	p.opts.ClusterOptions = append(p.opts.ClusterOptions, func(o *cluster.Options) {
		o.NewCache = func(_ *rest.Config, _ cache.Options) (cache.Cache, error) { return c, nil }
	})
	return c
}

// recordingCache records each informer it creates and whether the cache had
// started by then. Its WaitForCacheSync waits for Start, as controller-runtime's
// informer cache does, so an informer created after the sync is after the start.
type recordingCache struct {
	cache.Cache
	mu            sync.Mutex
	isStarted     bool
	created       map[string]bool
	after         []string
	failInformers bool
	neverSync     bool
	started       chan struct{}
	stopped       chan struct{}
}

// GetInformer creates a type's informer on its first request and returns the same one after, as controller-runtime's
// cache does; only a creation after Start is recorded.
func (c *recordingCache) GetInformer(_ context.Context, obj client.Object, _ ...cache.InformerGetOption) (cache.Informer, error) {
	if c.failInformers {
		return nil, errors.New("not cached")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	name := typeName(obj)
	if c.created[name] {
		return nil, nil
	}
	c.created[name] = true
	if c.isStarted {
		c.after = append(c.after, name)
	}
	return nil, nil
}

func (c *recordingCache) Start(ctx context.Context) error {
	c.mu.Lock()
	c.isStarted = true
	c.mu.Unlock()
	close(c.started)
	<-ctx.Done()
	close(c.stopped)
	return nil
}

func (c *recordingCache) WaitForCacheSync(ctx context.Context) bool {
	if c.neverSync {
		<-ctx.Done()
		return false
	}
	select {
	case <-c.started:
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *recordingCache) createdAfterStart() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.after...)
}

func (c *recordingCache) wasStarted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isStarted
}

func typeName(obj client.Object) string {
	switch obj.(type) {
	case *corev1.ConfigMap:
		return "ConfigMap"
	case *corev1.Secret:
		return "Secret"
	}
	return "unknown"
}

// watchingEngageManager engages a cluster the way a multicluster-runtime Kind
// source does: it asks the cluster's cache for the informer of each watched type.
type watchingEngageManager struct {
	mcmanager.Manager
	watches []client.Object
}

func (m *watchingEngageManager) Engage(ctx context.Context, _ multicluster.ClusterName, cl cluster.Cluster) error {
	for _, obj := range m.watches {
		if _, err := cl.GetCache().GetInformer(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}
