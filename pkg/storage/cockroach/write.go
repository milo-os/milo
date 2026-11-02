package cockroach

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	codeSerializationFailure = "40001"
	codeQueryCanceled        = "57014"

	maxAttempts = 20
	maxBackoff  = 100 * time.Millisecond
)

// write is one conditional write of a key. createWrite, updateWrite and
// deleteWrite build each kind.
type write struct {
	key         string
	expectedRev int64
	value       []byte
	deleted     bool
	ttlSeconds  uint64
}

// createWrite writes key only when it has no live object.
func createWrite(key string, value []byte, ttlSeconds uint64) write {
	return write{key: key, value: value, ttlSeconds: ttlSeconds}
}

// updateWrite writes key only when its live object is at rev.
func updateWrite(key string, rev int64, value []byte, ttlSeconds uint64) write {
	return write{key: key, expectedRev: rev, value: value, ttlSeconds: ttlSeconds}
}

// deleteWrite deletes key only when its live object is at rev.
func deleteWrite(key string, rev int64) write {
	return write{key: key, expectedRev: rev, deleted: true}
}

// matches reports whether live, the key's live row or nil, meets w's
// precondition.
func (w write) matches(live *row) bool {
	if live == nil {
		return w.expectedRev == 0 && !w.deleted
	}
	return live.rev == w.expectedRev
}

type writeResult struct {
	rev     int64
	live    *row // the live row the write was checked against
	written bool
}

// writeOutcome is a write's result, or why it failed.
type writeOutcome struct {
	result writeResult
	err    error
}

// The write path is a stack of layers, each adding one concern, and
// newCommitter stacks them. Above unbatched the layers handle one write at a
// time, and below it one transaction's worth.

// newCommitter stacks the write path's layers, which from the top are:
//
//  1. admittedWrites holds a slot under the write's project for the whole
//     write, so one busy project can't take every transaction.
//  2. unbatched gives each write a batch, and a transaction, of its own.
//  3. admittedBatches holds one of the server's slots for each transaction,
//     which keeps the transactions in flight below the pool's connections.
//  4. retryingBatches reruns a transaction that aborted on a conflict. It sits
//     below the server's slots, so a retry reruns the same batch in the slot
//     it already holds.
//  5. observedBatches times each attempt in the operation metrics.
//  6. attempt runs one transaction.
func newCommitter(attempt batchFunc, projectSlots, serverSlots *admission) committer {
	var txn batchCommitter = observedBatches{attempt}
	txn = retryingBatches{txn}
	txn = admittedBatches{serverSlots, txn}
	return admittedWrites{projectSlots, unbatched{txn}}
}

// committer commits one write into a project.
type committer interface {
	commit(ctx context.Context, project string, w write) (writeResult, error)
}

// batchCommitter commits a batch into a project in one transaction, returning
// one result per write.
type batchCommitter interface {
	commitBatch(ctx context.Context, project string, b writeBatch) ([]writeResult, error)
}

// batchFunc adapts a function to a batchCommitter.
type batchFunc func(ctx context.Context, project string, b writeBatch) ([]writeResult, error)

func (f batchFunc) commitBatch(ctx context.Context, project string, b writeBatch) ([]writeResult, error) {
	return f(ctx, project, b)
}

// writeBatch is writes that can commit together in one transaction: at least
// one, each of an object key, and no key twice. newWriteBatch builds one.
type writeBatch struct {
	writes []write
	// keys are the keys the transaction locks: the counter's, then each
	// write's.
	keys []string
}

func newWriteBatch(writes ...write) (writeBatch, error) {
	if len(writes) == 0 {
		return writeBatch{}, errors.New("cockroach storage: empty write batch")
	}
	keys := make([]string, 1, len(writes)+1)
	seen := make(map[string]bool, len(writes))
	for _, w := range writes {
		switch {
		case w.key == "":
			return writeBatch{}, errors.New("cockroach storage: a write requires an object key")
		case seen[w.key]:
			return writeBatch{}, fmt.Errorf("cockroach storage: batch repeats key %q", w.key)
		}
		seen[w.key] = true
		keys = append(keys, w.key)
	}
	return writeBatch{writes: writes, keys: keys}, nil
}

