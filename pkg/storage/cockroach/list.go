package cockroach

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

const (
	listExpired     = "The resourceVersion for the provided list is too old."
	continueExpired = "The provided continue parameter is too old to display a consistent list result. " +
		"You can start a new list without the continue parameter."
	inconsistentContinue = "The provided continue parameter is too old to display a consistent list result. " +
		"You can start a new list without the continue parameter, or use the continue token in this response to " +
		"retrieve the remainder of the results. Continuing with the provided token results in an inconsistent list - " +
		"objects that were created, modified, or deleted between the time the first chunk was returned and now may " +
		"show up in the list."
)

// listQuery is one page of a list.
type listQuery struct {
	prefix      string
	continueKey string
	rev         int64 // the exact revision to read; 0 reads the current state
	recursive   bool
	pred        storage.SelectionPredicate
	newItem     func() runtime.Object
}

// keyRange returns the keys the page reads.
func (q listQuery) keyRange() (start, end string) {
	start, end = q.prefix, prefixEnd(q.prefix)
	if q.continueKey != "" {
		start = q.continueKey
	}
	if !q.recursive {
		end = q.prefix + "\x00"
	}
	return start, end
}

// firstPageSize returns how many rows the page's first read takes, which is
// its limit or every row.
func (q listQuery) firstPageSize() int64 { return max(q.pred.Limit, 0) }

type listResult struct {
	items      []runtime.Object
	rev        int64 // the project revision the list reflects
	lastKey    string
	itemsCount int64
	hasMore    bool
	expired    bool // the snapshot for the requested revision is not known here
	tooLarge   bool
}

// lister reads one page of a list. A store's reads are a stack of listers,
// each adding one concern.
type lister interface {
	list(ctx context.Context, q listQuery) (listResult, error)
}

// newLister stacks a store's listers, which from the top are:
//
//  1. awaitingLists lists again once this replica's feed catches up, when
//     another replica wrote the requested revision moments ago.
//  2. retryingLists reruns a read that aborted on a conflict. It sits below
//     the wait, so a conflict never repeats the wait.
//  3. observedLists times each attempt in the operation metrics.
//  4. read reads one page.
func newLister(read listFunc, h *hub, project string) lister {
	var l lister = observedLists{read}
	l = retryingLists{l}
	return awaitingLists{hub: h, project: project, next: l}
}

// listFunc adapts a function to a lister.
type listFunc func(ctx context.Context, q listQuery) (listResult, error)

func (f listFunc) list(ctx context.Context, q listQuery) (listResult, error) { return f(ctx, q) }

// retryingLists runs next again when its transaction aborts on a conflict.
type retryingLists struct {
	next lister
}

func (r retryingLists) list(ctx context.Context, q listQuery) (listResult, error) {
	return retry(ctx, "list", func() (listResult, error) { return r.next.list(ctx, q) })
}

// observedLists records each read next makes in the operation metrics.
type observedLists struct {
	next lister
}

func (o observedLists) list(ctx context.Context, q listQuery) (listResult, error) {
	start := time.Now()
	res, err := o.next.list(ctx, q)
	observeOperation("list", start, err)
	return res, err
}

// awaitingLists lists again once this replica's feed delivers the revision
// after the requested one, when next could not read the requested revision
// because another replica wrote it, or the one after it, moments ago.
type awaitingLists struct {
	hub     *hub
	project string
	next    lister
}

func (a awaitingLists) list(ctx context.Context, q listQuery) (listResult, error) {
	res, err := a.next.list(ctx, q)
	if err != nil || !res.expired {
		return res, err
	}
	switch err := a.hub.awaitRevision(ctx, a.project, q.rev+1, "exact_read"); {
	case storage.IsTooLargeResourceVersion(err):
		// The feed never delivered it, so the read stays expired.
		return res, nil
	case err != nil:
		return res, err
	}
	return a.next.list(ctx, q)
}

