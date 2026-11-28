package cockroach

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/klog/v2"
)

const (
	// sweepBatch bounds the rows one statement expires or removes.
	sweepBatch = 256

	// sweepTimeout bounds one sweep, so a stalled node can't hold the sweeper.
	sweepTimeout = 30 * time.Second

	// backstopTimeout bounds one backstop scan of the objects table.
	backstopTimeout = 10 * time.Minute

	// maxQueuedTombstones bounds the tombstones a process remembers, about
	// 430 deletes/s over the default retention of 10 minutes. The backstop
	// removes any past it.
	maxQueuedTombstones = 1 << 18
)

// sweeper deletes objects whose TTL has passed and removes tombstones once
// nothing needs them. An expired object stays visible until its delete
// commits, as an ordinary write with a revision and a DELETED event of its
// own. Every process sweeps, and the deletes' revision preconditions make
// concurrent sweeps on other replicas harmless.
//
// Each process removes the tombstones it wrote, from tombstones, with point
// deletes once their retention passes. The backstop removes those no process
// remembers, such as the tombstones of a process that stopped first.
type sweeper struct {
	pool               *pgxpool.Pool
	committer          committer
	tombstones         *tombstoneQueue
	tombstoneRetention time.Duration
}

func (s *sweeper) sweep(ctx context.Context, now time.Time) {
	ctx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()
	if err := s.expire(ctx); err != nil && ctx.Err() == nil {
		klog.ErrorS(err, "Failed to delete expired objects from CockroachDB storage")
	}
	if err := s.purge(ctx, now.Add(-s.tombstoneRetention)); err != nil && ctx.Err() == nil {
		klog.ErrorS(err, "Failed to remove tombstones from CockroachDB storage")
	}
}

// sweepUnqueued runs the backstop for the tombstones past their retention at
// now.
func (s *sweeper) sweepUnqueued(ctx context.Context, now time.Time) {
	if err := s.backstop(ctx, now.Add(-s.tombstoneRetention)); err != nil && ctx.Err() == nil {
		klog.ErrorS(err, "Failed to scan CockroachDB storage for tombstones")
	}
}

// expire deletes the objects whose TTL has passed, from the expirations
// their writes recorded. Each delete requires the revision the object was
// written at with that TTL, so an object written or deleted since stays as it
// is, and its record goes either way.
func (s *sweeper) expire(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT expires_at, project, key, rv FROM expirations
		WHERE expires_at <= now() ORDER BY expires_at LIMIT $1`, sweepBatch)
	if err != nil {
		return err
	}
	due, err := pgx.CollectRows(rows, pgx.RowToStructByPos[expiration])
	if err != nil {
		return err
	}
	var done []expiration
	for _, e := range due {
		res, err := s.committer.commit(ctx, e.Project, deleteWrite(e.Key, e.Rev))
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			klog.ErrorS(err, "Failed to delete an expired object from CockroachDB storage", "project", e.Project, "key", e.Key)
			continue
		case res.written:
			expiredObjects.Inc()
		}
		done = append(done, e)
	}
	return s.forget(ctx, done)
}

// expiration is one row of the expirations table.
type expiration struct {
	At           time.Time
	Project, Key string
	Rev          int64
}

// forget removes the expirations records of handled objects.
func (s *sweeper) forget(ctx context.Context, done []expiration) error {
	if len(done) == 0 {
		return nil
	}
	at := make([]time.Time, len(done))
	projects := make([]string, len(done))
	keys := make([]string, len(done))
	for i, e := range done {
		at[i], projects[i], keys[i] = e.At, e.Project, e.Key
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM expirations
		WHERE (expires_at, project, key) IN (SELECT * FROM unnest($1::TIMESTAMPTZ[], $2::STRING[], $3::STRING[]))`,
		at, projects, keys)
	return err
}

