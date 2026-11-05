package cockroach

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/klog/v2"
)

const (
	// feedResets is how many refused feeds in a row make the feed give up on
	// its cursor and start again from a new baseline.
	feedResets = 3

	// baselineTimeout bounds reading every project's counter, which reads one
	// row per project.
	baselineTimeout = 5 * time.Minute

	// feedStallTimeout is how long the feed waits for a message before it
	// drops its connection and resumes. A resolved message comes every second,
	// so silence means a dead connection, which TCP alone takes minutes to
	// notice.
	feedStallTimeout = 30 * time.Second
)

// feedHandler receives what the feed reads.
type feedHandler interface {
	// reset starts over from a baseline, which holds every project's counter
	// at one timestamp. Nothing between the old position and the new one is
	// replayed.
	reset(counters map[string]int64)
	// write handles a written row, with its value before the write, if any,
	// and the write's commit timestamp. An error means the handler can't go on
	// from here, and the feed starts again from a new baseline.
	write(after, before *feedRow, committed hlc) error
}

// feed follows one changefeed over the objects table. After a drop it resumes
// from its cursor, and when it can't, it starts again from a new baseline.
type feed struct {
	pool       *pgxpool.Pool
	connConfig *pgx.ConnConfig
	retryDelay time.Duration
	handler    feedHandler
	// prefill is how far back the first baseline goes, so a replica that
	// starts can replay as far back as one that has run all along, and its
	// clients resume their watches instead of listing again.
	prefill time.Duration

	// resolved is the wall time of the last resolved message. Every write
	// before it has arrived.
	resolved atomic.Int64
	// startupTarget is the first baseline's wall time, which a resolved
	// message has to reach before the feed's events are current, even when
	// the baseline itself is older.
	startupTarget atomic.Int64
	caughtUp      atomic.Bool

	mu      sync.Mutex
	stopped chan struct{}
	started broadcast
}

// lag returns the time since the last resolved message, or since the first
// baseline while none has arrived. A later baseline doesn't count as
// progress, so a feed that keeps failing shows its lag growing.
func (f *feed) lag() time.Duration {
	since := f.resolved.Load()
	if since == 0 {
		since = f.startupTarget.Load()
	}
	if since == 0 {
		return 0
	}
	return time.Since(time.Unix(0, since))
}

func (f *feed) reportLag(time.Time) { feedLag.Set(f.lag().Seconds()) }

// run follows the changefeed until ctx ends. Only the first baseline goes
// back the prefill.
func (f *feed) run(ctx context.Context) {
	var cursor hlc
	back, failures := f.prefill, 0
	for {
		var kind feedFailure
		var err error
		cursor, kind, err = f.follow(ctx, cursor, back)
		if !cursor.isZero() {
			back = 0
		}
		if ctx.Err() != nil {
			return
		}
		if kind == feedRefused {
			failures++
		} else {
			failures = 0
		}
		if kind == feedReset || failures >= feedResets {
			klog.ErrorS(err, "CockroachDB watch feed cannot resume; restarting from the current state, and open watches must relist")
			cursor = hlc{}
			failures = 0
			reason := "reset"
			if errors.Is(err, errHeldBack) {
				reason = "held_back"
			}
			feedRestarts.WithLabelValues(reason).Inc()
		} else {
			klog.ErrorS(err, "CockroachDB watch feed stopped; resuming", "cursor", cursor)
			feedRestarts.WithLabelValues("resume").Inc()
		}
		select {
		case <-time.After(f.retryDelay):
		case <-ctx.Done():
			return
		}
	}
}

var errFeedStalled = fmt.Errorf("the changefeed sent nothing for %s", feedStallTimeout)

// feedFailure is why the feed stopped, which decides what it does next.
type feedFailure int

const (
	// feedDropped means the connection or the query ended, such as on a
	// cancelled query or a restarting node. The cursor stays valid for the
	// whole GC window, so the feed resumes from it.
	feedDropped feedFailure = iota
	// feedRefused means the database refused the changefeed before it sent a
	// row, such as for a cursor older than the GC window.
	feedRefused
	// feedReset means a row can't be decoded or handled and would fail again
	// on every resume, so the feed starts again from a new baseline right
	// away.
	feedReset
)

