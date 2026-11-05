package cockroach

import (
	"time"

	"k8s.io/apimachinery/pkg/watch"
)

// replayConfig bounds the recent events a project keeps for watches that start
// at an older revision. It keeps those within window, up to events of them
// and bytes of their values. An older start gets 410 Gone, and the client
// lists again.
type replayConfig struct {
	window time.Duration
	events int
	bytes  int
}

func defaultReplayConfig() replayConfig {
	return replayConfig{window: 5 * time.Minute, events: 1024, bytes: 64 << 20}
}

// feedEvent is one object write, or a bookmark at rev.
type feedEvent struct {
	op         watch.EventType
	key        string
	rev        int64
	value      []byte
	prevValue  []byte
	committed  hlc
	releasedAt time.Time
}

func (e *feedEvent) size() int { return len(e.value) + len(e.prevValue) }

// sequencer puts one project's writes in revision order and keeps the recent
// ones.
//
// The feed delivers rows in no particular order across keys. The project's
// counter row moves at every write, so its value says which revisions have
// committed. Revision n is released once the counter has reached n and the
// row written at n has arrived, which keeps releases gapless and in order
// however the rows interleave.
type sequencer struct {
	highestCounter int64
	heldBack       map[int64]*feedEvent
	log            eventRing
}

// sequenced is what one step of the sequencer did: the writes it released, in
// revision order, and how much the values it holds back grew or shrank.
type sequenced struct {
	released  []*feedEvent
	heldDelta int
}

func newSequencer(rev int64, replay replayConfig) *sequencer {
	return &sequencer{highestCounter: rev, heldBack: map[int64]*feedEvent{}, log: eventRing{base: rev, limits: replay}}
}

// writeCounter notes the counter row at rev.
func (s *sequencer) writeCounter(rev int64, now time.Time) sequenced {
	s.highestCounter = max(s.highestCounter, rev)
	return s.release(now, 0)
}

// write notes e. A write released before, which a resumed feed sends again,
// changes nothing, and one held back already replaces the earlier copy.
func (s *sequencer) write(e *feedEvent, now time.Time) sequenced {
	if e.rev <= s.log.end() {
		return sequenced{}
	}
	held := e.size()
	if earlier, ok := s.heldBack[e.rev]; ok {
		held -= earlier.size()
	}
	s.heldBack[e.rev] = e
	return s.release(now, held)
}

// release lets go of every held-back write that the counter has passed, in
// revision order. held is what the step before it added to the held-back
// values.
func (s *sequencer) release(now time.Time, held int) sequenced {
	r := sequenced{heldDelta: held}
	for s.log.end() < s.highestCounter {
		e, ok := s.heldBack[s.log.end()+1]
		if !ok {
			break
		}
		delete(s.heldBack, e.rev)
		r.heldDelta -= e.size()
		e.releasedAt = now
		s.log.push(e)
		r.released = append(r.released, e)
	}
	s.log.trim(now)
	return r
}

// heldBackWrites returns how many writes wait for an earlier revision.
func (s *sequencer) heldBackWrites() int { return len(s.heldBack) }

// age forgets released events past the replay limits. A project that stopped
// writing still ages its events this way.
func (s *sequencer) age(now time.Time) { s.log.trim(now) }

// history returns how many released events the sequencer keeps for replay,
// and the bytes of their values.
func (s *sequencer) history() (events, valueBytes int) {
	return len(s.log.events), s.log.valueBytes
}

// oldest returns the oldest revision a watch can resume from.
func (s *sequencer) oldest() int64 { return s.log.oldest() }

// latest returns the last revision released.
func (s *sequencer) latest() int64 { return s.log.end() }

// replayAfter returns the released events after rev, which must be between
// oldest and latest.
func (s *sequencer) replayAfter(rev int64) []*feedEvent { return s.log.since(rev) }

// eventRing holds released events. events[i] is at revision base+1+i, and
// the last one is the last revision released.
type eventRing struct {
	base       int64
	events     []*feedEvent
	valueBytes int
	limits     replayConfig
}

// oldest returns the oldest revision a watch can start from.
func (r *eventRing) oldest() int64 { return r.base }

// end returns the last revision released.
func (r *eventRing) end() int64 { return r.base + int64(len(r.events)) }

// push appends e, which must be at revision end()+1.
func (r *eventRing) push(e *feedEvent) {
	r.events = append(r.events, e)
	r.valueBytes += e.size()
}

// since returns the events after rev, which must be at or after start.
func (r *eventRing) since(rev int64) []*feedEvent { return r.events[rev-r.base:] }

// drop forgets the oldest n events.
func (r *eventRing) drop(n int) {
	for _, e := range r.events[:n] {
		r.valueBytes -= e.size()
	}
	clear(r.events[:n])
	r.events = r.events[n:]
	r.base += int64(n)
}

// trim forgets events past the ring's limits.
func (r *eventRing) trim(now time.Time) {
	n, bytes := max(len(r.events)-r.limits.events, 0), r.valueBytes
	for _, e := range r.events[:n] {
		bytes -= e.size()
	}
	for n < len(r.events) && (now.Sub(r.events[n].releasedAt) > r.limits.window || bytes > r.limits.bytes) {
		bytes -= r.events[n].size()
		n++
	}
	r.drop(n)
}
