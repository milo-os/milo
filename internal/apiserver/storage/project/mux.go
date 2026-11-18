package projectstorage

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.miloapis.com/milo/internal/apiserver/projectstate"
	"go.miloapis.com/milo/pkg/request"

	"golang.org/x/time/rate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"

	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	generic "k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	storagebackend "k8s.io/apiserver/pkg/storage/storagebackend"
	factory "k8s.io/apiserver/pkg/storage/storagebackend/factory"
	k8smetrics "k8s.io/component-base/metrics"
	k8slegacy "k8s.io/component-base/metrics/legacyregistry"

	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
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

	unknownProjectWritesRefused = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Name:           "projectstorage_unknown_project_writes_refused_total",
			Help:           "Writes refused because their Project could not be found (NotFound) or read (InternalError)",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"resource_group", "resource_kind", "reason"},
	)

	unknownProjectReadsRefused = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Name:           "projectstorage_unknown_project_reads_refused_total",
			Help:           "Reads and deletes refused because their Project could not be found (NotFound) or read (InternalError)",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"resource_group", "resource_kind", "reason"},
	)

	childRemovals = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Name: "projectstorage_child_removals_total",
			Help: "Child storages removed, by resource type and by reason: project_gone when their Project left " +
				"the API server's view, idle when no request and no watch used them for the idle time; " +
				"each is destroyed once in-flight requests drain",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"resource_group", "resource_kind", "reason"},
	)

	readinessChildren = k8smetrics.NewGaugeVec(
		&k8smetrics.GaugeOpts{
			Name: "projectstorage_readiness_children",
			Help: "Child storages of the Projects in the view, by resource type and by whether they serve, as the " +
				"readiness check at the server's start last read them",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"resource_group", "resource_kind", "state"},
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
	k8slegacy.MustRegister(childCreations, firstReady, childLockHold, namespaceBootstrapDuration,
		unknownProjectWritesRefused, unknownProjectReadsRefused, childRemovals, reinitErrors, readinessChildren)
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

	// lastUsed is the last time, in Unix nanoseconds, that a request picked
	// the child or a watch on it ended; watches counts its open watches. The
	// idle sweep retires a child with no open watch and no use for the idle
	// time.
	lastUsed atomic.Int64
	watches  atomic.Int32
}

func (c *child) touch(now time.Time) { c.lastUsed.Store(now.UnixNano()) }

// idle says whether no watch is open on the child and nothing used it for d.
func (c *child) idle(now time.Time, d time.Duration) bool {
	return c.watches.Load() == 0 && now.Sub(time.Unix(0, c.lastUsed.Load())) >= d
}

// countedWatch ends its child's count of open watches once, at Stop. The
// API server's watch handler stops every watch it serves when the watch ends
// (k8s.io/apiserver v0.37.1, pkg/endpoints/handlers/get.go line 299).
type countedWatch struct {
	watch.Interface
	child *child
	clock clock.PassiveClock
	once  sync.Once
}

