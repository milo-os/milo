package projectstorage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	generic "k8s.io/apiserver/pkg/registry/generic"
	apiserver "k8s.io/apiserver/pkg/server"
	serverstorage "k8s.io/apiserver/pkg/server/storage"
	"k8s.io/apiserver/pkg/storage"
	storagebackend "k8s.io/apiserver/pkg/storage/storagebackend"
	factory "k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/client-go/tools/cache"
	"k8s.io/component-base/metrics/testutil"

	"go.miloapis.com/milo/internal/apiserver/projectstate"
	"go.miloapis.com/milo/internal/apiserver/projectstate/projectstatetest"
	"go.miloapis.com/milo/pkg/request"
)

type fakeStorage struct {
	storage.Interface
}

func (fakeStorage) Versioner() storage.Versioner { return storage.APIObjectVersioner{} }

func (fakeStorage) Get(context.Context, string, storage.GetOptions, runtime.Object) error {
	return nil
}

func (fakeStorage) Create(context.Context, string, runtime.Object, runtime.Object, uint64) error {
	return nil
}

func (fakeStorage) GuaranteedUpdate(context.Context, string, runtime.Object, bool, *storage.Preconditions,
	storage.UpdateFunc, runtime.Object) error {
	return nil
}

func newTestMux(b *NamespaceBootstrapper, projects ProjectLookup) *projectMux {
	inner := func(*storagebackend.ConfigForResource, string, func(runtime.Object) (string, error),
		func() runtime.Object, func() runtime.Object, storage.AttrFunc, storage.IndexerFuncs,
		*cache.Indexers) (storage.Interface, factory.DestroyFunc, error) {
		return fakeStorage{}, func() {}, nil
	}
	return &projectMux{
		children:     map[string]*child{"": {s: fakeStorage{}}},
		versioner:    storage.APIObjectVersioner{},
		inner:        inner,
		args:         decoratorArgs{resourceGroup: "quota.miloapis.com", resourceKind: "resourceclaims"},
		projects:     projects,
		bootstrapper: b,
		refusalLog:   rate.NewLimiter(rate.Every(refusalLogInterval), 1),
	}
}

type blockingRun struct {
	mu      sync.Mutex
	calls   map[string]int
	started chan string
	release map[string]chan struct{}
}

func newBlockingRun(blocked ...string) *blockingRun {
	r := &blockingRun{
		calls:   map[string]int{},
		started: make(chan string, 16),
		release: map[string]chan struct{}{},
	}
	for _, p := range blocked {
		r.release[p] = make(chan struct{})
	}
	return r
}

func (r *blockingRun) run(ctx context.Context, project string) error {
	r.mu.Lock()
	r.calls[project]++
	ch := r.release[project]
	r.mu.Unlock()
	r.started <- project
	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (r *blockingRun) count(project string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[project]
}

func getAsync(m *projectMux, project string) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- m.Get(request.WithProject(context.Background(), project), "/k", storage.GetOptions{}, nil)
	}()
	return done
}

