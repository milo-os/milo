package cockroach

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/klog/v2"
)

// watcherBuffer is how many events a watcher can fall behind before the hub
// closes it, and the client then resumes from the recent events. Every watch
// allocates it up front, so it stays as small as etcd3's.
const watcherBuffer = 100

type hubConfig struct {
	awaitTimeout     time.Duration
	tickInterval     time.Duration
	bookmarkInterval time.Duration
	replay           replayConfig
	maxHeldBackBytes int
}

func defaultHubConfig() hubConfig {
	return hubConfig{
		awaitTimeout:     3 * time.Second,
		tickInterval:     time.Second,
		bookmarkInterval: time.Minute, // as the watch cache sends them
		replay:           defaultReplayConfig(),
		maxHeldBackBytes: maxHeldBackBytes,
	}
}

// hub hands each project's writes to its watchers in revision order, and lets
// requests wait for a revision to be released.
type hub struct {
	revTS  revisionTimestamps
	config hubConfig
	ready  chan struct{}

	mu            sync.Mutex
	readyOnce     sync.Once
	projects      map[string]*projectStream
	heldBackBytes int
}

// maxHeldBackBytes bounds the values the hub holds back for revisions that
// haven't arrived. A resumed feed replays its gap range by range, so a
// project's writes can wait for a counter row that comes last. At 10x today's
// hottest project, 120 writes/s of objects averaging 4 KiB, a 5-minute gap
// holds about 140 MiB. Past the bound the hub starts again from a new
// baseline, and every watch relists.
const maxHeldBackBytes = 1 << 30

// errHeldBack reports that the hub gave up holding back writes.
var errHeldBack = errors.New("the watch hub holds back more than its bound of writes waiting for earlier revisions")

// projectStream is one project's sequencer and the watches following it.
type projectStream struct {
	seq      *sequencer
	watchers map[*subscription]struct{}
	released broadcast
}

func (h *hub) newProjectStream(rev int64) *projectStream {
	return &projectStream{seq: newSequencer(rev, h.config.replay), watchers: map[*subscription]struct{}{}}
}

// watchTarget is what a watch follows.
type watchTarget struct {
	project        string
	key            string
	recursive      bool
	allowBookmarks bool
}

func (w watchTarget) matches(key string) bool {
	if w.recursive {
		return strings.HasPrefix(key, w.key)
	}
	return key == w.key
}

// errFellBehind ends a watch that fell more than watcherBuffer events behind.
// The watch ends quietly, and the client resumes from the recent events.
var errFellBehind = errors.New("the watch fell behind")

// subscription is a watch's place in its project's stream. The hub sends the
// watch's events on in. The other fields belong to the hub, under its lock.
type subscription struct {
	watchTarget
	in           chan *feedEvent
	err          error
	nextBookmark time.Time
}

