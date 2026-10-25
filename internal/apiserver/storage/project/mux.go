package projectstorage

import (
	"context"
	"path"
	"strings"
	"sync"
	"time"

	"go.miloapis.com/milo/pkg/request"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	generic "k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	storagebackend "k8s.io/apiserver/pkg/storage/storagebackend"
	factory "k8s.io/apiserver/pkg/storage/storagebackend/factory"
	k8smetrics "k8s.io/component-base/metrics"
	k8slegacy "k8s.io/component-base/metrics/legacyregistry"

	"k8s.io/client-go/tools/cache"
)

// -------------------- metrics --------------------

var (
	childCreations = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Name:           "projectstorage_child_creations_total",
			Help:           "Child storage creations by resource type",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"resource_group", "resource_kind"},
	)

	firstReady = k8smetrics.NewHistogramVec(
		&k8smetrics.HistogramOpts{
			Name:           "projectstorage_first_ready_seconds",
			Help:           "Time from child creation to first successful op",
			Buckets:        []float64{0.02, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"resource_group", "resource_kind"},
	)

	childLockHold = k8smetrics.NewHistogramVec(
		&k8smetrics.HistogramOpts{
			Name:           "projectstorage_child_lock_hold_seconds",
			Help:           "Time the per-resource storage lock is held to create a project child storage",
			Buckets:        []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"resource_group", "resource_kind"},
	)

	namespaceBootstrapDuration = k8smetrics.NewHistogramVec(
		&k8smetrics.HistogramOpts{
			Name:           "projectstorage_namespace_bootstrap_duration_seconds",
			Help:           "Time to ensure the milo-system namespace exists in a project control plane",
			Buckets:        []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"result"},
	)

	reinitErrors = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Name:           "projectstorage_reinitializing_errors_total",
			Help:           "Ops that hit 'storage is (re)initializing'",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"resource_group", "resource_kind", "verb"},
	)
)

func init() {
	k8slegacy.MustRegister(childCreations, firstReady, childLockHold, namespaceBootstrapDuration, reinitErrors)
}

func isReinitErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "storage is (re)initializing")
}

func incrReinit(group, kind, verb string) {
	reinitErrors.WithLabelValues(group, kind, verb).Inc()
}

func recordFirstReady(c *child, group, kind string) {
	c.readyOnce.Do(func() {
		firstReady.WithLabelValues(group, kind).
			Observe(time.Since(c.created).Seconds())
	})
}

// -------------------- child & args --------------------

type child struct {
	s         storage.Interface
	destroy   factory.DestroyFunc
	created   time.Time
	readyOnce sync.Once
}

type decoratorArgs struct {
	// labels/identity
	resourceGroup string // e.g. "", "apps", "iam.miloapis.com" (empty means core)
	resourceKind  string // resource plural (e.g., "roles", "protectedresources")

	resourcePrefix string
	keyFunc        func(obj runtime.Object) (string, error)
	newFunc        func() runtime.Object
	newListFunc    func() runtime.Object
	getAttrs       storage.AttrFunc
	triggerFn      storage.IndexerFuncs
	indexers       *cache.Indexers
}

// -------------------- instrumented wrapper --------------------

// instrumentedStorage wraps a storage.Interface to emit metrics once per child
type instrumentedStorage struct {
	inner storage.Interface
	child *child

	// normalized labels
	group string // API group ("" => "core" when you query; we keep "" here)
	kind  string // resource plural
}

func (i *instrumentedStorage) markSuccess() {
	recordFirstReady(i.child, i.group, i.kind)
}
func (i *instrumentedStorage) markReinit(verb string, err error) error {
	if isReinitErr(err) {
		incrReinit(i.group, i.kind, verb)
	}
	return err
}

func (i *instrumentedStorage) Versioner() storage.Versioner { return i.inner.Versioner() }

