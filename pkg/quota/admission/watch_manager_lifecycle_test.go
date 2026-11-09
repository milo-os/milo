package admission

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// watchedClient is a fake dynamic client whose ResourceClaim watches the test
// drives: each watch the manager opens is recorded with the resource version
// it starts from and the time it opened, and the test sends its events and
// ends it.
type watchedClient struct {
	*fake.FakeDynamicClient

	mu       sync.Mutex
	attempts int // every Watch call, also the ones that fail
	streams  []*watch.FakeWatcher
	from     []string
	openedAt []time.Time
	opened   chan struct{}
}

// watchScript says how the fake server answers the manager's watches.
type watchScript struct {
	openDelay time.Duration    // how long a watch takes to open, as on a busy API server
	failFirst int              // how many first Watch calls return an error
	endAtOnce func(i int) bool // whether stream i ends before its first event, as a failing server's does
}

func newWatchedClient(script watchScript) *watchedClient {
	scheme := runtime.NewScheme()
	if err := quotav1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	c := &watchedClient{FakeDynamicClient: fake.NewSimpleDynamicClient(scheme), opened: make(chan struct{}, 1000)}
	c.PrependWatchReactor("resourceclaims", func(action clienttesting.Action) (bool, watch.Interface, error) {
		time.Sleep(script.openDelay)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.attempts++
		if c.attempts <= script.failFirst {
			return true, nil, errors.New("the server is unavailable")
		}
		stream := watch.NewFakeWithChanSize(10, false)
		if script.endAtOnce != nil && script.endAtOnce(len(c.streams)) {
			stream.Stop()
		}
		c.streams = append(c.streams, stream)
		c.from = append(c.from, action.(clienttesting.WatchActionImpl).WatchRestrictions.ResourceVersion)
		c.openedAt = append(c.openedAt, time.Now())
		select {
		case c.opened <- struct{}{}:
		default: // a manager in a hot loop must not block the fake server
		}
		return true, stream, nil
	})
	return c
}

// stream waits for the manager to open its i-th watch and returns it, with the
// resource version it started from.
func (c *watchedClient) stream(t *testing.T, i int) (*watch.FakeWatcher, string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		c.mu.Lock()
		if len(c.streams) > i {
			defer c.mu.Unlock()
			return c.streams[i], c.from[i]
		}
		c.mu.Unlock()
		select {
		case <-c.opened:
		case <-deadline:
			t.Fatalf("the manager did not open watch %d", i)
		}
	}
}

func (c *watchedClient) opens() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.streams)
}

func (c *watchedClient) watchAttempts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts
}

func newTestPlugin(client *watchedClient) *ResourceQuotaEnforcementPlugin {
	return &ResourceQuotaEnforcementPlugin{dynamicClient: client, config: DefaultAdmissionPluginConfig(), logger: logr.Discard()}
}

// TestConcurrentFirstCreatesShareOneWatchManager checks that creates which
// arrive together in a project with no watch manager start one manager. A
// manager that loses the race to the cache gets no waiter, so its TTL never
// arms and its watch runs until the process ends.
func TestConcurrentFirstCreatesShareOneWatchManager(t *testing.T) {
	client := newWatchedClient(watchScript{openDelay: 20 * time.Millisecond})
	p := newTestPlugin(client)

	const creates = 20
	managers := make([]ClaimWatchManager, creates)
	var wg sync.WaitGroup
	for i := range creates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := p.getWatchManager(context.Background())
			if err != nil {
				t.Errorf("get watch manager: %v", err)
				return
			}
			managers[i] = m
		}()
	}
	wg.Wait()
	defer managers[0].(*watchManager).Stop()

	for i, m := range managers {
		if m != managers[0] {
			t.Fatalf("create %d got another watch manager", i)
		}
	}
	if n := client.opens(); n != 1 {
		t.Fatalf("%d watch managers started for one project, want 1", n)
	}
}

// TestSharedStartReturnsAManagerCachedMeanwhile checks that a call which
// missed the cache, and reaches the shared start after an earlier start stored
// its manager, takes that manager instead of starting another one.
func TestSharedStartReturnsAManagerCachedMeanwhile(t *testing.T) {
	client := newWatchedClient(watchScript{})
	p := newTestPlugin(client)
	cached := NewWatchManager(client, logr.Discard(), "")
	p.watchManagers.Store("", cached)

	m, err := p.cachedOrStartedWatchManager("")
	if err != nil {
		t.Fatal(err)
	}
	if m != cached || client.watchAttempts() != 0 {
		t.Fatalf("started a second manager (%d watches) beside the cached one", client.watchAttempts())
	}
}

// TestWatchManagerWithoutWaiterExpires checks that a manager no request waits
// on still stops after its TTL. The TTL arms when the last waiter leaves; a
// manager that never had one would otherwise watch forever.
func TestWatchManagerWithoutWaiterExpires(t *testing.T) {
	w := NewWatchManager(newWatchedClient(watchScript{}), logr.Discard(), "project").(*watchManager)
	w.config.TTL.DefaultTTL = 50 * time.Millisecond
	expired := make(chan struct{})
	w.SetTTLExpiredCallback(func() { close(expired) })
	if err := w.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	select {
	case <-expired:
	case <-time.After(2 * time.Second):
		w.Stop()
		t.Fatal("a watch manager with no waiter did not expire")
	}
}

// TestFailedStartStopsItsWatchLoop checks that a manager whose first watch
// fails stops trying. The plugin does not cache it and its TTL is not armed,
// so a loop left running would open a stream that nothing can stop.
func TestFailedStartStopsItsWatchLoop(t *testing.T) {
	client := newWatchedClient(watchScript{failFirst: 1})
	w := NewWatchManager(client, logr.Discard(), "project").(*watchManager)
	w.config.Retry.InitialDelay = 10 * time.Millisecond
	if err := w.Start(context.Background()); err == nil {
		t.Fatal("the start succeeded although its first watch failed")
	}

	time.Sleep(200 * time.Millisecond)
	if n := client.watchAttempts(); n != 1 {
		t.Fatalf("the failed manager made %d watch calls, want 1", n)
	}
}

// TestExpiredManagerKeepsANewerCachedManager checks that a manager's TTL
// callback removes that manager from the cache, not a newer manager of the
// same project. A removed manager keeps its watch, and the next create starts
// another beside it.
func TestExpiredManagerKeepsANewerCachedManager(t *testing.T) {
	client := newWatchedClient(watchScript{})
	p := newTestPlugin(client)
	older, err := p.getWatchManager(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer older.(*watchManager).Stop()
	newer := NewWatchManager(client, logr.Discard(), "")
	p.watchManagers.Store("", newer)

	older.(*watchManager).onTTLExpired()
	if cached, ok := p.watchManagers.Load(""); !ok || cached != newer {
		t.Fatal("an older manager's TTL removed the newer manager from the cache")
	}
}