func (s *store) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	keyPrefix, err := s.prepareKey(key, opts.Recursive)
	if err != nil {
		return err
	}
	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		return err
	}
	v, err := conversion.EnforcePtr(listPtr)
	if err != nil || v.Kind() != reflect.Slice {
		return fmt.Errorf("need ptr to slice: %v", err)
	}
	withRev, continueKey, err := storage.ValidateListOptions(keyPrefix, s.versioner, opts)
	if err != nil {
		return err
	}
	if withRev < 0 && continueKey == "" {
		// ValidateListOptions turns a resource version past every revision,
		// which are int64, into a negative one.
		return apierrors.NewBadRequest(fmt.Sprintf("invalid resource version %q: larger than any revision", opts.ResourceVersion))
	}
	res, err := s.lister.list(ctx, listQuery{
		prefix:      keyPrefix,
		continueKey: continueKey,
		rev:         withRev,
		recursive:   opts.Recursive,
		pred:        opts.Predicate,
		newItem:     getNewItemFunc(listObj, v),
	})
	if err != nil {
		// As with etcd, the items decoded before the failure stay in the list.
		setItems(v, res.items)
		return err
	}
	switch {
	case res.expired && opts.Predicate.Continue != "":
		return expiredContinue(continueKey, keyPrefix)
	case res.expired:
		return apierrors.NewResourceExpired(listExpired)
	case res.tooLarge:
		return storage.NewTooLargeResourceVersionError(uint64(withRev), uint64(res.rev), 0)
	}
	if withRev == 0 {
		if err := s.validateMinimumResourceVersion(opts.ResourceVersion, uint64(res.rev)); err != nil {
			return err
		}
	}
	setItems(v, res.items)
	continueValue, remainingItemCount, err := storage.PrepareContinueToken(res.lastKey, keyPrefix, res.rev, res.itemsCount, res.hasMore, opts)
	if err != nil {
		return err
	}
	return s.versioner.UpdateList(listObj, uint64(res.rev), continueValue, remainingItemCount)
}

// readList reads one page in one read-only snapshot.
func (s *store) readList(ctx context.Context, q listQuery) (listResult, error) {
	rows, err := s.openListRows(ctx, q)
	if err != nil {
		return listResult{}, err
	}
	defer rows.close()
	res := listResult{rev: rows.rev, tooLarge: rows.tooLarge, expired: rows.expired}
	if rows.tooLarge || rows.expired {
		return res, nil
	}
	if err := s.collect(ctx, rows, q, &res); err != nil {
		return res, err
	}
	// The remaining count goes out only for unfiltered lists.
	if res.hasMore && q.pred.Empty() {
		start, _ := q.keyRange()
		if res.itemsCount, err = rows.count(ctx, start); err != nil {
			return res, err
		}
	}
	return res, nil
}

// listRows is the project as one list page sees it: the rows of its range,
// at rev, in a snapshot that close ends. A revision this replica can't read
// leaves tooLarge or expired set, and no rows.
type listRows struct {
	rowSource
	rev      int64
	tooLarge bool
	expired  bool
	close    func()
}

// openListRows opens the snapshot q reads and the rows it sees there: the
// current state, an exact revision, or a revision inside a batch, rebuilt from
// the rows before it.
func (s *store) openListRows(ctx context.Context, q listQuery) (rows listRows, err error) {
	conn, err := s.b.pool.Acquire(ctx)
	if err != nil {
		return rows, err
	}
	rows.close = func() {
		endTransaction(ctx, conn.Conn())
		conn.Release()
	}
	defer func() {
		if err != nil {
			rows.close()
		}
	}()
	start, end := q.keyRange()
	live := liveRows{q: conn, project: s.project, end: end}
	rows.rowSource = live
	var prefetch func(*pgx.Batch)
	if q.rev == 0 {
		// A list of the current state reads its first page with BEGIN.
		prefetch = func(b *pgx.Batch) {
			size := q.firstPageSize()
			rows.rowSource = &prefetchedRows{rowSource: live, start: start, size: size, first: live.queuePage(b, start, size)}
		}
	}
	snap, err := s.openSnapshot(ctx, conn, q.rev, prefetch)
	if err != nil {
		return rows, err
	}
	rows.rev = snap.currentRev
	switch {
	case q.rev > snap.currentRev && snap.fromSuccessor:
		// rev and rev+1 committed together after all, so the instant before
		// rev+1 is before rev too, and this replica doesn't know rev's time.
		rows.expired = true
		return rows, nil
	case q.rev > snap.currentRev:
		rows.tooLarge = true
		return rows, nil
	case q.rev > 0 && q.rev < snap.currentRev && !snap.atRequestedRev:
		// Neither q.rev's commit time nor the next revision's is known here.
		rows.expired = true
		return rows, nil
	case q.rev > 0:
		rows.rev = q.rev
	}
	if q.rev > 0 && q.rev < snap.currentRev {
		// q.rev's batch went on past it, and every write in a batch shares one
		// timestamp, so no snapshot holds the state at q.rev alone.
		rows.rowSource, err = s.openBatchRows(ctx, conn, snap.ts, q.rev, start, end)
	}
	return rows, err
}