// follow runs one changefeed from cursor, or from a new baseline taken back
// ago when cursor is zero, until it fails. It returns where the next
// changefeed resumes and why this one stopped.
func (f *feed) follow(ctx context.Context, cursor hlc, back time.Duration) (hlc, feedFailure, error) {
	if cursor.isZero() {
		var err error
		if cursor, err = f.loadBaseline(ctx, back); err != nil {
			return hlc{}, feedRefused, fmt.Errorf("load the watch baseline: %w", err)
		}
	}
	stream, kind, err := f.openChangefeed(ctx, cursor)
	if err != nil {
		return cursor, kind, err
	}
	defer stream.close()
	f.setRunning(true)
	defer f.setRunning(false)

	received := false
	for values, ok := stream.next(); ok; values, ok = stream.next() {
		received = true
		if len(values) != 3 {
			return cursor, feedReset, fmt.Errorf("changefeed row has %d columns, want 3", len(values))
		}
		msg, err := decodeFeedMessage(values[2])
		if err != nil {
			return cursor, feedReset, err
		}
		switch {
		case !msg.Resolved.isZero():
			cursor = msg.Resolved
			f.resolved.Store(msg.Resolved.wallNanos)
			if msg.Resolved.wallNanos >= f.startupTarget.Load() {
				f.caughtUp.Store(true)
			}
		case msg.After != nil:
			if err := f.handler.write(msg.After, msg.Before, msg.Updated); err != nil {
				return cursor, feedReset, err
			}
		}
		// A message with neither is a row removed outright rather than
		// written, so there is nothing to watch.
	}
	err = stream.err()
	if received {
		return cursor, feedDropped, err
	}
	return cursor, classifyStartError(err), err
}

// changefeedStream is one running changefeed: the connection it holds, the
// rows it streams, and a watchdog that cancels it when the rows stop coming.
type changefeedStream struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	stall  *time.Timer
	conn   *pgx.Conn
	rows   pgx.Rows
}

// openChangefeed starts a changefeed over the objects table from cursor, and
// says why it couldn't.
func (f *feed) openChangefeed(ctx context.Context, cursor hlc) (*changefeedStream, feedFailure, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	s := &changefeedStream{ctx: ctx, cancel: cancel}
	s.stall = time.AfterFunc(feedStallTimeout, func() { cancel(errFeedStalled) })
	// A changefeed holds its connection for as long as it runs, so it gets one
	// of its own rather than one from the pool.
	conn, err := pgx.ConnectConfig(ctx, f.connConfig.Copy())
	if err != nil {
		s.close()
		return nil, feedDropped, err
	}
	s.conn = conn
	// min_checkpoint_frequency defaults to 30s, which would hold resolved
	// timestamps, and with them the readiness lag, back that long.
	rows, err := conn.Query(ctx, `CREATE CHANGEFEED FOR TABLE objects WITH updated, diff, resolved = '1s', min_checkpoint_frequency = '1s', cursor = '`+cursor.String()+`'`)
	if err != nil {
		s.close()
		return nil, classifyStartError(err), err
	}
	s.rows = rows
	return s, 0, nil
}

// next waits for the changefeed's next row and returns its columns, or false
// once the changefeed has ended.
func (s *changefeedStream) next() ([][]byte, bool) {
	if !s.rows.Next() {
		return nil, false
	}
	s.stall.Reset(feedStallTimeout)
	return s.rows.RawValues(), true
}

// err returns why the changefeed ended.
func (s *changefeedStream) err() error {
	if cause := context.Cause(s.ctx); errors.Is(cause, errFeedStalled) {
		return cause
	}
	if err := s.rows.Err(); err != nil {
		return err
	}
	return errors.New("the changefeed ended")
}

// close ends the changefeed and releases its connection.
func (s *changefeedStream) close() {
	// Closing rows reads until the query ends, and a changefeed never does,
	// so the query is cancelled first.
	s.cancel(nil)
	s.stall.Stop()
	if s.rows != nil {
		s.rows.Close()
	}
	if s.conn != nil {
		_ = s.conn.Close(context.WithoutCancel(s.ctx))
	}
}