// unbatched commits each write in a transaction of its own.
type unbatched struct {
	next batchCommitter
}

func (u unbatched) commit(ctx context.Context, project string, w write) (writeResult, error) {
	b, err := newWriteBatch(w)
	if err != nil {
		return writeResult{}, err
	}
	results, err := u.next.commitBatch(ctx, project, b)
	if err != nil {
		return writeResult{}, err
	}
	return results[0], nil
}

// retryingBatches runs next again when its transaction aborts on a conflict.
type retryingBatches struct {
	next batchCommitter
}

func (r retryingBatches) commitBatch(ctx context.Context, project string, b writeBatch) ([]writeResult, error) {
	return retry(ctx, "write", func() ([]writeResult, error) { return r.next.commitBatch(ctx, project, b) })
}

// observedBatches records each transaction next runs in the operation
// metrics.
type observedBatches struct {
	next batchCommitter
}

func (o observedBatches) commitBatch(ctx context.Context, project string, b writeBatch) ([]writeResult, error) {
	start := time.Now()
	results, err := o.next.commitBatch(ctx, project, b)
	observeOperation("write", start, err)
	return results, err
}

// retry runs fn, an operation's transaction, until it succeeds, fails in a
// way another attempt cannot fix, or has run maxAttempts times. It backs off
// between attempts, with jitter, as CockroachDB advises for transaction
// retries, so conflicting transactions spread out instead of colliding again.
func retry[T any](ctx context.Context, operation string, fn func() (T, error)) (T, error) {
	for attempt := 1; ; attempt++ {
		v, err := fn()
		if err == nil || !retryable(err) || attempt == maxAttempts {
			return v, err
		}
		retries.WithLabelValues(operation, errorCode(err)).Inc()
		select {
		case <-time.After(rand.N(min(time.Millisecond<<attempt, maxBackoff))):
		case <-ctx.Done():
			return v, ctx.Err()
		}
	}
}

// retryable reports whether err is safe to retry. Only a serialization
// failure guarantees the transaction aborted. An ambiguous commit may have
// landed, so it can't be retried.
func retryable(err error) bool {
	pe, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pe.Code == codeSerializationFailure
}

type row struct {
	key   string
	rev   int64
	value []byte
}