// maxPageSize caps the pages a filtered list grows to, as etcd3 does, so a
// selective filter over a large range doesn't hold it all at once.
const maxPageSize = 10000

// collect decodes and filters the query's rows from src into res, until the
// query's limit or the end of the range. A page the filter leaves short makes
// the next one larger.
func (s *store) collect(ctx context.Context, src rowSource, q listQuery, res *listResult) error {
	start, _ := q.keyRange()
	limit := q.pred.Limit
	size := q.firstPageSize()
	errs := listErrors{aggregate: s.unsafeDeletion}
	for {
		p, err := src.page(ctx, start, size)
		if err != nil {
			return err
		}
		for _, r := range p.rows {
			if limit > 0 && int64(len(res.items)) >= limit {
				res.hasMore = true
				return errs.err(s.groupResource)
			}
			res.lastKey = r.key
			obj, err := s.decodeListItem(ctx, r, q.newItem)
			if err != nil {
				if errs.add(r.key, err) {
					return errs.err(s.groupResource)
				}
				continue
			}
			if matched, err := q.pred.Matches(obj); err == nil && matched {
				res.items = append(res.items, obj)
			}
		}
		if p.scannedTo != "" {
			res.lastKey = p.scannedTo
		}
		if !p.hasMore {
			return errs.err(s.groupResource)
		}
		if limit > 0 && int64(len(res.items)) >= limit {
			res.hasMore = true
			return errs.err(s.groupResource)
		}
		start = res.lastKey + "\x00"
		size = min(size*2, maxPageSize)
	}
}

func (s *store) decodeListItem(ctx context.Context, r row, newItemFunc func() runtime.Object) (runtime.Object, error) {
	data, _, err := s.transformer.TransformFromStorage(ctx, r.value, s.authData(r.key))
	if err != nil {
		if s.corrupt(err) {
			return nil, err
		}
		return nil, storage.NewInternalError(fmt.Errorf("unable to transform key %q: %w", r.key, err))
	}
	return s.decoder.DecodeListItem(ctx, data, uint64(r.rev), newItemFunc)
}

// snapshot is a read-only transaction open on a connection. Reads through the
// connection see the project at ts.
type snapshot struct {
	ts             hlc
	currentRev     int64
	atRequestedRev bool
	fromSuccessor  bool
}

// openSnapshot opens a snapshot on conn at a timestamp where the project is at
// rev, when this replica knows one, and at the current state otherwise. The
// reads that prefetch queues run in the snapshot.
func (s *store) openSnapshot(ctx context.Context, conn *pgxpool.Conn, rev int64, prefetch func(*pgx.Batch)) (snapshot, error) {
	var snap snapshot
	if rev > 0 {
		snap.ts, snap.fromSuccessor, snap.atRequestedRev = s.b.revTS.readAt(s.project, rev)
	}
	var b pgx.Batch
	if snap.atRequestedRev {
		b.Queue(snapshotSQL(snap.ts))
		b.Queue(`SELECT `+counterSQL, s.project, initialRevision).QueryRow(func(r pgx.Row) error { return r.Scan(&snap.currentRev) })
	} else {
		b.Queue(`BEGIN TRANSACTION READ ONLY`)
		b.Queue(clusterTimeSQL+`, `+counterSQL, s.project, initialRevision).QueryRow(func(r pgx.Row) error {
			return r.Scan(&snap.ts, &snap.currentRev)
		})
	}
	if prefetch != nil {
		prefetch(&b)
	}
	// BEGIN, the read of the snapshot's timestamp and revision, and the
	// prefetched reads all go in one round trip.
	return snap, conn.SendBatch(ctx, &b).Close()
}

