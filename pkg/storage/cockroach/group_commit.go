package cockroach

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// Group commit's batching rules are also part of the exact-read contract.
// Changing them requires checking historical reads, not just writes.
//
// Every write to a project locks the project's counter row, so writes to one
// project commit one at a time, one transaction per write. When many writes
// to one project arrive at once, as when a controller reconciles a large
// project, they queue behind separate transactions, increasing latency and
// potentially hitting request deadlines. Group commit lets the writes that pile
// up while one transaction runs share the next one. A write to a quiet project
// still starts its transaction right away.
//
// Sharing a transaction means sharing its commit timestamp, and exact reads
// are built around that:
//
//   - A batch never writes the same key twice. A second write to a key waits
//     for the next batch, so every key in a batch has one state before it and
//     one after, and openBatchRows can rebuild a revision inside the batch
//     from the rows as they were before it.
//   - A revision inside a batch has no database snapshot, since the whole
//     batch becomes visible at once. Exact lists at such a revision go
//     through openBatchRows.
//   - Two revisions in one batch share a commit time, so the moment just
//     before rev+1 isn't always the state at rev. readAt reports when it falls
//     back to rev+1's time, and openListRows validates it.
//
// A batch runs under the group committer's context, not a caller's. When it
// fails, ambiguous commits included, every write in it gets the error. A write
// whose caller gives up before its batch starts is dropped. Once its batch has
// started, it may still land, and the caller sees only its context's error, as
// with an etcd request that times out. newCommitter describes how admission
// wraps the batches.

const (
	// maxBatchWrites and maxBatchBytes bound one batch. A batch always takes
	// at least one write, however large.
	maxBatchWrites = 256
	maxBatchBytes  = 4 << 20

	// batchTimeout bounds one batch, retries included. It runs under the
	// group committer's context, never a caller's.
	batchTimeout = 30 * time.Second
)

// errBackendClosed answers the writes still queued when the backend closes.
var errBackendClosed = errors.New("cockroach storage: the backend is closing")

// groupCommitter commits a project's concurrent writes together, one batch
// per call to next. A project's writes queue while a goroutine commits its
// batches, and the goroutine ends once none are left. Quiet projects wait for
// nothing, because a write that finds no goroutine starts one.
type groupCommitter struct {
	next   batchCommitter
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	queues queues
}

// queues holds each project's queued writes. Only its methods change it, so
// a project has a queue exactly while its goroutine runs.
type queues map[string][]*queuedWrite

// push queues c for project, and reports whether project had no queue, in
// which case the caller starts the project's goroutine.
func (q queues) push(project string, c *queuedWrite) (start bool) {
	queued, running := q[project]
	q[project] = append(queued, c)
	return !running
}

// next removes project's next batch. With none left, it removes the project
// and reports false, and the project's goroutine ends.
func (q queues) next(project string) ([]*queuedWrite, bool) {
	batch, rest := nextBatch(q[project])
	if batch == nil {
		delete(q, project)
		return nil, false
	}
	q[project] = rest
	return batch, true
}

// drain removes project and returns its queued writes, and the project's
// goroutine ends.
func (q queues) drain(project string) []*queuedWrite {
	queued := q[project]
	delete(q, project)
	return queued
}

// withdraw drops c from project's queue, unless a batch has taken it.
func (q queues) withdraw(project string, c *queuedWrite) {
	if queued, ok := q[project]; ok {
		q[project] = slices.DeleteFunc(queued, func(p *queuedWrite) bool { return p == c })
	}
}

// queuedWrite is one write waiting for its batch.
type queuedWrite struct {
	write
	ctx    context.Context
	queued time.Time
	done   chan writeOutcome
}

func newQueuedWrite(ctx context.Context, w write) *queuedWrite {
	return &queuedWrite{write: w, ctx: ctx, queued: time.Now(), done: make(chan writeOutcome, 1)}
}

func (c *queuedWrite) finish(result writeResult, err error) { c.done <- writeOutcome{result, err} }

func newGroupCommitter(next batchCommitter) *groupCommitter {
	ctx, cancel := context.WithCancel(context.Background())
	return &groupCommitter{next: next, ctx: ctx, cancel: cancel, queues: queues{}}
}

// stop ends every project's goroutine. Writes still queued get
// errBackendClosed.
func (g *groupCommitter) stop() {
	g.mu.Lock()
	g.cancel()
	g.mu.Unlock()
	g.wg.Wait()
}

// commit queues w for the next batch. A caller whose context ends first gets
// its context's error. A write dropped before its batch starts never lands,
// but one already in a batch may still land, as with an etcd request that
// times out.
func (g *groupCommitter) commit(ctx context.Context, project string, w write) (writeResult, error) {
	c := newQueuedWrite(ctx, w)
	g.mu.Lock()
	if g.ctx.Err() != nil {
		g.mu.Unlock()
		return writeResult{}, errBackendClosed
	}
	if g.queues.push(project, c) {
		g.wg.Go(func() { g.run(project) })
	}
	g.mu.Unlock()

	select {
	case r := <-c.done:
		return r.result, r.err
	case <-ctx.Done():
		g.withdraw(project, c)
		return writeResult{}, ctx.Err()
	}
}

func (g *groupCommitter) run(project string) {
	for batch, ok := g.take(project); ok; batch, ok = g.take(project) {
		g.commitBatch(project, batch)
	}
}

// take removes project's next batch, and reports false once the project's
// goroutine should end. Once the group committer stops, every queued write
// gets errBackendClosed.
func (g *groupCommitter) take(project string) ([]*queuedWrite, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ctx.Err() != nil {
		for _, c := range g.queues.drain(project) {
			c.finish(writeResult{}, errBackendClosed)
		}
		return nil, false
	}
	return g.queues.next(project)
}

func (g *groupCommitter) withdraw(project string, c *queuedWrite) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.queues.withdraw(project, c)
}

// nextBatch splits pending into the next batch and the writes left for
// later. The batch takes writes in arrival order, within the batch limits, of
// keys no earlier queued write is for. Writes whose callers gave up are
// answered and dropped.
func nextBatch(pending []*queuedWrite) (batch, rest []*queuedWrite) {
	keys := map[string]bool{}
	size := 0
	for _, c := range pending {
		if err := c.ctx.Err(); err != nil {
			c.finish(writeResult{}, err)
			continue
		}
		full := len(batch) > 0 && (len(batch) == maxBatchWrites || size+len(c.value) > maxBatchBytes)
		seen := keys[c.key]
		keys[c.key] = true
		if full || seen {
			rest = append(rest, c)
			continue
		}
		size += len(c.value)
		batch = append(batch, c)
	}
	return batch, rest
}

// commitBatch commits batch in one call to next and answers every write in
// it.
func (g *groupCommitter) commitBatch(project string, batch []*queuedWrite) {
	start := time.Now()
	writes := make([]write, len(batch))
	for i, c := range batch {
		batchWait.Observe(start.Sub(c.queued).Seconds())
		writes[i] = c.write
	}
	batchSize.Observe(float64(len(writes)))

	ctx, cancel := context.WithTimeout(g.ctx, batchTimeout)
	defer cancel()
	b, err := newWriteBatch(writes...)
	var results []writeResult
	if err == nil {
		results, err = g.next.commitBatch(ctx, project, b)
	}
	for i, c := range batch {
		if err != nil {
			// Every write gets the error, including an ambiguous commit's.
			c.finish(writeResult{}, err)
			continue
		}
		c.finish(results[i], nil)
	}
}