func scanRow(r pgx.CollectableRow) (row, error) {
	var out row
	err := r.Scan(&out.key, &out.rev, &out.value)
	return out, err
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// counterSQL selects the project's current revision, which is its counter or
// initialRevision before its first write.
const counterSQL = `COALESCE((SELECT rv FROM objects WHERE project = $1 AND key = ''), $2)`

// currentRevision returns project's last revision, or initialRevision before
// its first write.
func currentRevision(ctx context.Context, q querier, project string) (int64, error) {
	var rev int64
	err := q.QueryRow(ctx, `SELECT `+counterSQL, project, initialRevision).Scan(&rev)
	return rev, err
}

// keysSQL selects the rows of the keys, the counter's "" among them.
const keysSQL = `SELECT key, rv, deleted, value FROM objects WHERE project = $1 AND key = ANY($2)`

// collectKeys reads keysSQL's rows and returns the project's current revision
// and its live rows.
func collectKeys(rows pgx.Rows) (int64, map[string]*row, error) {
	current := int64(initialRevision)
	live := map[string]*row{}
	var r row
	var deleted bool
	_, err := pgx.ForEachRow(rows, []any{&r.key, &r.rev, &deleted, &r.value}, func() error {
		switch {
		case r.key == "":
			current = r.rev
		case !deleted:
			live[r.key] = &row{key: r.key, rev: r.rev, value: r.value}
		}
		return nil
	})
	return current, live, err
}

// tryCommitBatch makes one attempt at writing batch, in one serializable
// transaction that locks the project's counter row, so revisions follow commit
// order. A project's first write has no counter row to lock, so concurrent
// first writes conflict on reading its absence instead, and all but one
// retry.
func (b *Backend) tryCommitBatch(ctx context.Context, project string, batch writeBatch) ([]writeResult, error) {
	conn, err := b.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	defer endTransaction(ctx, conn.Conn())

	// The transaction takes two round trips, with BEGIN sent alongside the
	// locking read and COMMIT alongside the UPSERT. That keeps the counter
	// locked for one round trip less than a statement at a time would.
	var counter int64
	var live map[string]*row
	var begin pgx.Batch
	begin.Queue(`BEGIN ISOLATION LEVEL SERIALIZABLE`)
	begin.Queue(keysSQL+` ORDER BY key FOR UPDATE`, project, batch.keys).Query(func(rows pgx.Rows) error {
		var err error
		counter, live, err = collectKeys(rows)
		return err
	})
	if err := conn.SendBatch(ctx, &begin).Close(); err != nil {
		return nil, err
	}
	results := make([]writeResult, len(batch.writes))
	var rows upsertRows
	for i, w := range batch.writes {
		l := live[w.key]
		results[i].live = l
		if !w.matches(l) {
			continue
		}
		value := w.value
		if w.deleted {
			// The tombstone keeps the last value, the delete's before-image.
			value = l.value
		}
		results[i].rev, results[i].written = counter+int64(len(rows.keys))+1, true
		rows.add(w.key, results[i].rev, w.deleted, value, w.ttlSeconds)
	}
	if len(rows.keys) == 0 {
		return results, nil
	}
	// Commit timestamps come from the feed. Selecting cluster_logical_timestamp()
	// here would fix the transaction's timestamp, so CockroachDB could no longer
	// refresh it past concurrent reads and would abort it instead.
	var commit pgx.Batch
	commit.Queue(upsertSQL, project, rows.keys, rows.revs, rows.deleted, rows.values, rows.ttls, counter+int64(len(rows.keys)))
	commit.Queue(`COMMIT`)
	if err := conn.SendBatch(ctx, &commit).Close(); err != nil {
		return nil, err
	}
	return results, nil
}

// endTransaction rolls back a transaction still open on conn, after a batch
// failed or wrote nothing. When it can't, the pool closes the connection
// instead of reusing it.
func endTransaction(ctx context.Context, conn *pgx.Conn) {
	if conn.PgConn().TxStatus() == 'I' {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = conn.Exec(ctx, `ROLLBACK`)
}

// upsertSQL writes a batch's rows, passed as one array per column, and moves
// the project's counter to the batch's last revision. Its text is the same
// for every batch, so every connection prepares it once.
const upsertSQL = `UPSERT INTO objects (project, key, rv, deleted, value, expires_at, deleted_at)
	SELECT $1, k, r, d, v, CASE WHEN t > 0 THEN now() + t * INTERVAL '1 second' END, CASE WHEN d THEN now() END
	FROM unnest($2::STRING[], $3::INT8[], $4::BOOL[], $5::BYTES[], $6::INT8[]) AS w (k, r, d, v, t)
	UNION ALL
	SELECT $1, '', $7, false, ''::BYTES, NULL::TIMESTAMPTZ, NULL::TIMESTAMPTZ`

// upsertRows is a batch's rows, by column.
type upsertRows struct {
	keys    []string
	revs    []int64
	deleted []bool
	values  [][]byte
	ttls    []int64
}

func (u *upsertRows) add(key string, rev int64, deleted bool, value []byte, ttl uint64) {
	u.keys = append(u.keys, key)
	u.revs = append(u.revs, rev)
	u.deleted = append(u.deleted, deleted)
	u.values = append(u.values, value)
	u.ttls = append(u.ttls, int64(ttl))
}