func TestSlowBootstrapDoesNotBlockOtherProjects(t *testing.T) {
	r := newBlockingRun("slow")
	m := newTestMux(newNamespaceBootstrapper(time.Minute, r.run), everyProjectKnown())

	slow := getAsync(m, "slow")
	if p := <-r.started; p != "slow" {
		t.Fatalf("expected bootstrap for slow project first, got %q", p)
	}

	select {
	case err := <-getAsync(m, "fast"):
		if err != nil {
			t.Fatalf("fast project get: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request for another project blocked behind a slow bootstrap")
	}

	select {
	case <-slow:
		t.Fatal("slow project request returned before its bootstrap finished")
	default:
	}

	close(r.release["slow"])
	if err := <-slow; err != nil {
		t.Fatalf("slow project get: %v", err)
	}
}

func TestConcurrentFirstTouchesShareOneBootstrap(t *testing.T) {
	r := newBlockingRun("p")
	b := newNamespaceBootstrapper(time.Minute, r.run)
	muxes := []*projectMux{newTestMux(b, everyProjectKnown()), newTestMux(b, everyProjectKnown())}

	first := getAsync(muxes[0], "p")
	<-r.started

	var waiters []<-chan error
	for i := 0; i < 10; i++ {
		waiters = append(waiters, getAsync(muxes[i%2], "p"))
	}
	time.Sleep(50 * time.Millisecond)
	for _, w := range waiters {
		select {
		case <-w:
			t.Fatal("request returned before the in-flight bootstrap for its project finished")
		default:
		}
	}

	close(r.release["p"])
	for _, w := range append(waiters, first) {
		if err := <-w; err != nil {
			t.Fatalf("get: %v", err)
		}
	}
	if n := r.count("p"); n != 1 {
		t.Fatalf("expected one bootstrap for the project, got %d", n)
	}

	if err := <-getAsync(muxes[1], "p"); err != nil {
		t.Fatalf("get after bootstrap: %v", err)
	}
	if n := r.count("p"); n != 1 {
		t.Fatalf("expected no bootstrap after success, got %d", n)
	}
}

func TestBootstrapIsBoundedByTimeout(t *testing.T) {
	r := newBlockingRun("stuck")
	m := newTestMux(newNamespaceBootstrapper(50*time.Millisecond, r.run), everyProjectKnown())

	select {
	case err := <-getAsync(m, "stuck"):
		if err != nil {
			t.Fatalf("get: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bootstrap was not bounded by its timeout")
	}
}

func TestWaiterHonoursItsOwnContext(t *testing.T) {
	r := newBlockingRun("p")
	b := newNamespaceBootstrapper(time.Minute, r.run)
	go b.Ensure(context.Background(), "p", "uid-p")
	<-r.started

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		b.Ensure(ctx, "p", "uid-p")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter ignored its context")
	}
	close(r.release["p"])
}

func TestFailedBootstrapRetriesAfterBackoff(t *testing.T) {
	var calls atomic.Int32
	b := newNamespaceBootstrapper(time.Minute, func(context.Context, string) error {
		calls.Add(1)
		return errors.New("loopback unavailable")
	})
	now := time.Unix(0, 0)
	b.now = func() time.Time { return now }

	b.Ensure(context.Background(), "p", "uid-p")
	b.Ensure(context.Background(), "p", "uid-p")
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected no retry inside back-off, got %d calls", n)
	}

	now = now.Add(b.retryAfter)
	b.Ensure(context.Background(), "p", "uid-p")
	if n := calls.Load(); n != 2 {
		t.Fatalf("expected a retry after back-off, got %d calls", n)
	}
}

func TestNilBootstrapperAndRootProjectSkipBootstrap(t *testing.T) {
	var nilB *NamespaceBootstrapper
	nilB.Ensure(context.Background(), "p", "uid-p")

	var calls atomic.Int32
	b := newNamespaceBootstrapper(time.Minute, func(context.Context, string) error {
		calls.Add(1)
		return nil
	})
	m := newTestMux(b, everyProjectKnown())
	if err := m.Get(context.Background(), "/k", storage.GetOptions{}, nil); err != nil {
		t.Fatalf("root get: %v", err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("expected no bootstrap for the root project, got %d", n)
	}
}

func TestPanickingBootstrapReleasesWaiters(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	b := newNamespaceBootstrapper(time.Minute, func(context.Context, string) error {
		calls.Add(1)
		<-release
		panic("boom")
	})
	now := time.Unix(0, 0)
	var nowMu sync.Mutex
	b.now = func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}

	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			b.Ensure(context.Background(), "p", "uid-p")
			done <- struct{}{}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("a panicking bootstrap left requests waiting")
		}
	}

	nowMu.Lock()
	now = now.Add(b.retryAfter)
	nowMu.Unlock()
	b.Ensure(context.Background(), "p", "uid-p")
	if n := calls.Load(); n != 2 {
		t.Fatalf("expected the panicked attempt to count as failed and retry, got %d calls", n)
	}
}