// next returns the watch's next event. Once the hub has ended the
// subscription and the queued events are gone, it returns why the hub ended
// it.
func (s *subscription) next(ctx context.Context) (*feedEvent, error) {
	select {
	case e, ok := <-s.in:
		if !ok {
			return nil, s.err
		}
		return e, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func newHub(revTS revisionTimestamps, config hubConfig) *hub {
	return &hub{revTS: revTS, config: config, ready: make(chan struct{}), projects: map[string]*projectStream{}}
}

// reset implements feedHandler. Open watches end with 410 Gone, because the
// feed replays nothing from before the new baseline.
func (h *hub) reset(counters map[string]int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	gone := apierrors.NewResourceExpired("the watch feed restarted; list again")
	for _, t := range h.projects {
		for sub := range t.watchers {
			t.drop(sub, gone, "reset")
		}
		// Requests waiting for a revision look again, in the new projects.
		t.released.fire()
	}
	h.projects = map[string]*projectStream{}
	h.heldBackBytes = 0
	for project, rev := range counters {
		h.projects[project] = h.newProjectStream(rev)
	}
	h.readyOnce.Do(func() { close(h.ready) })
}

// write implements feedHandler.
func (h *hub) write(after, before *feedRow, committed hlc) error {
	var e *feedEvent
	if after.Key != "" {
		e = h.event(after, before, committed)
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.projectLocked(after.Project)
	var r sequenced
	if e == nil {
		r = t.seq.writeCounter(after.RV, now)
	} else {
		r = t.seq.write(e, now)
	}
	// Commit times are recorded as revisions are released, in order and before
	// waiters wake, so a replica that knows a revision's knows every earlier
	// one's. The commit time lets exact reads at a revision work on every
	// replica, not only the one that wrote it.
	for _, e := range r.released {
		h.revTS.put(after.Project, e.rev, e.committed)
	}
	t.publish(r.released, now)
	h.heldBackBytes += r.heldDelta
	if h.heldBackBytes > h.config.maxHeldBackBytes {
		return errHeldBack
	}
	return nil
}

// event returns the feed event for a written object row.
func (h *hub) event(after, before *feedRow, committed hlc) *feedEvent {
	e := &feedEvent{op: watch.Modified, key: after.Key, rev: after.RV, committed: committed}
	if before == nil || before.Deleted {
		e.op = watch.Added
	} else {
		e.prevValue = before.Value
	}
	if after.Deleted {
		e.op = watch.Deleted
	} else {
		e.value = after.Value
	}
	return e
}

// projectLocked returns project's stream. A project missing from the baseline
// had no counter row then, so its revisions start at initialRevision.
func (h *hub) projectLocked(project string) *projectStream {
	t, ok := h.projects[project]
	if !ok {
		t = h.newProjectStream(initialRevision)
		h.projects[project] = t
	}
	return t
}

// publish sends released events to the watches they match, closing any
// watch too far behind to take one.
func (t *projectStream) publish(released []*feedEvent, now time.Time) {
	for _, e := range released {
		watchEvents.Inc()
		if !e.committed.isZero() {
			delivery.Observe(now.Sub(e.committed.time()).Seconds())
		}
		for sub := range t.watchers {
			if !sub.matches(e.key) {
				continue
			}
			select {
			case sub.in <- e:
			default:
				klog.V(2).InfoS("Closing a watcher that fell behind", "project", sub.project, "key", sub.key)
				t.drop(sub, errFellBehind, "behind")
			}
		}
	}
	if len(released) > 0 {
		t.released.fire()
	}
}

// drop stops sending to sub. The watch ends with err once it has handled the
// events already queued.
func (t *projectStream) drop(sub *subscription, err error, reason string) {
	delete(t.watchers, sub)
	sub.err = err
	close(sub.in)
	closedWatchers.WithLabelValues(reason).Inc()
}

// tick ages out recent events, sends due bookmarks and updates the hub's
// gauges.
func (h *hub) tick(now time.Time) {
	var watchers, pending, recent, recentBytes int
	h.mu.Lock()
	for _, t := range h.projects {
		t.seq.age(now)
		pending += t.seq.heldBackWrites()
		events, valueBytes := t.seq.history()
		recent += events
		recentBytes += valueBytes
		var bookmark *feedEvent // shared by the project's watchers, which only read it
		for sub := range t.watchers {
			watchers++
			if !sub.allowBookmarks || now.Before(sub.nextBookmark) {
				continue
			}
			if bookmark == nil {
				bookmark = &feedEvent{op: watch.Bookmark, rev: t.seq.latest()}
			}
			// Every released event is already queued ahead of the bookmark,
			// and a full queue gets the bookmark on a later tick.
			select {
			case sub.in <- bookmark:
				sub.nextBookmark = now.Add(h.config.bookmarkInterval)
				bookmarks.Inc()
			default:
			}
		}
	}
	h.mu.Unlock()
	openWatchers.Set(float64(watchers))
	pendingEvents.Set(float64(pending))
	recentEvents.Set(float64(recent))
	recentEventBytes.Set(float64(recentBytes))
}

func (h *hub) waitReady(ctx context.Context) error {
	select {
	case <-h.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// subscribe follows target's events after from. It returns the ones already
// released, and the subscription the rest arrive on. It waits up to
// tooLargeWait for a revision the hub has not released.
func (h *hub) subscribe(ctx context.Context, target watchTarget, from int64) ([]*feedEvent, *subscription, error) {
	if err := h.awaitRevision(ctx, target.project, from, "watch"); err != nil {
		return nil, nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.projectLocked(target.project)
	switch {
	case from < t.seq.oldest():
		return nil, nil, apierrors.NewResourceExpired(fmt.Sprintf("too old resource version: %d (%d)", from, t.seq.oldest()))
	case from > t.seq.latest():
		// A reset between awaitRevision and the lock started the project lower.
		return nil, nil, storage.NewTooLargeResourceVersionError(uint64(from), uint64(t.seq.latest()), 1)
	}
	var released []*feedEvent
	for _, e := range t.seq.replayAfter(from) {
		if target.matches(e.key) {
			released = append(released, e)
		}
	}
	sub := &subscription{
		watchTarget:  target,
		in:           make(chan *feedEvent, watcherBuffer),
		nextBookmark: time.Now().Add(h.config.bookmarkInterval),
	}
	t.watchers[sub] = struct{}{}
	return released, sub, nil
}

func (h *hub) unsubscribe(sub *subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t, ok := h.projects[sub.project]; ok {
		delete(t.watchers, sub)
	}
}

// compactRevision returns the oldest revision a watch of project can start
// from, or 0 while it can start from any.
func (h *hub) compactRevision(project string) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.projects[project]
	if !ok || t.seq.oldest() <= initialRevision {
		return 0
	}
	return t.seq.oldest()
}

// releasedRevision returns the last revision of project that the hub has
// released.
func (h *hub) releasedRevision(project string) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.projectLocked(project).seq.latest()
}

// awaitRevision waits up to tooLargeWait for the hub to release project's
// rev. It returns a too-large resource version error when the hub doesn't,
// and request names the waiter in the metrics.
func (h *hub) awaitRevision(ctx context.Context, project string, rev int64, request string) error {
	if err := h.waitReady(ctx); err != nil {
		return err
	}
	if h.releasedRevision(project) >= rev {
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, h.config.awaitTimeout)
	defer cancel()
	released, err := h.waitReleased(waitCtx, project, rev)
	switch {
	case err == nil:
		feedWaits.WithLabelValues(request, "caught_up").Inc()
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	}
	feedWaits.WithLabelValues(request, "timeout").Inc()
	return storage.NewTooLargeResourceVersionError(uint64(rev), uint64(released), 1)
}

// waitReleased waits until the hub has released project's rev, and returns
// the last revision it has released. A reset wakes it to look again in the
// new baseline.
func (h *hub) waitReleased(ctx context.Context, project string, rev int64) (int64, error) {
	for {
		h.mu.Lock()
		t := h.projectLocked(project)
		end := t.seq.latest()
		if end >= rev {
			h.mu.Unlock()
			return end, nil
		}
		released := t.released.wait()
		h.mu.Unlock()
		select {
		case <-released:
		case <-ctx.Done():
			return end, ctx.Err()
		}
	}
}
