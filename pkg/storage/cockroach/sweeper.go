package cockroach

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/klog/v2"
)

const (
	// sweepBatch bounds the rows one sweep expires or removes.
	sweepBatch = 256

	// sweepTimeout bounds one sweep, so a stalled node can't hold the sweeper.
	sweepTimeout = 30 * time.Second
)

// sweeper deletes objects whose TTL has passed and removes tombstones once
// nothing needs them. An expired object stays visible until its delete
// commits, as an ordinary write with a revision and a DELETED event of its
// own. Every process sweeps, and the deletes' revision preconditions make
// concurrent sweeps on other replicas harmless.
type sweeper struct {
	pool               *pgxpool.Pool
	committer          committer
	tombstoneRetention time.Duration
}

func (s *sweeper) sweep(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()
	if err := s.expire(ctx); err != nil && ctx.Err() == nil {
		klog.ErrorS(err, "Failed to delete expired objects from CockroachDB storage")
	}
	if err := s.purge(ctx); err != nil && ctx.Err() == nil {
		klog.ErrorS(err, "Failed to remove tombstones from CockroachDB storage")
	}
}

// expire deletes the objects whose TTL has passed. Each delete requires the
// revision the object expired at, and expires_at only changes with a write,
// so a delete never removes an object that was written again.
func (s *sweeper) expire(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT project, key, rv FROM objects
		WHERE expires_at IS NOT NULL AND NOT deleted AND expires_at <= now()
		ORDER BY expires_at LIMIT $1`, sweepBatch)
	if err != nil {
		return err
	}
	due, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct {
		Project, Key string
		Rev          int64
	}])
	if err != nil {
		return err
	}
	for _, e := range due {
		res, err := s.committer.commit(ctx, e.Project, deleteWrite(e.Key, e.Rev))
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			klog.ErrorS(err, "Failed to delete an expired object from CockroachDB storage", "project", e.Project, "key", e.Key)
		case res.written:
			expiredObjects.Inc()
		}
	}
	return nil
}

// purge removes tombstones older than the retention.
func (s *sweeper) purge(ctx context.Context) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM objects
		WHERE deleted AND deleted_at < now() - $1::INT8 * INTERVAL '1 microsecond'
		ORDER BY deleted_at LIMIT $2`, s.tombstoneRetention.Microseconds(), sweepBatch)
	if err != nil {
		return err
	}
	purgedTombstones.Add(float64(tag.RowsAffected()))
	return nil
}