func TestCancelledLeaderStopsWaitingWhileAttemptContinues(t *testing.T) {
	r := newBlockingRun("p")
	b := newNamespaceBootstrapper(time.Minute, r.run)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	leader := make(chan struct{})
	go func() {
		b.Ensure(ctx, "p", "uid-p")
		close(leader)
	}()
	<-r.started
	select {
	case <-leader:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled leader kept waiting on the bootstrap")
	}

	waiter := make(chan struct{})
	go func() {
		b.Ensure(context.Background(), "p", "uid-p")
		close(waiter)
	}()
	select {
	case <-waiter:
		t.Fatal("waiter returned before the detached attempt finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(r.release["p"])
	<-waiter
	if n := r.count("p"); n != 1 {
		t.Fatalf("expected the detached attempt to be reused, got %d calls", n)
	}
}

func TestNamespacesStorageNeverWaitsOnBootstrap(t *testing.T) {
	r := newBlockingRun("p")
	b := newNamespaceBootstrapper(time.Minute, r.run)
	inner := func(*storagebackend.ConfigForResource, string, func(runtime.Object) (string, error),
		func() runtime.Object, func() runtime.Object, storage.AttrFunc, storage.IndexerFuncs,
		*cache.Indexers) (storage.Interface, factory.DestroyFunc, error) {
		return fakeStorage{}, func() {}, nil
	}
	cfg := &storagebackend.ConfigForResource{}
	ns, _, err := ProjectAwareDecorator(schema.GroupResource{Resource: "namespaces"}, inner, everyProjectKnown(), b)(
		cfg, "", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("decorate namespaces: %v", err)
	}
	claims, _, err := ProjectAwareDecorator(schema.GroupResource{Group: "quota.miloapis.com", Resource: "resourceclaims"}, inner, everyProjectKnown(), b)(
		cfg, "", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("decorate resourceclaims: %v", err)
	}

	pctx := request.WithProject(context.Background(), "p")
	claimDone := make(chan error, 1)
	go func() { claimDone <- claims.Get(pctx, "/k", storage.GetOptions{}, nil) }()
	<-r.started

	nsDone := make(chan error, 1)
	go func() { nsDone <- ns.Get(pctx, "/k", storage.GetOptions{}, nil) }()
	select {
	case err := <-nsDone:
		if err != nil {
			t.Fatalf("namespaces get: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("namespaces storage waited on the in-flight bootstrap for its own project")
	}

	close(r.release["p"])
	if err := <-claimDone; err != nil {
		t.Fatalf("resourceclaims get: %v", err)
	}
	if n := r.count("p"); n != 1 {
		t.Fatalf("expected only the CRD storage to bootstrap, got %d calls", n)
	}
}

func TestReadyAndFailedEntriesExpire(t *testing.T) {
	fail := atomic.Bool{}
	var calls atomic.Int32
	b := newNamespaceBootstrapper(time.Minute, func(context.Context, string) error {
		calls.Add(1)
		if fail.Load() {
			return errors.New("loopback unavailable")
		}
		return nil
	})
	now := time.Unix(0, 0)
	b.now = func() time.Time { return now }

	b.Ensure(context.Background(), "ok", "uid-ok")
	b.Ensure(context.Background(), "ok", "uid-ok")
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected one bootstrap while ready, got %d", n)
	}
	now = now.Add(b.readyTTL)
	b.Ensure(context.Background(), "ok", "uid-ok")
	if n := calls.Load(); n != 2 {
		t.Fatalf("expected re-ensure after the ready TTL, got %d", n)
	}

	fail.Store(true)
	b.Ensure(context.Background(), "gone", "uid-gone")
	now = now.Add(b.readyTTL)
	b.Ensure(context.Background(), "other", "uid-other")

	b.mu.Lock()
	_, failedKept := b.attempts[bootstrapKey{name: "gone", uid: "uid-gone"}]
	b.mu.Unlock()
	if failedKept {
		t.Fatal("failed attempt was not evicted after its back-off")
	}
	if _, ok := b.ready.Load(bootstrapKey{name: "ok", uid: "uid-ok"}); ok {
		t.Fatal("expired ready entry was not evicted")
	}
}

// fakeProjects is the bootstrapper's view of Projects: each name maps to the
// Project it refers to now. With all set, a name with no entry reads as a
// live Project whose UID is derived from its name; without it, as NotFound.
// With err set, every lookup fails with it.
type fakeProjects struct {
	mu       sync.Mutex
	projects map[string]projectstate.Project
	all      bool
	err      error
}

func everyProjectKnown() *fakeProjects {
	return &fakeProjects{projects: map[string]projectstate.Project{}, all: true}
}

func noProjectKnown() *fakeProjects {
	return &fakeProjects{projects: map[string]projectstate.Project{}}
}

func (f *fakeProjects) Lookup(_ context.Context, name string) (projectstate.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return projectstate.Project{}, f.err
	}
	if p, ok := f.projects[name]; ok {
		return p, nil
	}
	if f.all {
		return projectstate.Project{UID: types.UID("uid-" + name)}, nil
	}
	return projectstate.Project{}, apierrors.NewNotFound(projectstate.ProjectGVR.GroupResource(), name)
}

// LiveFromView answers from the view alone: a name with a live entry, or any
// name with all set; never a deleting entry, and nothing when err is set.
func (f *fakeProjects) LiveFromView(name string) (projectstate.Project, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return projectstate.Project{}, false
	}
	if p, ok := f.projects[name]; ok {
		return p, !p.Deleting
	}
	return projectstate.Project{UID: types.UID("uid-" + name)}, f.all
}

func (f *fakeProjects) set(name string, p projectstate.Project) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[name] = p
}