// purge removes the queued tombstones written before cutoff. A batch that
// fails is left to the backstop.
func (s *sweeper) purge(ctx context.Context, cutoff time.Time) error {
	for {
		due := s.tombstones.popBefore(cutoff, sweepBatch)
		if len(due) == 0 {
			return nil
		}
		if err := s.remove(ctx, due); err != nil {
			return err
		}
	}
}

// backstop removes the tombstones deleted before cutoff, a page at a time,
// found by scanning a recent snapshot of the objects table, which takes no
// locks.
func (s *sweeper) backstop(ctx context.Context, cutoff time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, backstopTimeout)
	defer cancel()
	var after tombstone
	for {
		rows, err := s.pool.Query(ctx, `SELECT project, key, rv FROM objects AS OF SYSTEM TIME follower_read_timestamp()
			WHERE (project, key) > ($1, $2) AND deleted AND deleted_at < $3
			ORDER BY project, key LIMIT $4`, after.project, after.key, cutoff, sweepBatch)
		if err != nil {
			return err
		}
		due, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (tombstone, error) {
			var t tombstone
			return t, r.Scan(&t.project, &t.key, &t.rev)
		})
		if err != nil {
			return err
		}
		if len(due) > 0 {
			if err := s.remove(ctx, due); err != nil {
				return err
			}
		}
		if len(due) < sweepBatch {
			return nil
		}
		after = due[len(due)-1]
	}
}

// remove deletes the tombstones, each only while its row is still that
// tombstone, so an object written again since stays.
func (s *sweeper) remove(ctx context.Context, tombstones []tombstone) error {
	projects := make([]string, len(tombstones))
	keys := make([]string, len(tombstones))
	revs := make([]int64, len(tombstones))
	for i, t := range tombstones {
		projects[i], keys[i], revs[i] = t.project, t.key, t.rev
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM objects
		WHERE (project, key, rv) IN (SELECT * FROM unnest($1::STRING[], $2::STRING[], $3::INT8[])) AND deleted`,
		projects, keys, revs)
	if err != nil {
		return err
	}
	purgedTombstones.Add(float64(tag.RowsAffected()))
	return nil
}

// tombstone is a deleted object's row, at the revision of its delete, which
// committed at at.
type tombstone struct {
	project, key string
	rev          int64
	at           time.Time
}

// tombstoneQueue holds the tombstones this process wrote, in the order it
// wrote them.
type tombstoneQueue struct {
	mu    sync.Mutex
	items []tombstone
}

func (q *tombstoneQueue) push(t tombstone) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == maxQueuedTombstones {
		droppedTombstones.Inc()
		return
	}
	q.items = append(q.items, t)
	queuedTombstones.Set(float64(len(q.items)))
}

// popBefore removes and returns up to n of the oldest tombstones, those
// written before cutoff.
func (q *tombstoneQueue) popBefore(cutoff time.Time, n int) []tombstone {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := 0
	for i < len(q.items) && i < n && q.items[i].at.Before(cutoff) {
		i++
	}
	due := slices.Clone(q.items[:i])
	// The queue drops its references to what it hands out, and its array once
	// empty, so a burst of deletes doesn't hold memory after it drains.
	clear(q.items[:i])
	q.items = q.items[i:]
	if len(q.items) == 0 {
		q.items = nil
	}
	queuedTombstones.Set(float64(len(q.items)))
	return due
}

// queuedDeletes queues the tombstone of each delete that next writes.
type queuedDeletes struct {
	next       batchCommitter
	tombstones *tombstoneQueue
}

func (q queuedDeletes) commitBatch(ctx context.Context, project string, b writeBatch) ([]writeResult, error) {
	results, err := q.next.commitBatch(ctx, project, b)
	if err != nil {
		return results, err
	}
	now := time.Now()
	for i, w := range b.writes {
		if w.deleted && results[i].written {
			q.tombstones.push(tombstone{project: project, key: w.key, rev: results[i].rev, at: now})
		}
	}
	return results, nil
}