// classifyStartError classifies an error from a changefeed that sent no row.
// A database error other than a cancelled query means the database refused
// the changefeed.
func classifyStartError(err error) feedFailure {
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code != codeQueryCanceled {
		return feedRefused
	}
	return feedDropped
}

// loadBaseline reads every project's counter as of back ago, hands them to
// the handler, and returns that timestamp for the feed to start from, so it
// replays everything since. When it can't read that far back, such as for a
// table created since, it reads the current state instead.
func (f *feed) loadBaseline(ctx context.Context, back time.Duration) (hlc, error) {
	ctx, cancel := context.WithTimeout(ctx, baselineTimeout)
	defer cancel()
	var now hlc
	if err := f.pool.QueryRow(ctx, clusterTimeSQL).Scan(&now); err != nil {
		return hlc{}, err
	}
	ts := now
	if back > 0 {
		ts = hlc{wallNanos: now.wallNanos - back.Nanoseconds()}
	}
	counters, err := f.readCounters(ctx, ts)
	if err != nil && back > 0 {
		klog.InfoS("Cannot replay the CockroachDB watch feed from before this start; starting from the current state", "err", err)
		ts = now
		counters, err = f.readCounters(ctx, ts)
	}
	if err != nil {
		return hlc{}, err
	}
	f.startupTarget.CompareAndSwap(0, now.wallNanos)
	f.handler.reset(counters)
	return ts, nil
}

// countersSQL finds every project's counter by seeking from project to
// project. A counter row's key is empty, so it is its project's first row.
// That reads one row per project, where filtering on the key would read the
// whole table.
const countersSQL = `WITH RECURSIVE counters (project, key, rv) AS (
	(SELECT project, key, rv FROM objects ORDER BY project, key LIMIT 1)
	UNION ALL
	(SELECT o.project, o.key, o.rv FROM counters AS c,
		LATERAL (SELECT project, key, rv FROM objects WHERE project > c.project ORDER BY project, key LIMIT 1) AS o)
)
SELECT project, rv FROM counters WHERE key = ''`

// readCounters reads every project's counter at ts.
func (f *feed) readCounters(ctx context.Context, ts hlc) (map[string]int64, error) {
	conn, err := f.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	defer endTransaction(ctx, conn.Conn())
	if _, err := conn.Exec(ctx, snapshotSQL(ts)); err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, countersSQL)
	if err != nil {
		return nil, err
	}
	counters := map[string]int64{}
	var project string
	var rev int64
	_, err = pgx.ForEachRow(rows, []any{&project, &rev}, func() error {
		counters[project] = rev
		return nil
	})
	return counters, err
}

func (f *feed) setRunning(running bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if running {
		f.stopped = make(chan struct{})
		f.started.fire()
		return
	}
	close(f.stopped)
	f.stopped = nil
}

// waitRunning waits for a changefeed to run and returns a channel closed when
// it stops.
func (f *feed) waitRunning(ctx context.Context) (<-chan struct{}, error) {
	for {
		f.mu.Lock()
		if running := f.stopped; running != nil {
			f.mu.Unlock()
			return running, nil
		}
		started := f.started.wait()
		f.mu.Unlock()
		select {
		case <-started:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// feedMessage is one message of a changefeed WITH updated, diff, resolved.
type feedMessage struct {
	After    *feedRow `json:"after"`
	Before   *feedRow `json:"before"`
	Updated  hlc      `json:"updated"`
	Resolved hlc      `json:"resolved"`
}

func decodeFeedMessage(data []byte) (feedMessage, error) {
	var msg feedMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return msg, fmt.Errorf("decode changefeed message: %w", err)
	}
	return msg, nil
}

// feedRow is a row of the objects table as a changefeed encodes it.
type feedRow struct {
	Project string    `json:"project"`
	Key     string    `json:"key"`
	RV      int64     `json:"rv"`
	Deleted bool      `json:"deleted"`
	Value   feedBytes `json:"value"`
}

// feedBytes is a BYTES column, which changefeeds encode in JSON as "\x" and
// hex digits.
type feedBytes []byte

func (b *feedBytes) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*b = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	digits, ok := strings.CutPrefix(s, `\x`)
	if !ok {
		return fmt.Errorf("changefeed bytes value %.20q has no \\x prefix", s)
	}
	var err error
	*b, err = hex.DecodeString(digits)
	return err
}
