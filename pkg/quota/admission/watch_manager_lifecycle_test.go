package admission

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

// gap returns the time from watch i's opening to watch i+1's.
func (c *watchedClient) gap(t *testing.T, i int) time.Duration {
	t.Helper()
	c.stream(t, i+1)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.openedAt[i+1].Sub(c.openedAt[i])
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

// claimReads counts the manager's GETs of ResourceClaims.
func (c *watchedClient) claimReads() int {
	reads := 0
	for _, action := range c.Actions() {
		if action.GetVerb() == "get" && action.GetResource().Resource == "resourceclaims" {
			reads++
		}
	}
	return reads
}

func (c *watchedClient) createClaim(t *testing.T, claim *unstructured.Unstructured) {
	t.Helper()
	claims := c.Resource(quotav1alpha1.GroupVersion.WithResource("resourceclaims")).Namespace(claim.GetNamespace())
	if _, err := claims.Create(context.Background(), claim, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func (c *watchedClient) updateClaim(t *testing.T, claim *unstructured.Unstructured) {
	t.Helper()
	claims := c.Resource(quotav1alpha1.GroupVersion.WithResource("resourceclaims")).Namespace(claim.GetNamespace())
	if _, err := claims.Update(context.Background(), claim, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

// newWaiterOnPendingClaim registers a waiter, creates its claim pending, and
// binds the waiter, in the order a create request does.
func newWaiterOnPendingClaim(t *testing.T, w *watchManager, client *watchedClient) ClaimWaiter {
	t.Helper()
	waiter := newBoundWaiter(t, w, "claim", "")
	pending := existingClaim("claim", "default", nil, "", "")
	pending.SetUID("own")
	client.createClaim(t, pending)
	waiter.Bind("own")
	return waiter
}

// newTestWatchManager starts a manager whose backoff starts at initialDelay.
func newTestWatchManager(t *testing.T, client *watchedClient, initialDelay time.Duration) *watchManager {
	t.Helper()
	w := NewWatchManager(client, logr.Discard(), "project").(*watchManager)
	w.config.Retry.InitialDelay = initialDelay
	w.config.Retry.MaxDelay = time.Second
	if err := w.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return w
}

func newBoundWaiter(t *testing.T, w *watchManager, claimName string, uid string) ClaimWaiter {
	t.Helper()
	waiter, err := w.RegisterClaimWaiter(context.Background(), claimName, "default", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if uid != "" {
		waiter.Bind(types.UID(uid))
	}
	return waiter
}

func awaitResult(t *testing.T, waiter ClaimWaiter, lost string) ClaimResult {
	t.Helper()
	select {
	case result := <-waiter.Result():
		return result
	case <-time.After(2 * time.Second):
		t.Fatal(lost)
		return ClaimResult{}
	}
}

func bookmark(resourceVersion string) *unstructured.Unstructured {
	b := &unstructured.Unstructured{}
	b.SetAPIVersion("quota.miloapis.com/v1alpha1")
	b.SetKind("ResourceClaim")
	b.SetResourceVersion(resourceVersion)
	return b
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

// TestNormalCloseReconnectsAtOnce checks that a stream the server ends at its
// watch timeout is reopened at once, from its last bookmark. Were a normal
// close counted as a failure, the delay would double at every close, and each
// reconnect would leave the project unwatched for up to MaxDelay.
func TestNormalCloseReconnectsAtOnce(t *testing.T) {
	client := newWatchedClient(watchScript{})
	w := newTestWatchManager(t, client, 50*time.Millisecond)
	defer w.Stop()

	const closes = 6
	began := time.Now()
	for i := range closes {
		stream, _ := client.stream(t, i)
		stream.Action(watch.Bookmark, bookmark(fmt.Sprint(100+i)))
		stream.Stop()
		if _, from := client.stream(t, i+1); from != fmt.Sprint(100+i) {
			t.Fatalf("watch %d started from %q, want the bookmark %d", i+1, from, 100+i)
		}
	}
	// With the backoff applied, six closes cost at least 1.9 s (50 ms doubling, -25 % jitter).
	if took := time.Since(began); took > 500*time.Millisecond {
		t.Fatalf("%d normal closes took %v to reconnect, want no delay", closes, took)
	}
}

// TestIdleStreamEndedAfterASecondReconnectsAtOnce checks that a stream the
// server ends with no event, after it lived for more than a second, counts as
// a normal close: an idle project's stream carries no event for its whole life.
func TestIdleStreamEndedAfterASecondReconnectsAtOnce(t *testing.T) {
	client := newWatchedClient(watchScript{})
	w := newTestWatchManager(t, client, 500*time.Millisecond)
	defer w.Stop()

	stream, _ := client.stream(t, 0)
	time.Sleep(minHealthyStreamLifetime + 100*time.Millisecond)
	stream.Stop()
	// A backoff would wait 375 ms or more (500 ms, -25 % jitter).
	if gap := client.gap(t, 0) - minHealthyStreamLifetime - 100*time.Millisecond; gap > 200*time.Millisecond {
		t.Fatalf("an idle stream's normal close waited %v to reconnect, want no delay", gap)
	}
}

// TestFailingStreamBacksOff checks that a stream which ends before its first
// event is retried with a growing delay, so a failing server is not hammered,
// and is retried at all.
func TestFailingStreamBacksOff(t *testing.T) {
	client := newWatchedClient(watchScript{endAtOnce: func(int) bool { return true }})
	w := newTestWatchManager(t, client, 50*time.Millisecond)
	defer w.Stop()

	time.Sleep(400 * time.Millisecond)
	// Delays of 50, 100, 200 ms (±25 %) leave room for two to five opens.
	if n := client.opens(); n < 2 || n > 5 {
		t.Fatalf("%d watches opened in 400 ms against a failing server, want 2 to 5", n)
	}
}

// TestHealthyCloseResetsTheBackoff checks that a healthy stream after a
// failing period resets the delay, so the next failure waits the initial delay
// and not the delay the failing period reached.
func TestHealthyCloseResetsTheBackoff(t *testing.T) {
	const healthy = 3 // streams 0-2 fail and grow the delay to 400 ms; stream 3 is healthy
	client := newWatchedClient(watchScript{endAtOnce: func(i int) bool { return i != healthy }})
	w := newTestWatchManager(t, client, 50*time.Millisecond)
	defer w.Stop()

	stream, _ := client.stream(t, healthy)
	stream.Action(watch.Bookmark, bookmark("100"))
	stream.Stop()
	// Stream 4 fails at once. Without the reset it waits 300 ms or more (400 ms, -25 % jitter).
	if gap := client.gap(t, healthy+1); gap > 200*time.Millisecond {
		t.Fatalf("the first failure after a healthy stream waited %v, want the initial delay", gap)
	}
}

// TestReconnectFromNowRereadsWaitingClaims checks that a waiter whose claim is
// judged while the manager has no stream still gets the outcome. A stream that
// ends before its first event or bookmark leaves no resume point, so the next
// one starts from now and carries no event from the gap.
func TestReconnectFromNowRereadsWaitingClaims(t *testing.T) {
	client := newWatchedClient(watchScript{})
	w := newTestWatchManager(t, client, 50*time.Millisecond)
	defer w.Stop()
	waiter := newWaiterOnPendingClaim(t, w, client)

	first, _ := client.stream(t, 0)
	// The grant lands while the stream carries no event of it, and the stream then ends before its first event.
	client.updateClaim(t, judgedClaim("claim", "default", "own", true, quotav1alpha1.ResourceClaimGrantedReason))
	first.Stop()

	if _, from := client.stream(t, 1); from != "" {
		t.Fatalf("watch 1 started from %q, want from now", from)
	}
	if result := awaitResult(t, waiter, "the grant made while the manager had no stream never reached its waiter"); !result.Granted {
		t.Fatalf("got %+v, want the grant", result)
	}
}

// TestReconnectAfter410RereadsWaitingClaims checks the route production takes
// to a stream from now: the resume point is too old (410 Gone), the manager
// drops it, and a grant made before the new stream must still arrive.
func TestReconnectAfter410RereadsWaitingClaims(t *testing.T) {
	client := newWatchedClient(watchScript{})
	w := newTestWatchManager(t, client, 50*time.Millisecond)
	defer w.Stop()
	waiter := newWaiterOnPendingClaim(t, w, client)

	first, _ := client.stream(t, 0)
	first.Action(watch.Bookmark, bookmark("100"))
	client.updateClaim(t, judgedClaim("claim", "default", "own", true, quotav1alpha1.ResourceClaimGrantedReason))
	first.Error(&metav1.Status{Status: metav1.StatusFailure, Code: http.StatusGone, Reason: metav1.StatusReasonExpired})

	if _, from := client.stream(t, 1); from != "" {
		t.Fatalf("watch 1 after a 410 started from %q, want from now", from)
	}
	if result := awaitResult(t, waiter, "the grant made before the 410's new stream never reached its waiter"); !result.Granted {
		t.Fatalf("got %+v, want the grant", result)
	}
}

// TestResumedStreamReadsNoClaim checks that a stream which resumes from a
// bookmark reads no waiting claim: it replays every event since the bookmark.
func TestResumedStreamReadsNoClaim(t *testing.T) {
	client := newWatchedClient(watchScript{})
	w := newTestWatchManager(t, client, 50*time.Millisecond)
	defer w.Stop()
	pending := existingClaim("claim", "default", nil, "", "")
	pending.SetUID("own")
	client.createClaim(t, pending)
	newBoundWaiter(t, w, "claim", "own")

	const closes = 3
	for i := range closes {
		stream, _ := client.stream(t, i)
		stream.Action(watch.Bookmark, bookmark(fmt.Sprint(100+i)))
		stream.Stop()
	}
	client.stream(t, closes)
	time.Sleep(100 * time.Millisecond) // a read runs in its own goroutine
	// The first stream starts from now and may read the claim once.
	if n := client.claimReads(); n > 1 {
		t.Fatalf("%d reads of the waiting claim over %d resumed streams, want none", n-1, closes)
	}
}

// TestRereadEndsABoundWaiterWhoseClaimIsGone checks that a bound waiter whose
// claim is not found on the re-read ends as deleted: its request created the
// claim, so the claim was deleted while the manager had no stream, as a denied
// claim is by its cleanup. An unbound waiter's claim may not exist yet, and
// that waiter waits for the stream.
func TestRereadEndsABoundWaiterWhoseClaimIsGone(t *testing.T) {
	client := newWatchedClient(watchScript{})
	w := newTestWatchManager(t, client, 50*time.Millisecond)
	defer w.Stop()
	bound := newBoundWaiter(t, w, "deleted-claim", "own")
	unbound := newBoundWaiter(t, w, "unborn-claim", "")

	first, _ := client.stream(t, 0)
	first.Stop()

	if result := awaitResult(t, bound, "a waiter whose claim was deleted in the gap waited to its timeout"); result.Reason != "deleted" {
		t.Fatalf("got %+v, want deleted", result)
	}
	time.Sleep(100 * time.Millisecond) // the re-read reads the waiters in no fixed order
	if result, ok := pendingResult(unbound); ok || heldCount(w, unbound) != 0 {
		t.Fatalf("a waiter whose claim is not created yet was given an outcome: %+v", result)
	}
}
