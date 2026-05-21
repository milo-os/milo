package projectprovider

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

// fakeSink records AddProject/RemoveProject calls and can simulate failures.
type fakeSink struct {
	mu       sync.Mutex
	added    map[string]int // project → number of AddProject calls
	removed  []string
	failNext int32 // atomic: number of remaining AddProject failures
}

func newFakeSink() *fakeSink {
	return &fakeSink{added: make(map[string]int)}
}

func (s *fakeSink) AddProject(_ context.Context, id string, _ *rest.Config) error {
	if atomic.AddInt32(&s.failNext, -1) >= 0 {
		return fmt.Errorf("simulated failure for %s", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.added[id]++
	return nil
}

func (s *fakeSink) RemoveProject(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removed = append(s.removed, id)
}

func (s *fakeSink) addCount(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.added[id]
}

func testProvider(sink Sink) *Provider {
	return &Provider{
		root: &rest.Config{Host: "https://example.com"},
		sink: sink,
		cfg:  DefaultConfig(),
	}
}

func TestProcessProject_RetriesOnFailure(t *testing.T) {
	sink := newFakeSink()
	atomic.StoreInt32(&sink.failNext, 3)

	p := testProvider(sink)
	queue := newTestQueue()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		p.processProject(ctx, queue, "test-project")
		if queue.NumRequeues("test-project") != i+1 {
			t.Fatalf("attempt %d: expected %d requeues, got %d",
				i, i+1, queue.NumRequeues("test-project"))
		}
	}

	// 4th attempt should succeed
	p.processProject(ctx, queue, "test-project")
	if sink.addCount("test-project") != 1 {
		t.Fatalf("expected 1 successful add, got %d", sink.addCount("test-project"))
	}
}

func TestProcessProject_GivesUpAfterMaxRetries(t *testing.T) {
	sink := newFakeSink()
	p := testProvider(sink)
	atomic.StoreInt32(&sink.failNext, int32(p.cfg.MaxRetries)+10)

	queue := newTestQueue()
	ctx := context.Background()

	for i := 0; i <= p.cfg.MaxRetries; i++ {
		p.processProject(ctx, queue, "test-project")
	}

	if queue.forgotten["test-project"] != 1 {
		t.Fatalf("expected project to be forgotten after %d retries", p.cfg.MaxRetries)
	}
	if sink.addCount("test-project") != 0 {
		t.Fatal("project should never have been successfully added")
	}
}

func TestProcessProject_RespectsCustomConfig(t *testing.T) {
	sink := newFakeSink()
	atomic.StoreInt32(&sink.failNext, 100)

	p := &Provider{
		root: &rest.Config{Host: "https://example.com"},
		sink: sink,
		cfg:  Config{MaxRetries: 3},
	}

	queue := newTestQueue()
	ctx := context.Background()

	// 3 retries + 1 final = 4 total calls to give up
	for i := 0; i <= 3; i++ {
		p.processProject(ctx, queue, "test-project")
	}

	if queue.forgotten["test-project"] != 1 {
		t.Fatal("expected project to be forgotten after custom MaxRetries=3")
	}
}

// testQueue is a minimal mock of workqueue.TypedRateLimitingInterface for
// unit testing processProject without the async queue machinery.
type testQueue struct {
	requeues  map[string]int
	forgotten map[string]int
}

func newTestQueue() *testQueue {
	return &testQueue{
		requeues:  make(map[string]int),
		forgotten: make(map[string]int),
	}
}

func (q *testQueue) Add(string)                                   {}
func (q *testQueue) Len() int                                     { return 0 }
func (q *testQueue) Get() (string, bool)                          { return "", true }
func (q *testQueue) Done(string)                                  {}
func (q *testQueue) ShutDown()                                    {}
func (q *testQueue) ShutDownWithDrain()                           {}
func (q *testQueue) ShuttingDown() bool                           { return false }
func (q *testQueue) AddAfter(item string, duration time.Duration) {}
func (q *testQueue) AddRateLimited(item string)                   { q.requeues[item]++ }
func (q *testQueue) Forget(item string)                           { q.forgotten[item]++ }
func (q *testQueue) NumRequeues(item string) int                  { return q.requeues[item] }
