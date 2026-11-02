package cockroach

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/cache"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/apis/example"
	"k8s.io/apiserver/pkg/storage"
)

func revs(events []*feedEvent) []int64 {
	var out []int64
	for _, e := range events {
		out = append(out, e.rev)
	}
	return out
}

func TestSequencerReleasesInOrder(t *testing.T) {
	now := time.Now()
	for _, order := range [][]int64{{0, 2, 4, 3}, {4, 3, 2, 0}, {3, 0, 4, 2}} {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			s := newSequencer(initialRevision, defaultReplayConfig())
			var released []int64
			for _, rev := range order {
				if rev == 0 {
					released = append(released, revs(s.writeCounter(4, now).released)...)
				} else {
					released = append(released, revs(s.write(&feedEvent{rev: rev}, now).released)...)
				}
			}
			require.Equal(t, []int64{2, 3, 4}, released)
			require.Empty(t, s.write(&feedEvent{rev: 3}, now).released, "a replayed write was released twice")
		})
	}
}

// The sequencer reports how the values it holds back change, a resent copy
// of a held-back write included.
func TestSequencerAccountsForHeldBackBytes(t *testing.T) {
	now := time.Now()
	s := newSequencer(initialRevision, defaultReplayConfig())
	write := func(rev int64, value string) int {
		return s.write(&feedEvent{rev: rev, value: []byte(value)}, now).heldDelta
	}
	require.Equal(t, 3, write(3, "abc"), "held back behind revision 2")
	require.Equal(t, 2, write(3, "abcde"), "a resent copy replaces the first")
	require.Equal(t, 2, write(2, "xy"), "held back until the counter passes it")
	r := s.writeCounter(3, now)
	require.Equal(t, []int64{2, 3}, revs(r.released))
	require.Equal(t, -7, r.heldDelta)
	require.Zero(t, s.heldBackWrites())
}

// Writes held back for an earlier revision count against the hub's bound
// until they are released, and past it the hub gives up.
func TestHubBoundsHeldBackWrites(t *testing.T) {
	config := defaultHubConfig()
	config.maxHeldBackBytes = 10
	h := memoryHubWith(config)
	held := func(rev int64) error {
		return h.write(&feedRow{Project: "project", Key: fmt.Sprintf("/pods/%d", rev), RV: rev, Value: []byte("abcd")}, nil, hlc{})
	}
	require.NoError(t, held(3))
	require.NoError(t, held(4))
	require.Equal(t, 8, h.heldBackBytes)
	require.ErrorIs(t, held(5), errHeldBack)

	h.reset(nil)
	require.Zero(t, h.heldBackBytes, "a new baseline holds nothing back")
	require.NoError(t, held(3))
	require.NoError(t, held(4))
	require.NoError(t, h.write(&feedRow{Project: "project", RV: 4}, nil, hlc{}))
	require.NoError(t, held(2))
	require.Zero(t, h.heldBackBytes, "released writes leave the bound")
}

func TestEventRing(t *testing.T) {
	now := time.Now()
	limits := replayConfig{window: time.Minute, events: 8, bytes: 1 << 20}
	r := eventRing{base: 4, limits: limits}
	for rev := range int64(limits.events + 2) {
		r.push(&feedEvent{rev: 5 + rev, releasedAt: now, value: []byte("ab"), prevValue: []byte("c")})
	}
	r.trim(now)
	require.Len(t, r.events, limits.events)
	require.Equal(t, 3*limits.events, r.valueBytes)
	require.Equal(t, int64(6), r.oldest())
	require.Equal(t, int64(7), r.since(6)[0].rev)

	r.trim(now.Add(limits.window + time.Nanosecond))
	require.Empty(t, r.events)
	require.Zero(t, r.valueBytes)
	require.Equal(t, int64(limits.events+6), r.end())

	r.limits.bytes = 5
	for range 3 {
		r.push(&feedEvent{rev: r.end() + 1, releasedAt: now, value: []byte("ab"), prevValue: []byte("c")})
	}
	r.trim(now)
	require.Len(t, r.events, 1, "the oldest events go past the byte bound")
	require.Equal(t, 3, r.valueBytes)
}

func TestDecodeFeedMessage(t *testing.T) {
	msg, err := decodeFeedMessage([]byte(`{"after":{"project":"t","key":"/pods/p","rv":3,"value":"\\x0aff"},"before":null,"updated":"123.0000000001"}`))
	require.NoError(t, err)
	require.Equal(t, feedRow{Project: "t", Key: "/pods/p", RV: 3, Value: feedBytes{0x0a, 0xff}}, *msg.After)
	require.Nil(t, msg.Before)
	require.Equal(t, hlc{wallNanos: 123, logical: 1}, msg.Updated)

	for _, bad := range []string{
		`{"resolved":"1'; --"}`,
		`{"after":{"project":"t","key":"/pods/p","rv":2,"value":"plain"}}`,
		`{"after":{"project":"t","key":"/pods/p","rv":2,"value":"\\x0"}}`,
	} {
		_, err := decodeFeedMessage([]byte(bad))
		require.Error(t, err, bad)
	}
}