// rowSource reads a project's rows in a key range, in key order, as a list
// sees them.
type rowSource interface {
	// page reads up to size rows from start, or every row when size is 0.
	page(ctx context.Context, start string, size int64) (rowPage, error)
	// count returns the number of rows from start to the end of the range.
	count(ctx context.Context, start string) (int64, error)
}

type rowPage struct {
	rows      []row
	scannedTo string
	hasMore   bool
}

// liveRows reads the live rows of a range.
type liveRows struct {
	q       querier
	project string
	end     string
}

func (l liveRows) page(ctx context.Context, start string, size int64) (rowPage, error) {
	return l.read(ctx, start, size, `NOT deleted`)
}

// read reads rows from start that meet cond, which may refer to the
// arguments after the range's.
func (l liveRows) read(ctx context.Context, start string, size int64, cond string, args ...any) (rowPage, error) {
	query, args := l.pageQuery(start, size, cond, args...)
	rows, err := l.q.Query(ctx, query, args...)
	if err != nil {
		return rowPage{}, err
	}
	all, err := pgx.CollectRows(rows, scanRow)
	return pageOf(all, size), err
}

// queuePage queues on b the read of the live page from start, which lands in
// the page returned once b runs.
func (l liveRows) queuePage(b *pgx.Batch, start string, size int64) *rowPage {
	p := new(rowPage)
	query, args := l.pageQuery(start, size, `NOT deleted`)
	b.Queue(query, args...).Query(func(rows pgx.Rows) error {
		all, err := pgx.CollectRows(rows, scanRow)
		*p = pageOf(all, size)
		return err
	})
	return p
}

// pageQuery selects up to size rows from start that meet cond, and one more,
// which shows whether rows remain.
func (l liveRows) pageQuery(start string, size int64, cond string, args ...any) (string, []any) {
	args = append([]any{l.project, start, l.end}, args...)
	query := `SELECT key, rv, value FROM objects WHERE project = $1 AND key >= $2 AND key < $3 AND ` + cond + ` ORDER BY key`
	if size > 0 {
		query += fmt.Sprintf(` LIMIT $%d`, len(args)+1)
		args = append(args, size+1)
	}
	return query, args
}

func pageOf(all []row, size int64) rowPage {
	var p rowPage
	if p.hasMore = size > 0 && int64(len(all)) > size; p.hasMore {
		all = all[:size]
	}
	p.rows = all
	if len(all) > 0 {
		p.scannedTo = all[len(all)-1].key
	}
	return p
}

// prefetchedRows serves its first page from a read made in advance, and the
// rest from its rowSource.
type prefetchedRows struct {
	rowSource
	start string
	size  int64
	first *rowPage
}

func (p *prefetchedRows) page(ctx context.Context, start string, size int64) (rowPage, error) {
	if first := p.first; first != nil && start == p.start && size == p.size {
		p.first = nil
		return *first, nil
	}
	return p.rowSource.page(ctx, start, size)
}

func (l liveRows) count(ctx context.Context, start string) (int64, error) {
	return l.countWhere(ctx, start, `NOT deleted`)
}

// countWhere counts the rows from start that meet cond, which may refer to
// the arguments after the range's.
func (l liveRows) countWhere(ctx context.Context, start, cond string, args ...any) (int64, error) {
	var n int64
	err := l.q.QueryRow(ctx, `SELECT count(*) FROM objects WHERE project = $1 AND key >= $2 AND key < $3 AND `+cond,
		append([]any{l.project, start, l.end}, args...)...).Scan(&n)
	return n, err
}

// batchRows reads a range at a revision inside a batch, from a snapshot at
// the batch's end. Rows the batch wrote after rev show as they were before
// the batch, and rows it created after rev don't show.
type batchRows struct {
	liveRows
	rev        int64
	liveBefore map[string]row
}

