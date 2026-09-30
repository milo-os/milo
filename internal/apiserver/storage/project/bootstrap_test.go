package projectstorage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage"
	storagebackend "k8s.io/apiserver/pkg/storage/storagebackend"
	factory "k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/client-go/tools/cache"

	"go.miloapis.com/milo/pkg/request"
)

type fakeStorage struct {
	storage.Interface
}

func (fakeStorage) Versioner() storage.Versioner { return storage.APIObjectVersioner{} }

func (fakeStorage) Get(context.Context, string, storage.GetOptions, runtime.Object) error {
	return nil
}

func newTestMux(b *NamespaceBootstrapper) *projectMux {
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
		bootstrapper: b,
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
	m := newTestMux(newNamespaceBootstrapper(time.Minute, r.run))

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
	muxes := []*projectMux{newTestMux(b), newTestMux(b)}

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
	m := newTestMux(newNamespaceBootstrapper(50*time.Millisecond, r.run))

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
	go b.Ensure(context.Background(), "p")
	<-r.started

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		b.Ensure(ctx, "p")
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

	b.Ensure(context.Background(), "p")
	b.Ensure(context.Background(), "p")
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected no retry inside back-off, got %d calls", n)
	}

	now = now.Add(b.retryAfter)
	b.Ensure(context.Background(), "p")
	if n := calls.Load(); n != 2 {
		t.Fatalf("expected a retry after back-off, got %d calls", n)
	}
}

func TestNilBootstrapperAndRootProjectSkipBootstrap(t *testing.T) {
	var nilB *NamespaceBootstrapper
	nilB.Ensure(context.Background(), "p")

	var calls atomic.Int32
	b := newNamespaceBootstrapper(time.Minute, func(context.Context, string) error {
		calls.Add(1)
		return nil
	})
	m := newTestMux(b)
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
			b.Ensure(context.Background(), "p")
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
	b.Ensure(context.Background(), "p")
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
		b.Ensure(ctx, "p")
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
		b.Ensure(context.Background(), "p")
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
	ns, _, err := ProjectAwareDecorator(schema.GroupResource{Resource: "namespaces"}, inner, b)(
		cfg, "", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("decorate namespaces: %v", err)
	}
	claims, _, err := ProjectAwareDecorator(schema.GroupResource{Group: "quota.miloapis.com", Resource: "resourceclaims"}, inner, b)(
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

	b.Ensure(context.Background(), "ok")
	b.Ensure(context.Background(), "ok")
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected one bootstrap while ready, got %d", n)
	}
	now = now.Add(b.readyTTL)
	b.Ensure(context.Background(), "ok")
	if n := calls.Load(); n != 2 {
		t.Fatalf("expected re-ensure after the ready TTL, got %d", n)
	}

	fail.Store(true)
	b.Ensure(context.Background(), "gone")
	now = now.Add(b.readyTTL)
	b.Ensure(context.Background(), "other")

	b.mu.Lock()
	_, failedKept := b.attempts["gone"]
	b.mu.Unlock()
	if failedKept {
		t.Fatal("failed attempt was not evicted after its back-off")
	}
	if _, ok := b.ready.Load("ok"); ok {
		t.Fatal("expired ready entry was not evicted")
	}
}