// memoryHub is a hub with no feed, which tests drive by hand.
func memoryHub() *hub { return memoryHubWith(defaultHubConfig()) }

func memoryHubWith(config hubConfig) *hub {
	h := newHub(revisionTimestamps{c: cache.NewExpiring(), window: time.Minute}, config)
	h.reset(nil)
	return h
}

// everything follows every key of the project.
var everything = watchTarget{project: "project", key: "/pods/", recursive: true}

func subscribe(t *testing.T, h *hub, target watchTarget, from int64) ([]*feedEvent, *subscription) {
	t.Helper()
	released, sub, err := h.subscribe(t.Context(), target, from)
	require.NoError(t, err)
	return released, sub
}

// onlySubscription is the project's one subscription.
func onlySubscription(t *testing.T, h *hub) *subscription {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	subs := h.projects["project"].watchers
	require.Len(t, subs, 1)
	for sub := range subs {
		return sub
	}
	return nil
}

func memoryWatcher(t *testing.T, h *hub) *watcher {
	s := &store{b: &Backend{hub: h}, alive: t.Context(), versioner: storage.APIObjectVersioner{}, newFunc: func() runtime.Object { return &example.Pod{} }}
	w := newWatcher(t.Context(), s, everything, storage.Everything)
	t.Cleanup(w.Stop)
	return w
}

// testEpoch is when arrive's writes commit. It's recent, so the hub records
// their timestamps.
var testEpoch = time.Now().UnixNano()

func committedAt(rev int64) hlc { return hlc{wallNanos: testEpoch, logical: int32(rev)} }

// arrive hands the hub the project's writes at revs, then its counter at high.
func arrive(t *testing.T, h *hub, high int64, revs ...int64) {
	t.Helper()
	for _, rev := range revs {
		require.NoError(t, h.write(&feedRow{Project: "project", Key: fmt.Sprintf("/pods/%d", rev), RV: rev}, nil, committedAt(rev)))
	}
	require.NoError(t, h.write(&feedRow{Project: "project", RV: high}, nil, hlc{}))
}

// span is the revisions from first to last.
func span(first, last int64) []int64 {
	var out []int64
	for rev := first; rev <= last; rev++ {
		out = append(out, rev)
	}
	return out
}

func TestHubPublishesReleasedWrites(t *testing.T) {
	h := memoryHub()
	_, sub := subscribe(t, h, everything, initialRevision)
	arrive(t, h, 3, 3, 2)
	require.Equal(t, int64(2), (<-sub.in).rev)
	require.Equal(t, int64(3), (<-sub.in).rev)
	ts, ok := h.revTS.get("project", 3)
	require.True(t, ok)
	require.Equal(t, committedAt(3), ts)
}

// A revision's commit time is recorded only once it's released, in order, so
// a replica that knows revision 3's also knows 2's. Reads at 2 rely on that
// when they fall back to the instant before 3 committed.
func TestHubRecordsCommitTimesInReleaseOrder(t *testing.T) {
	h := memoryHub()
	batch := committedAt(2)
	for _, rev := range []int64{3, 2} {
		require.NoError(t, h.write(&feedRow{Project: "project", Key: fmt.Sprintf("/pods/%d", rev), RV: rev}, nil, batch))
		for _, r := range []int64{2, 3} {
			_, ok := h.revTS.get("project", r)
			require.False(t, ok, "revision %d's commit time is known before its release", r)
		}
	}
	require.NoError(t, h.write(&feedRow{Project: "project", RV: 3}, nil, hlc{}))
	for _, r := range []int64{2, 3} {
		ts, ok := h.revTS.get("project", r)
		require.True(t, ok)
		require.Equal(t, batch, ts)
	}
}

func TestHubResetEndsWatches(t *testing.T) {
	h := memoryHub()
	_, sub := subscribe(t, h, everything, initialRevision)
	h.reset(map[string]int64{"project": 9})
	_, err := sub.next(t.Context())
	require.True(t, apierrors.IsResourceExpired(err), "%v", err)
	require.Equal(t, int64(9), h.releasedRevision("project"))
}

func TestHubAgesOutQuietProjectsEvents(t *testing.T) {
	h := memoryHub()
	arrive(t, h, 3, 2, 3)
	h.tick(time.Now().Add(h.config.replay.window + time.Second))
	seq := h.projects["project"].seq
	events, _ := seq.history()
	require.Zero(t, events)
	require.Equal(t, int64(3), seq.latest())
}

// Requests waiting for a revision when the feed resets wake and look again,
// in the projects of the new baseline.
func TestHubResetWakesWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := memoryHub()
		done := make(chan error, 1)
		go func() {
			_, err := h.waitReleased(t.Context(), "project", 5)
			done <- err
		}()
		synctest.Wait()
		require.Empty(t, done)
		h.reset(map[string]int64{"project": 5})
		synctest.Wait()
		require.Len(t, done, 1)
		require.NoError(t, <-done)
	})
}