func (i *instrumentedStorage) Create(ctx context.Context, key string, obj, out runtime.Object, ttl uint64) error {
	if err := i.inner.Create(ctx, key, obj, out, ttl); err != nil {
		return i.markReinit("create", err)
	}
	i.markSuccess()
	return nil
}
func (i *instrumentedStorage) Delete(ctx context.Context, key string, out runtime.Object,
	precond *storage.Preconditions, validateDeletion storage.ValidateObjectFunc,
	cachedExistingObject runtime.Object, opts storage.DeleteOptions) error {
	if err := i.inner.Delete(ctx, key, out, precond, validateDeletion, cachedExistingObject, opts); err != nil {
		return i.markReinit("delete", err)
	}
	i.markSuccess()
	return nil
}
func (i *instrumentedStorage) Watch(ctx context.Context, key string, opts storage.ListOptions) (watch.Interface, error) {
	w, err := i.inner.Watch(ctx, key, opts)
	if err != nil {
		return nil, i.markReinit("watch", err)
	}
	i.markSuccess()
	return w, nil
}
func (i *instrumentedStorage) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	if err := i.inner.Get(ctx, key, opts, objPtr); err != nil {
		return i.markReinit("get", err)
	}
	i.markSuccess()
	return nil
}
func (i *instrumentedStorage) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	if err := i.inner.GetList(ctx, key, opts, listObj); err != nil {
		return i.markReinit("list", err)
	}
	i.markSuccess()
	return nil
}
func (i *instrumentedStorage) GuaranteedUpdate(ctx context.Context, key string, out runtime.Object,
	ignoreNotFound bool, precond *storage.Preconditions, tryUpdate storage.UpdateFunc, suggestion runtime.Object) error {
	if err := i.inner.GuaranteedUpdate(ctx, key, out, ignoreNotFound, precond, tryUpdate, suggestion); err != nil {
		return i.markReinit("update", err)
	}
	i.markSuccess()
	return nil
}
func (i *instrumentedStorage) ReadinessCheck() error { return i.inner.ReadinessCheck() }
func (i *instrumentedStorage) RequestWatchProgress(ctx context.Context) error {
	if err := i.inner.RequestWatchProgress(ctx); err != nil {
		return i.markReinit("watch_progress", err)
	}
	return nil
}
func (i *instrumentedStorage) Stats(ctx context.Context) (storage.Stats, error) {
	return i.inner.Stats(ctx)
}
func (i *instrumentedStorage) GetCurrentResourceVersion(ctx context.Context) (uint64, error) {
	return i.inner.GetCurrentResourceVersion(ctx)
}
func (i *instrumentedStorage) EnableResourceSizeEstimation(fn storage.KeysFunc) error {
	return i.inner.EnableResourceSizeEstimation(fn)
}
func (i *instrumentedStorage) CompactRevision() int64 { return i.inner.CompactRevision() }

// -------------------- mux --------------------

// projectMux implements storage.Interface and routes to a per-project child.
type projectMux struct {
	mu        sync.RWMutex
	children  map[string]*child
	versioner storage.Versioner

	inner        generic.StorageDecorator
	cfg          storagebackend.ConfigForResource
	args         decoratorArgs
	bootstrapper *NamespaceBootstrapper
}

func (m *projectMux) Versioner() storage.Versioner { return m.versioner }

func (m *projectMux) childForProject(project string) (storage.Interface, error) {
	m.mu.RLock()
	if c, ok := m.children[project]; ok {
		m.mu.RUnlock()
		return c.s, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.children[project]; ok {
		return c.s, nil
	}
	start := time.Now()
	defer func() {
		childLockHold.WithLabelValues(m.args.resourceGroup, m.args.resourceKind).Observe(time.Since(start).Seconds())
	}()

	cfg2 := m.cfg // copy
	cfg2.Config.Prefix = "/" + path.Join("projects", project)

	s, destroy, err := m.inner(
		&cfg2,
		m.args.resourcePrefix,
		m.args.keyFunc,
		m.args.newFunc,
		m.args.newListFunc,
		m.args.getAttrs,
		m.args.triggerFn,
		m.args.indexers,
	)
	if err != nil {
		return nil, err
	}
	if m.versioner == nil {
		m.versioner = s.Versioner()
	}
	if m.children == nil {
		m.children = make(map[string]*child, 1)
	}

	// Wrap the child once with instrumentation.
	c := &child{s: s, destroy: destroy, created: time.Now()}
	wrapped := &instrumentedStorage{
		inner: s,
		child: c,
		group: m.args.resourceGroup,
		kind:  m.args.resourceKind,
	}
	c.s = wrapped

	m.children[project] = c
	childCreations.WithLabelValues(m.args.resourceGroup, m.args.resourceKind).Inc()

	return c.s, nil
}

func (m *projectMux) destroyAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, c := range m.children {
		if c.destroy != nil {
			c.destroy()
		}
		delete(m.children, k)
	}
}