func (w *countedWatch) Stop() {
	w.once.Do(func() {
		w.child.touch(w.clock.Now())
		w.child.watches.Add(-1)
	})
	w.Interface.Stop()
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
	clock clock.PassiveClock // the mux's clock, for the time a watch on the child stops

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
	i.child.watches.Add(1)
	w, err := i.inner.Watch(ctx, key, opts)
	if err != nil {
		i.child.watches.Add(-1)
		return nil, i.markReinit("watch", err)
	}
	i.markSuccess()
	return &countedWatch{Interface: w, child: i.child, clock: i.clock}, nil
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

// refusalLogInterval bounds the refusal log line to one per kind in that time; the counter carries the
// volume. A writer that retries a refused write in a loop would otherwise log at its own rate.
const refusalLogInterval = 10 * time.Second

// Reasons a child storage is removed, the reason label of childRemovals.
const (
	removedProjectGone = "project_gone"
	removedIdle        = "idle"
)

// DefaultChildIdleTimeout is --project-storage-idle-timeout's default. A
// client that asks for one Project's resource at an interval a little longer
// than the idle time pays a rebuild each time: one list from etcd and a cache
// start. Keep the idle time longer than such a client's interval.
const DefaultChildIdleTimeout = 15 * time.Minute

// childIdleTimeout is how long a Project's child storage is kept with no
// request and no open watch; zero keeps every child until its Project goes.
// SetChildIdleTimeout sets it at start, before the first storage is built.
var childIdleTimeout time.Duration

// SetChildIdleTimeout sets how long a Project's child storage is kept with no
// request and no open watch, for every storage built after the call. Zero
// turns the idle retire off.
func SetChildIdleTimeout(d time.Duration) {
	childIdleTimeout = d
}

// ChildIdleTimeout returns the idle time SetChildIdleTimeout set.
func ChildIdleTimeout() time.Duration { return childIdleTimeout }

// maxIdleSweepPeriod bounds how often each mux looks for idle children, as the
// kubelet's watch-based object cache sweeps its idle watches once a minute
// (k8s.io/kubernetes v1.37.1, pkg/kubelet/util/manager/watch_based_manager.go
// line 207).
const maxIdleSweepPeriod = time.Minute

// ProjectLookup tells the storage about Projects: Lookup says which Project a
// name refers to now, reading storage when the view cannot answer; Project
// says whether the view holds a name; AddListener reports Projects that leave
// the view.
type ProjectLookup interface {
	Lookup(ctx context.Context, name string) (projectstate.Project, error)
	Project(name string) (projectstate.Project, bool)
	AddListener(projectstate.Listener) (remove func())
}

// projectMux implements storage.Interface and routes to a per-project child.
type projectMux struct {
	mu        sync.RWMutex
	children  map[string]*child
	versioner storage.Versioner

	inner        generic.StorageDecorator
	cfg          storagebackend.ConfigForResource
	args         decoratorArgs
	projects     ProjectLookup
	bootstrapper *NamespaceBootstrapper
	refusalLog   *rate.Limiter

	// drain is how long a removed child serves the requests that picked it
	// before its storage stops; removeListener stops the view's reports.
	drain          time.Duration
	removeListener func()

	// idle is how long a child is kept with no request and no open watch;
	// zero keeps it until its Project goes. clock tells the time of each use
	// and of each sweep; stopSweep ends the sweep.
	idle      time.Duration
	clock     clock.PassiveClock
	stopSweep chan struct{}
}

func (m *projectMux) Versioner() storage.Versioner { return m.versioner }

// childForProject returns the project's child, built on the first request.
// It marks the child used under the mux's lock, so the idle sweep, which
// holds the lock to remove a child, never retires one a request just picked.
func (m *projectMux) childForProject(project string) (storage.Interface, error) {
	m.mu.RLock()
	if c, ok := m.children[project]; ok {
		c.touch(m.clock.Now())
		m.mu.RUnlock()
		return c.s, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.children[project]; ok {
		c.touch(m.clock.Now())
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
	c.touch(m.clock.Now())
	wrapped := &instrumentedStorage{
		inner: s,
		child: c,
		clock: m.clock,
		group: m.args.resourceGroup,
		kind:  m.args.resourceKind,
	}
	c.s = wrapped

	m.children[project] = c
	childCreations.WithLabelValues(m.args.resourceGroup, m.args.resourceKind).Inc()

	return c.s, nil
}

// groupResource names the resource this mux stores.
func (m *projectMux) groupResource() schema.GroupResource {
	return schema.GroupResource{Group: m.args.resourceGroup, Resource: m.args.resourceKind}
}

func (m *projectMux) destroyAll() {
	forgetMux(m.groupResource(), m)
	if m.removeListener != nil {
		m.removeListener()
	}
	if m.stopSweep != nil {
		close(m.stopSweep)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, c := range m.children {
		if c.destroy != nil {
			c.destroy()
		}
		delete(m.children, k)
	}
}

// buildChildren builds the missing children of the named Projects, at most n
// of them, as a request into each would. It returns the first build that
// failed, so the readiness check can say a build failed rather than a cache
// being slow; the next call tries that Project again.
func (m *projectMux) buildChildren(names []string, n int) error {
	var failed error
	for _, name := range names {
		if n == 0 {
			break
		}
		if m.hasChild(name) {
			continue
		}
		if _, err := m.childForProject(name); err != nil {
			if failed == nil {
				failed = fmt.Errorf("building the storage of project %s: %w", name, err)
			}
			continue
		}
		n--
	}
	return failed
}

func (m *projectMux) hasChild(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.children[name]
	return ok
}

// unready returns the named Projects whose child is missing or does not serve:
// its own ReadinessCheck, the cacher's ready state that a watch is gated on.
func (m *projectMux) unready(names []string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var cold []string
	for _, name := range names {
		if c, ok := m.children[name]; !ok || c.s.ReadinessCheck() != nil {
			cold = append(cold, name)
		}
	}
	return cold
}

// ProjectGone removes the named Project's child, so the next request into the
// gone Project builds none, and retires it.
func (m *projectMux) ProjectGone(name string) {
	if name == "" {
		return
	}
	m.mu.Lock()
	c, ok := m.children[name]
	delete(m.children, name)
	m.mu.Unlock()
	if ok {
		m.retire(c, removedProjectGone)
	}
}

// ViewResynced removes and retires the child of every name the view does not
// hold: a Project created and deleted inside one watch gap, or removed after a
// lookup found it and before its child was built.
func (m *projectMux) ViewResynced() {
	m.mu.Lock()
	var gone []*child
	for name, c := range m.children {
		if _, ok := m.projects.Project(name); !ok && name != "" {
			gone = append(gone, c)
			delete(m.children, name)
		}
	}
	m.mu.Unlock()
	for _, c := range gone {
		m.retire(c, removedProjectGone)
	}
}

// startIdleSweep retires idle children every sweep period until destroyAll,
// when m.idle is set.
func (m *projectMux) startIdleSweep() {
	if m.idle <= 0 {
		return
	}
	m.stopSweep = make(chan struct{})
	go wait.Until(m.retireIdle, min(m.idle, maxIdleSweepPeriod), m.stopSweep)
}

// retireIdle removes and retires every Project's child that no request and no
// watch used for the idle time, as ProjectGone retires a gone Project's; the
// next request into the Project builds it again. The root plane's child
// stays, and so does a child whose cache has not served yet, as the kubelet
// stops only a synced idle watch (k8s.io/kubernetes v1.37.1,
// pkg/kubelet/util/manager/watch_based_manager.go line 106): its first list
// is still running, and a request is waiting for it. A resource that a
// readiness check holds warm keeps every child.
func (m *projectMux) retireIdle() {
	if isHeldWarm(m.groupResource()) {
		return
	}
	now := m.clock.Now()
	m.mu.Lock()
	var idle []*child
	for name, c := range m.children {
		if name != "" && c.idle(now, m.idle) && c.s.ReadinessCheck() == nil {
			idle = append(idle, c)
			delete(m.children, name)
		}
	}
	m.mu.Unlock()
	for _, c := range idle {
		m.retire(c, removedIdle)
	}
}

// retire destroys a removed child after drain. A request that picked the child
// before its removal ends within the server's request timeout, and a watch on
// it ends when its cacher stops.
func (m *projectMux) retire(c *child, reason string) {
	childRemovals.WithLabelValues(m.args.resourceGroup, m.args.resourceKind, reason).Inc()
	if c.destroy != nil {
		time.AfterFunc(m.drain, c.destroy)
	}
}

func (m *projectMux) pick(ctx context.Context) (storage.Interface, error) {
	project, ok := request.ProjectID(ctx)
	if !ok || project == "" {
		return m.childForProject("")
	}
	if err := m.admit(ctx, project); err != nil {
		return nil, err
	}
	return m.childForProject(project)
}

// admit looks up the Project a request into its plane names, whatever its
// verb. A request into a Project that storage does not hold, or that cannot be
// read, is refused and builds no child: a child keeps a watch cache until its
// Project leaves the view, so a read into a missing Project would keep one
// forever. A live Project is bootstrapped where this kind bootstraps; a
// deleting one is not, and its children serve the purge until it is gone.
//
// Upstream's NamespaceLifecycle refuses only a create into a missing
// namespace and lets an update through (k8s.io/apiserver v0.37.1,
// pkg/admission/plugin/namespace/lifecycle/admission.go lines 105-109,
// kubernetes/kubernetes#140661). The request's verb cannot tell an update
// that creates (an apply patch, or a kind that allows create on update) from
// one that changes, so an update and a patch are refused too. That strands no
// namespaced object: the Project's finalizer holds until the purge has
// emptied every namespace and every cluster-scoped object that carries a
// finalizer is gone. A cluster-scoped object whose delete was refused can
// outlive its Project, so an update to one of them in a gone Project is
// refused, and counted with reason NotFound.
func (m *projectMux) admit(ctx context.Context, project string) error {
	p, err := m.projects.Lookup(ctx, project)
	if err != nil {
		return m.refuse(ctx, project, err)
	}
	if !p.Deleting {
		m.bootstrapper.Ensure(ctx, project, p.UID)
	}
	return nil
}

// refuse returns err for a request into project's plane, counted as a write or
// a read, and logged at most once per refusalLogInterval for this kind.
func (m *projectMux) refuse(ctx context.Context, project string, err error) error {
	reason := "InternalError"
	if apierrors.IsNotFound(err) {
		reason = "NotFound"
	}
	refused := unknownProjectReadsRefused
	if _, writes := projectstate.WriteVerb(ctx); writes {
		refused = unknownProjectWritesRefused
	}
	refused.WithLabelValues(m.args.resourceGroup, m.args.resourceKind, reason).Inc()
	if m.refusalLog.Allow() {
		klog.InfoS("Refused a request into a project control plane whose Project cannot be found",
			"project", project, "group", m.args.resourceGroup, "resource", m.args.resourceKind,
			"verb", requestVerb(ctx), "user", requestUser(ctx), "reason", reason)
	}
	return err
}

// requestVerb names the request's verb, or "" when the context carries none.
func requestVerb(ctx context.Context) string {
	if info, ok := genericapirequest.RequestInfoFrom(ctx); ok {
		return info.Verb
	}
	return ""
}

// requestUser names the request's user, or "" when the context carries none.
func requestUser(ctx context.Context) string {
	if u, ok := genericapirequest.UserFrom(ctx); ok {
		return u.GetName()
	}
	return ""
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