func countingRun(calls *atomic.Int32) func(context.Context, string) error {
	return func(context.Context, string) error {
		calls.Add(1)
		return nil
	}
}

// TestRecreatedProjectIsBootstrappedAgain covers a Project deleted and
// created again under its name while the old one's ready mark is fresh:
// the mark answers for the UID it was taken for, and only for that UID.
func TestRecreatedProjectIsBootstrappedAgain(t *testing.T) {
	var calls atomic.Int32
	b := newNamespaceBootstrapper(time.Minute, countingRun(&calls))

	b.Ensure(context.Background(), "p", "uid-1")
	b.Ensure(context.Background(), "p", "uid-1")
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected one bootstrap for uid-1, got %d", n)
	}
	b.Ensure(context.Background(), "p", "uid-2")
	if n := calls.Load(); n != 2 {
		t.Fatalf("expected the re-created project to be bootstrapped, got %d bootstraps", n)
	}
	b.Ensure(context.Background(), "p", "uid-2")
	if n := calls.Load(); n != 2 {
		t.Fatalf("expected uid-2's mark to hold, got %d bootstraps", n)
	}
}

// TestDeletingProjectIsNotBootstrapped covers a request that reaches a
// Project being deleted: milo-system is not written into a plane the
// purge is emptying, and the request itself is served.
func TestDeletingProjectIsNotBootstrapped(t *testing.T) {
	projects := noProjectKnown()
	projects.set("p", projectstate.Project{UID: "uid-1", Deleting: true})
	var calls atomic.Int32
	m := newTestMux(newNamespaceBootstrapper(time.Minute, countingRun(&calls)), projects)

	if err := m.Create(requestFor("p", "create"), "/k", nil, nil, 0); err != nil {
		t.Fatalf("expected a write into a deleting project to be served, got %v", err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("expected no bootstrap for a deleting project, got %d", n)
	}
}

// TestMissingProjectIsNotBootstrapped covers a name storage holds no Project
// for: a read is served, and nothing is written into its plane.
func TestMissingProjectIsNotBootstrapped(t *testing.T) {
	projects := noProjectKnown()
	var calls atomic.Int32
	m := newTestMux(newNamespaceBootstrapper(time.Minute, countingRun(&calls)), projects)

	if err := m.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil); err != nil {
		t.Fatalf("expected a read into a missing project to be served, got %v", err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("expected no bootstrap for a missing project, got %d", n)
	}

	projects.set("p", projectstate.Project{UID: "uid-1"})
	_ = m.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil)
	_ = m.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil)
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected one bootstrap once the project exists, then its mark, got %d", n)
	}
}

// TestUnseenProjectIsBootstrappedOnceFromItsLiveRead covers a write into a
// Project the API server's reflector has not seen yet: the live read
// gives its UID, so it is bootstrapped once and marked, not bootstrapped
// blind on every request.
func TestUnseenProjectIsBootstrappedOnceFromItsLiveRead(t *testing.T) {
	client := projectstatetest.NewClient(t)
	gets := projectstatetest.StorageAnswers(client, projectstatetest.NewProjectWithUID("p", "uid-1"), nil)
	var calls atomic.Int32
	m := newTestMux(newNamespaceBootstrapper(time.Minute, countingRun(&calls)), projectstate.NewCache(client))

	_ = m.Create(requestFor("p", "create"), "/k", nil, nil, 0)
	_ = m.Create(requestFor("p", "create"), "/k", nil, nil, 0)
	if n := gets.Load(); n != 2 {
		t.Fatalf("expected each write to read the unseen project live, got %d reads", n)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected one bootstrap for the unseen project, got %d", n)
	}
}