func (b batchRows) page(ctx context.Context, start string, size int64) (rowPage, error) {
	p, err := b.read(ctx, start, size, `(NOT deleted OR rv > $4)`, b.rev)
	if err != nil {
		return p, err
	}
	rows := p.rows[:0]
	for _, r := range p.rows {
		if r.rev > b.rev {
			before, ok := b.liveBefore[r.key]
			if !ok {
				continue
			}
			r = before
		}
		rows = append(rows, r)
	}
	p.rows = rows
	return p, nil
}

func (b batchRows) count(ctx context.Context, start string) (int64, error) {
	n, err := b.countWhere(ctx, start, `NOT deleted AND rv <= $4`, b.rev)
	for key := range b.liveBefore {
		if key >= start && key < b.end {
			n++
		}
	}
	return n, err
}

// openBatchRows returns the rows of [start, end) as of rev, which a batch
// committed at ts wrote before its other writes. It leaves conn in a
// snapshot at ts.
func (s *store) openBatchRows(ctx context.Context, conn *pgxpool.Conn, ts hlc, rev int64, start, end string) (rowSource, error) {
	// The keys the batch wrote after rev read as they were before the batch,
	// which is also their state at rev, since a batch writes each key once. No
	// other write commits between the batch's timestamp and the one before it,
	// because every write locks the project's counter row.
	live := liveRows{q: conn, project: s.project, end: end}
	rows, err := conn.Query(ctx, `SELECT key FROM objects WHERE project = $1 AND key >= $2 AND key < $3 AND rv > $4`, s.project, start, end, rev)
	if err != nil {
		return nil, err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(keys) == 0 {
		return live, err
	}

	// CockroachDB fixes a transaction's timestamp when it starts, so reading at
	// another timestamp takes another transaction. Every transaction runs on
	// conn, which this read already holds, because taking a second connection
	// could deadlock the pool when concurrent reads each hold one and wait for
	// another. The read before the batch and the return to a snapshot at ts
	// go in one round trip.
	var prior []row
	var b pgx.Batch
	b.Queue(`COMMIT`)
	b.Queue(snapshotSQL(ts.prev()))
	b.Queue(`SELECT key, rv, value FROM objects WHERE project = $1 AND key = ANY($2) AND NOT deleted`, s.project, keys).Query(func(rows pgx.Rows) error {
		prior, err = pgx.CollectRows(rows, scanRow)
		return err
	})
	b.Queue(`COMMIT`)
	b.Queue(snapshotSQL(ts))
	if err := conn.SendBatch(ctx, &b).Close(); err != nil {
		return nil, err
	}
	liveBefore := make(map[string]row, len(prior))
	for _, r := range prior {
		liveBefore[r.key] = r
	}
	return batchRows{liveRows: live, rev: rev, liveBefore: liveBefore}, nil
}

func setItems(v reflect.Value, items []runtime.Object) {
	v.Set(reflect.MakeSlice(v.Type(), 0, len(items)))
	for _, item := range items {
		v.Set(reflect.Append(v, reflect.ValueOf(item).Elem()))
	}
}

func getNewItemFunc(listObj runtime.Object, v reflect.Value) func() runtime.Object {
	if unstructuredList, isUnstructured := listObj.(*unstructured.UnstructuredList); isUnstructured {
		if apiVersion := unstructuredList.GetAPIVersion(); len(apiVersion) > 0 {
			return func() runtime.Object {
				return &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion}}
			}
		}
	}
	elem := v.Type().Elem()
	return func() runtime.Object {
		return reflect.New(elem).Interface().(runtime.Object)
	}
}

// expiredContinue returns the error for a continue token whose snapshot is
// gone. The error carries a new token that continues from the same key at the
// current state, as etcd does, and resource version -1 marks that token.
func expiredContinue(continueKey, keyPrefix string) error {
	token, err := storage.EncodeContinue(continueKey, keyPrefix, -1)
	if err != nil {
		return apierrors.NewResourceExpired(continueExpired)
	}
	statusErr := apierrors.NewResourceExpired(inconsistentContinue)
	statusErr.ErrStatus.ListMeta.Continue = token
	return statusErr
}