func TestHubSubscribeFuture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := memoryHub()
		var released []*feedEvent
		done := make(chan error, 1)
		go func() {
			var err error
			released, _, err = h.subscribe(t.Context(), everything, 2)
			done <- err
		}()
		synctest.Wait()
		require.Empty(t, done, "subscription did not wait")
		arrive(t, h, 3, 2, 3)
		require.NoError(t, <-done)
		require.Equal(t, []int64{3}, revs(released))
	})
}

func TestHubSubscribeFutureTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := memoryHub()
		start := time.Now()
		_, _, err := h.subscribe(t.Context(), everything, 2)
		require.True(t, storage.IsTooLargeResourceVersion(err), "%v", err)
		require.Equal(t, h.config.awaitTimeout, time.Since(start))
	})
}

func TestHubSubscribeCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			_, _, err := memoryHub().subscribe(ctx, everything, 2)
			done <- err
		}()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-done, context.Canceled)
	})
}

func TestWatcherFutureRevisionTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := memoryWatcher(t, memoryHub())
		go w.run(100, false, false)
		e := <-w.result
		require.Equal(t, watch.Error, e.Type)
		require.True(t, storage.IsTooLargeResourceVersion(apierrors.FromObject(e.Object.(*metav1.Status))))
	})
}

func TestHubBookmarksRespectQueue(t *testing.T) {
	h := memoryHub()
	target := everything
	target.allowBookmarks = true
	_, sub := subscribe(t, h, target, initialRevision)
	arrive(t, h, watcherBuffer+1, span(2, watcherBuffer+1)...)
	due := sub.nextBookmark
	h.tick(due)
	require.Equal(t, due, sub.nextBookmark, "a full queue postponed the bookmark")
	for _, rev := range span(2, watcherBuffer+1) {
		require.Equal(t, rev, (<-sub.in).rev)
	}

	h.tick(due)
	e := <-sub.in
	require.Equal(t, watch.Bookmark, e.op)
	require.Equal(t, int64(watcherBuffer+1), e.rev)
	h.tick(due)
	require.Empty(t, sub.in, "bookmark sent before its next interval")
}

func TestHubDropsSlowWatcher(t *testing.T) {
	h := memoryHub()
	_, sub := subscribe(t, h, everything, initialRevision)
	arrive(t, h, watcherBuffer+2, span(2, watcherBuffer+2)...)
	require.Len(t, sub.in, watcherBuffer)
	require.ErrorIs(t, sub.err, errFellBehind)
	feed := h.projects["project"]
	require.NotContains(t, feed.watchers, sub)
	require.Equal(t, int64(watcherBuffer+2), feed.seq.latest(), "dropping a watcher interrupted the recent events")
}

func TestWatcherDrainsBeforeError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := memoryHub()
		w := memoryWatcher(t, h)
		go w.run(initialRevision, false, false)
		synctest.Wait()
		sub := onlySubscription(t, h)
		h.mu.Lock()
		sub.in <- &feedEvent{op: watch.Bookmark, rev: 2}
		h.projects["project"].drop(sub, apierrors.NewResourceExpired("relist"), "reset")
		h.mu.Unlock()

		first := <-w.result
		require.Equal(t, watch.Bookmark, first.Type)
		require.Equal(t, "2", first.Object.(*example.Pod).ResourceVersion)
		last := <-w.result
		require.Equal(t, watch.Error, last.Type)
		require.Equal(t, int32(410), last.Object.(*metav1.Status).Code)
		_, open := <-w.result
		require.False(t, open)
	})
}

func TestWatcherStopUnblocksDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := memoryHub()
		w := memoryWatcher(t, h)
		w.result = make(chan watch.Event)
		go w.run(initialRevision, false, false)
		synctest.Wait()
		onlySubscription(t, h).in <- &feedEvent{op: watch.Bookmark, rev: 2}
		synctest.Wait()
		w.Stop()
		synctest.Wait()
		_, open := <-w.result
		require.False(t, open, "watch delivered an event after it stopped")
		h.mu.Lock()
		defer h.mu.Unlock()
		require.Empty(t, h.projects["project"].watchers)
	})
}

// A timestamp is kept only until it is a window old, however often it is
// recorded, so exact reads never reach past the versions GC keeps.
func TestRevisionTimestampsExpireByCommitTime(t *testing.T) {
	r := revisionTimestamps{c: cache.NewExpiring(), window: time.Minute}
	r.put("project", 2, hlc{wallNanos: time.Now().Add(-2 * time.Minute).UnixNano()})
	_, ok := r.get("project", 2)
	require.False(t, ok, "a timestamp older than the window was recorded")
	recent := hlc{wallNanos: time.Now().UnixNano()}
	r.put("project", 3, recent)
	got, ok := r.get("project", 3)
	require.True(t, ok)
	require.Equal(t, recent, got)
}