// TestReadOfAnUnseenProjectReadsNoStorage covers a read into a Project the
// reflector does not hold, as the controller manager's watches into a deleted
// Project are: a read refuses nothing and the view cannot bootstrap it,
// so storage is not read and nothing is bootstrapped. Its first write does.
func TestReadOfAnUnseenProjectReadsNoStorage(t *testing.T) {
	client := projectstatetest.NewClient(t)
	gets := projectstatetest.StorageAnswers(client, projectstatetest.NewProjectWithUID("p", "uid-1"), nil)
	var calls atomic.Int32
	m := newTestMux(newNamespaceBootstrapper(time.Minute, countingRun(&calls)), projectstate.NewCache(client))

	for _, verb := range []string{"get", "list", "watch"} {
		if err := m.Get(requestFor("p", verb), "/k", storage.GetOptions{}, nil); err != nil {
			t.Fatalf("expected a %s to be served, got %v", verb, err)
		}
	}
	if n, b := gets.Load(), calls.Load(); n != 0 || b != 0 {
		t.Fatalf("expected no storage read and no bootstrap for reads, got %d reads and %d bootstraps", n, b)
	}
}

// TestUnseenDeletingProjectIsNotBootstrapped covers a write into a Project
// the reflector has not seen and storage shows deleting: the write is
// served, and milo-system is not written into a plane the purge is emptying.
func TestUnseenDeletingProjectIsNotBootstrapped(t *testing.T) {
	deleting := projectstatetest.NewProjectWithUID("p", "uid-1")
	deleting.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	deleting.SetFinalizers([]string{"example.com/finalizer"})
	var calls atomic.Int32
	m := newTestMux(newNamespaceBootstrapper(time.Minute, countingRun(&calls)),
		projectstate.NewCache(projectstatetest.NewClient(t, deleting)))

	if err := m.Create(requestFor("p", "create"), "/k", nil, nil, 0); err != nil {
		t.Fatalf("expected a write into a deleting project to be served, got %v", err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("expected no bootstrap for a deleting project, got %d", n)
	}
}

// TestRecreatedNameIsBootstrappedAfterItsDelete covers a Project deleted and
// created again while the reflector still shows the old UID live:
// once the delete is recorded, the next write reads storage, and the new
// Project is bootstrapped although the old one's mark is fresh.
func TestRecreatedNameIsBootstrappedAfterItsDelete(t *testing.T) {
	c, client := projectstatetest.NewCache(t, projectstatetest.NewProjectWithUID("p", "uid-1"))
	var calls atomic.Int32
	m := newTestMux(newNamespaceBootstrapper(time.Minute, countingRun(&calls)), c)
	waitForUID(t, c, "p", "uid-1")
	_ = m.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil)

	projectstatetest.StorageAnswers(client, projectstatetest.NewProjectWithUID("p", "uid-2"), nil)
	c.ForceLiveLookup("p")
	_ = m.Create(requestFor("p", "create"), "/k", nil, nil, 0)
	if n := calls.Load(); n != 2 {
		t.Fatalf("expected the re-created project to be bootstrapped, got %d bootstraps", n)
	}
}

// TestReadOfAForcedNameIsNotBootstrapped covers a read into a Project whose
// DELETE was recorded while the reflector still shows it live: the view
// cannot say which Project the name refers to, and a read decides nothing
// else, so storage is not read and nothing is bootstrapped.
func TestReadOfAForcedNameIsNotBootstrapped(t *testing.T) {
	c, client := projectstatetest.NewCache(t, projectstatetest.NewProjectWithUID("p", "uid-1"))
	var calls atomic.Int32
	m := newTestMux(newNamespaceBootstrapper(time.Minute, countingRun(&calls)), c)
	waitForUID(t, c, "p", "uid-1")
	gets := projectstatetest.StorageAnswers(client, projectstatetest.NewProjectWithUID("p", "uid-2"), nil)

	c.ForceLiveLookup("p")
	_ = m.Get(requestFor("p", "watch"), "/k", storage.GetOptions{}, nil)
	if n, b := gets.Load(), calls.Load(); n != 0 || b != 0 {
		t.Fatalf("expected no storage read and no bootstrap, got %d reads and %d bootstraps", n, b)
	}
}