func (m *projectMux) pick(ctx context.Context) (storage.Interface, error) {
	if proj, ok := request.ProjectID(ctx); ok && proj != "" {
		s, err := m.childForProject(proj)
		if err != nil {
			return nil, err
		}
		m.bootstrapper.Ensure(ctx, proj)
		return s, nil
	}
	return m.childForProject("")
}

// ---------- storage.Interface forwarding ----------

func (m *projectMux) Create(ctx context.Context, key string, obj, out runtime.Object, ttl uint64) error {
	s, err := m.pick(ctx)
	if err != nil {
		return err
	}
	return s.Create(ctx, key, obj, out, ttl)
}

func (m *projectMux) Delete(ctx context.Context, key string, out runtime.Object, precond *storage.Preconditions,
	validateDeletion storage.ValidateObjectFunc, cachedExistingObject runtime.Object, opts storage.DeleteOptions) error {
	s, err := m.pick(ctx)
	if err != nil {
		return err
	}
	return s.Delete(ctx, key, out, precond, validateDeletion, cachedExistingObject, opts)
}

func (m *projectMux) Watch(ctx context.Context, key string, opts storage.ListOptions) (watch.Interface, error) {
	s, err := m.pick(ctx)
	if err != nil {
		return nil, err
	}
	return s.Watch(ctx, key, opts)
}

func (m *projectMux) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	s, err := m.pick(ctx)
	if err != nil {
		return err
	}
	return s.Get(ctx, key, opts, objPtr)
}

func (m *projectMux) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	s, err := m.pick(ctx)
	if err != nil {
		return err
	}
	return s.GetList(ctx, key, opts, listObj)
}

func (m *projectMux) GuaranteedUpdate(ctx context.Context, key string, out runtime.Object, ignoreNotFound bool,
	precond *storage.Preconditions, tryUpdate storage.UpdateFunc, suggestion runtime.Object) error {
	s, err := m.pick(ctx)
	if err != nil {
		return err
	}
	return s.GuaranteedUpdate(ctx, key, out, ignoreNotFound, precond, tryUpdate, suggestion)
}

// ReadinessCheck proxies to the appropriate child (defaults to the "" project).
func (m *projectMux) ReadinessCheck() error {
	m.mu.RLock()
	c := m.children[""]
	m.mu.RUnlock()
	if c == nil {
		if _, err := m.childForProject(""); err != nil {
			return err
		}
		m.mu.RLock()
		c = m.children[""]
		m.mu.RUnlock()
	}
	return c.s.ReadinessCheck()
}

func (m *projectMux) RequestWatchProgress(ctx context.Context) error {
	s, err := m.pick(ctx)
	if err != nil {
		return err
	}
	return s.RequestWatchProgress(ctx)
}

func (m *projectMux) Stats(ctx context.Context) (storage.Stats, error) {
	s, err := m.pick(ctx)
	if err != nil {
		return storage.Stats{}, err
	}
	return s.Stats(ctx)
}

func (m *projectMux) GetCurrentResourceVersion(ctx context.Context) (uint64, error) {
	s, err := m.pick(ctx)
	if err != nil {
		return 0, err
	}
	return s.GetCurrentResourceVersion(ctx)
}

func (m *projectMux) EnableResourceSizeEstimation(fn storage.KeysFunc) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.children {
		if err := c.s.EnableResourceSizeEstimation(fn); err != nil {
			return err
		}
	}
	return nil
}

func (m *projectMux) CompactRevision() int64 {
	m.mu.RLock()
	c := m.children[""]
	m.mu.RUnlock()
	if c == nil {
		return 0
	}
	return c.s.CompactRevision()
}