// waitForUID polls the reflector until it shows name with uid, since the
// fake client delivers its initial List asynchronously.
func waitForUID(t *testing.T, c *projectstate.Cache, name string, uid types.UID) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for p, _ := c.Project(name); p.UID != uid; p, _ = c.Project(name) {
		if time.Now().After(deadline) {
			t.Fatalf("the reflector never showed %s as %s", name, uid)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// requestFor returns the context of a request with verb into project's plane.
func requestFor(project, verb string) context.Context {
	ctx := request.WithProject(context.Background(), project)
	return genericapirequest.WithRequestInfo(ctx, &genericapirequest.RequestInfo{IsResourceRequest: true, Verb: verb})
}

// TestWriteIntoAMissingProjectIsRefused covers a create, update or patch into
// the plane of a Project storage does not hold, which upstream's
// NamespaceLifecycle refuses for a missing namespace: before, it bootstrapped
// milo-system there and stored the write under a gone Project.
func TestWriteIntoAMissingProjectIsRefused(t *testing.T) {
	var calls atomic.Int32
	m := newTestMux(newNamespaceBootstrapper(time.Minute, countingRun(&calls)), noProjectKnown())

	if err := m.Create(requestFor("p", "create"), "/k", nil, nil, 0); !apierrors.IsNotFound(err) {
		t.Fatalf("expected a create to be refused NotFound, got %v", err)
	}
	for _, verb := range []string{"update", "patch"} {
		err := m.GuaranteedUpdate(requestFor("p", verb), "/k", nil, false, nil, nil, nil)
		if !apierrors.IsNotFound(err) {
			t.Fatalf("expected an %s to be refused NotFound, got %v", verb, err)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("expected no bootstrap, got %d", n)
	}
}

// TestReadIntoAMissingProjectIsServed covers a read or a delete into the
// plane of a Project storage does not hold: it is not bootstrapped and
// not refused, so a teardown consumer sees what it saw before. A DELETE of an
// object with finalizers reaches storage as GuaranteedUpdate, so the
// request's verb, not the storage call, decides.
func TestReadIntoAMissingProjectIsServed(t *testing.T) {
	var calls atomic.Int32
	m := newTestMux(newNamespaceBootstrapper(time.Minute, countingRun(&calls)), noProjectKnown())

	for _, verb := range []string{"get", "list", "watch"} {
		if err := m.Get(requestFor("p", verb), "/k", storage.GetOptions{}, nil); err != nil {
			t.Fatalf("expected a %s to be served, got %v", verb, err)
		}
	}
	if err := m.GuaranteedUpdate(requestFor("p", "delete"), "/k", nil, false, nil, nil, nil); err != nil {
		t.Fatalf("expected a delete to be served, got %v", err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("expected no bootstrap, got %d", n)
	}
}

// TestFailedLookupRefusesAWriteAndServesARead covers a lookup that fails for
// any reason but NotFound: a write gets the InternalError and its
// client retries, a read is served, and neither bootstraps blind.
func TestFailedLookupRefusesAWriteAndServesARead(t *testing.T) {
	projects := noProjectKnown()
	projects.err = apierrors.NewInternalError(errors.New("loopback unavailable"))
	var calls atomic.Int32
	m := newTestMux(newNamespaceBootstrapper(time.Minute, countingRun(&calls)), projects)

	if err := m.Create(requestFor("p", "create"), "/k", nil, nil, 0); !apierrors.IsInternalError(err) {
		t.Fatalf("expected a create to be refused with an InternalError, got %v", err)
	}
	if err := m.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil); err != nil {
		t.Fatalf("expected a get to be served, got %v", err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("expected no bootstrap, got %d", n)
	}
}

// fakeChildStorage stands in for etcd as every project child's storage.
func fakeChildStorage(*storagebackend.ConfigForResource, string, func(runtime.Object) (string, error),
	func() runtime.Object, func() runtime.Object, storage.AttrFunc, storage.IndexerFuncs,
	*cache.Indexers) (storage.Interface, factory.DestroyFunc, error) {
	return fakeStorage{}, func() {}, nil
}

// TestNamespaceWriteIntoAMissingProjectIsRefused covers the kind that does
// not bootstrap: the namespaces storage has no bootstrapper, and a
// Namespace stored under a gone Project's name would reappear in a Project
// created again under it. The lookup is the mux's, so the write is refused.
func TestNamespaceWriteIntoAMissingProjectIsRefused(t *testing.T) {
	b := newNamespaceBootstrapper(time.Minute, countingRun(new(atomic.Int32)))
	ns, _, err := ProjectAwareDecorator(schema.GroupResource{Resource: "namespaces"}, fakeChildStorage, noProjectKnown(), b)(
		&storagebackend.ConfigForResource{}, "", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("decorate namespaces: %v", err)
	}

	if err := ns.Create(requestFor("p", "create"), "/k", nil, nil, 0); !apierrors.IsNotFound(err) {
		t.Fatalf("expected a Namespace create into a missing project to be refused NotFound, got %v", err)
	}
	if err := ns.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil); err != nil {
		t.Fatalf("expected a Namespace read into a missing project to be served, got %v", err)
	}
}

// capturingProvider is a native storage provider that keeps the options
// getter it is given.
type capturingProvider struct {
	getter generic.RESTOptionsGetter
}

func (*capturingProvider) GroupName() string { return "" }

func (c *capturingProvider) NewRESTStorage(_ serverstorage.APIResourceConfigSource,
	getter generic.RESTOptionsGetter) (apiserver.APIGroupInfo, error) {
	c.getter = getter
	return apiserver.APIGroupInfo{}, nil
}

// emptyOptionsGetter answers every kind with an empty storage configuration.
type emptyOptionsGetter struct{}

func (emptyOptionsGetter) GetRESTOptions(schema.GroupResource, runtime.Object) (generic.RESTOptions, error) {
	return generic.RESTOptions{StorageConfig: &storagebackend.ConfigForResource{}}, nil
}

// TestCoreProviderWriteIntoAMissingProjectIsRefused covers the path the core
// kinds take: every native storage provider is wrapped by WrapProvider,
// whose getter carries no bootstrapper. A ConfigMap write into a missing
// Project is refused there too.
func TestCoreProviderWriteIntoAMissingProjectIsRefused(t *testing.T) {
	provider := &capturingProvider{}
	if _, err := WrapProvider(provider, noProjectKnown()).NewRESTStorage(nil, emptyOptionsGetter{}); err != nil {
		t.Fatalf("NewRESTStorage: %v", err)
	}
	getter, ok := provider.getter.(roGetter)
	if !ok {
		t.Fatalf("expected the provider to receive the project-aware getter, got %T", provider.getter)
	}
	getter.storage = fakeChildStorage
	opts, err := getter.GetRESTOptions(schema.GroupResource{Resource: "configmaps"}, nil)
	if err != nil {
		t.Fatalf("GetRESTOptions: %v", err)
	}
	configMaps, _, err := opts.Decorator(opts.StorageConfig, "", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("decorate configmaps: %v", err)
	}

	if err := configMaps.Create(requestFor("p", "create"), "/k", nil, nil, 0); !apierrors.IsNotFound(err) {
		t.Fatalf("expected a ConfigMap create into a missing project to be refused NotFound, got %v", err)
	}
}

// lookupForbidden fails the test if anything looks a Project up where no
// lookup is wanted.
type lookupForbidden struct{ t *testing.T }

func (l lookupForbidden) Lookup(_ context.Context, name string) (projectstate.Project, error) {
	l.t.Fatalf("looked up %q where no lookup is wanted", name)
	return projectstate.Project{}, nil
}

func (l lookupForbidden) LiveFromView(name string) (projectstate.Project, bool) {
	l.t.Fatalf("asked the view for %q where no lookup is wanted", name)
	return projectstate.Project{}, false
}

// TestRootRequestNeverLooksUp covers the lookup's own read: a request
// with no project, as the GET of a Project is, never reaches the lookup, so
// the lookup cannot recurse.
func TestRootRequestNeverLooksUp(t *testing.T) {
	var calls atomic.Int32
	m := newTestMux(newNamespaceBootstrapper(time.Minute, countingRun(&calls)), lookupForbidden{t})
	root := genericapirequest.WithRequestInfo(context.Background(),
		&genericapirequest.RequestInfo{IsResourceRequest: true, Verb: "create"})

	if err := m.Create(root, "/k", nil, nil, 0); err != nil {
		t.Fatalf("root create: %v", err)
	}
	if err := m.Get(context.Background(), "/k", storage.GetOptions{}, nil); err != nil {
		t.Fatalf("root get: %v", err)
	}
}

// TestReadOfAKindThatDoesNotBootstrapNeverLooksUp keeps the lookup to the
// requests whose answer is used: a read or a delete of a kind with no
// bootstrapper refuses nothing and bootstraps nothing, so it must not pay a
// live read. A write of that kind still looks its Project up.
func TestReadOfAKindThatDoesNotBootstrapNeverLooksUp(t *testing.T) {
	m := newTestMux(nil, lookupForbidden{t})

	for _, verb := range []string{"get", "list", "watch"} {
		if err := m.Get(requestFor("p", verb), "/k", storage.GetOptions{}, nil); err != nil {
			t.Fatalf("expected a %s to be served, got %v", verb, err)
		}
	}
	if err := m.GuaranteedUpdate(requestFor("p", "delete"), "/k", nil, false, nil, nil, nil); err != nil {
		t.Fatalf("expected a delete to be served, got %v", err)
	}

	m.projects = noProjectKnown()
	if err := m.Create(requestFor("p", "create"), "/k", nil, nil, 0); !apierrors.IsNotFound(err) {
		t.Fatalf("expected a write of that kind to be looked up and refused, got %v", err)
	}
}

// TestRefusedWritesAreCounted keeps the volume of refusals visible, since the
// log line for them is rate-limited: every refused write counts once, by
// reason.
func TestRefusedWritesAreCounted(t *testing.T) {
	projects := noProjectKnown()
	m := newTestMux(newNamespaceBootstrapper(time.Minute, countingRun(new(atomic.Int32))), projects)
	m.args.resourceKind = "refusal-counts"
	count := func(reason string) float64 {
		n, err := testutil.GetCounterMetricValue(
			unknownProjectWritesRefused.WithLabelValues("quota.miloapis.com", "refusal-counts", reason))
		if err != nil {
			t.Fatalf("read the counter: %v", err)
		}
		return n
	}

	_ = m.Create(requestFor("p", "create"), "/k", nil, nil, 0)
	_ = m.Create(requestFor("p", "create"), "/k", nil, nil, 0)
	_ = m.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil)
	projects.err = apierrors.NewInternalError(errors.New("loopback unavailable"))
	_ = m.Create(requestFor("p", "create"), "/k", nil, nil, 0)

	if got := [2]float64{count("NotFound"), count("InternalError")}; got != [2]float64{2, 1} {
		t.Fatalf("expected 2 NotFound and 1 InternalError refusals, got %v", got)
	}
}

// TestRecreatedProjectDoesNotWaitOutTheOldBackOff covers a name re-created
// while the old Project's failed attempt is in its back-off: the new
// Project gets its own bootstrap at once.
func TestRecreatedProjectDoesNotWaitOutTheOldBackOff(t *testing.T) {
	var calls atomic.Int32
	b := newNamespaceBootstrapper(time.Minute, func(context.Context, string) error {
		if calls.Add(1) == 1 {
			return errors.New("loopback unavailable")
		}
		return nil
	})

	b.Ensure(context.Background(), "p", "uid-1")
	b.Ensure(context.Background(), "p", "uid-1")
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected uid-1's failed attempt to back off, got %d attempts", n)
	}
	b.Ensure(context.Background(), "p", "uid-2")
	if n := calls.Load(); n != 2 {
		t.Fatalf("expected uid-2 to be bootstrapped inside uid-1's back-off, got %d attempts", n)
	}
}

// TestRecreatedProjectDoesNotJoinTheOldAttempt covers a name re-created
// while the old Project's attempt is still in flight: the new Project
// starts its own attempt rather than waiting on the old one.
func TestRecreatedProjectDoesNotJoinTheOldAttempt(t *testing.T) {
	projects := noProjectKnown()
	r := newBlockingRun("p")
	m := newTestMux(newNamespaceBootstrapper(time.Minute, r.run), projects)
	t.Cleanup(func() { close(r.release["p"]) })

	projects.set("p", projectstate.Project{UID: "uid-1"})
	first := getAsync(m, "p")
	<-r.started

	projects.set("p", projectstate.Project{UID: "uid-2"})
	second := getAsync(m, "p")
	select {
	case <-r.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the re-created project joined the old project's attempt")
	}
	if n := r.count("p"); n != 2 {
		t.Fatalf("expected two attempts for the name, got %d", n)
	}
	select {
	case <-first:
		t.Fatal("uid-1's request returned before its attempt finished")
	case <-second:
		t.Fatal("uid-2's request returned before its attempt finished")
	default:
	}
}
