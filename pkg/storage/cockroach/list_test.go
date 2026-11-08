package cockroach

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/apis/example"
	"k8s.io/apiserver/pkg/storage"
)

// memoryRows is a rowSource over rows in key order. It records the size of
// each page read.
type memoryRows struct {
	rows  []row
	sizes []int64
}

func (m *memoryRows) page(_ context.Context, start string, size int64) (rowPage, error) {
	m.sizes = append(m.sizes, size)
	var p rowPage
	for _, r := range m.rows {
		if r.key < start {
			continue
		}
		if size > 0 && int64(len(p.rows)) == size {
			p.hasMore = true
			break
		}
		p.rows = append(p.rows, r)
		p.scannedTo = r.key
	}
	return p, nil
}

func (m *memoryRows) count(_ context.Context, start string) (int64, error) {
	var n int64
	for _, r := range m.rows {
		if r.key >= start {
			n++
		}
	}
	return n, nil
}

// podRows stores pods named by names as the store would, labelled with
// whether their index is even.
func podRows(t *testing.T, s *store, names ...string) []row {
	t.Helper()
	var rows []row
	for i, name := range names {
		pod := newPod(name)
		pod.Labels = map[string]string{"even": fmt.Sprint(i%2 == 0)}
		data, err := runtime.Encode(s.codec, pod)
		require.NoError(t, err)
		key := "/pods/test-ns/" + name
		data, err = s.transformer.TransformToStorage(t.Context(), data, s.authData(key))
		require.NoError(t, err)
		rows = append(rows, row{key: key, rev: int64(i + 2), value: data})
	}
	return rows
}

func TestCollect(t *testing.T) {
	s, err := newStore(&Backend{}, testConfig("project"))
	require.NoError(t, err)
	rows := podRows(t, s, "a", "b", "c", "d", "e", "f")
	evenOnly := storage.SelectionPredicate{Label: mustSelector(t, "even=true"), GetAttrs: storage.DefaultClusterScopedAttr}
	for name, tc := range map[string]struct {
		pred    storage.SelectionPredicate
		want    []string
		lastKey string
		hasMore bool
		pages   int
	}{
		"everything":                 {pred: storage.Everything, want: []string{"a", "b", "c", "d", "e", "f"}, lastKey: "/pods/test-ns/f", pages: 1},
		"a limit stops at the limit": {pred: withLimit(storage.Everything, 2), want: []string{"a", "b"}, lastKey: "/pods/test-ns/b", hasMore: true, pages: 1},
		"a filter grows short pages": {pred: withLimit(evenOnly, 2), want: []string{"a", "c"}, lastKey: "/pods/test-ns/c", hasMore: true, pages: 2},
		"the range ends first":       {pred: withLimit(evenOnly, 5), want: []string{"a", "c", "e"}, lastKey: "/pods/test-ns/f", pages: 2},
	} {
		t.Run(name, func(t *testing.T) {
			src := &memoryRows{rows: rows}
			var res listResult
			q := listQuery{prefix: "/pods/", recursive: true, pred: tc.pred, newItem: func() runtime.Object { return &example.Pod{} }}
			require.NoError(t, s.collect(t.Context(), src, q, &res))
			var got []string
			for _, item := range res.items {
				got = append(got, item.(*example.Pod).Name)
			}
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.lastKey, res.lastKey)
			require.Equal(t, tc.hasMore, res.hasMore)
			require.Len(t, src.sizes, tc.pages)
		})
	}
}

// A filter that matches little grows its pages only up to maxPageSize.
func TestCollectCapsPageGrowth(t *testing.T) {
	s, err := newStore(&Backend{}, testConfig("project"))
	require.NoError(t, err)
	names := make([]string, 3*maxPageSize)
	for i := range names {
		names[i] = fmt.Sprintf("pod-%05d", i)
	}
	src := &memoryRows{rows: podRows(t, s, names...)}
	none := storage.SelectionPredicate{Label: mustSelector(t, "even=never"), GetAttrs: storage.DefaultClusterScopedAttr, Limit: 1}
	q := listQuery{prefix: "/pods/", recursive: true, pred: none, newItem: func() runtime.Object { return &example.Pod{} }}
	require.NoError(t, s.collect(t.Context(), src, q, &listResult{}))
	require.Equal(t, int64(maxPageSize), slices.Max(src.sizes))
}

// An exact list that can't read its revision lists again once the feed has
// delivered the next one, including when it arrived just after the miss.
func TestAwaitingFeedListsAgainOnceTheRevisionArrives(t *testing.T) {
	for name, caughtUp := range map[string]bool{"waits for the feed": false, "feed already caught up": true} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := memoryHub()
				if caughtUp {
					arrive(t, h, 3, 2, 3)
				}
				calls := 0
				next := listFunc(func(context.Context, listQuery) (listResult, error) {
					calls++
					return listResult{rev: 2, expired: calls == 1}, nil
				})
				done := make(chan error, 1)
				go func() {
					_, err := awaitingLists{hub: h, project: "project", next: next}.list(t.Context(), listQuery{rev: 2})
					done <- err
				}()
				if !caughtUp {
					synctest.Wait()
					require.Empty(t, done)
					arrive(t, h, 3, 2, 3)
				}
				require.NoError(t, <-done)
				require.Equal(t, 2, calls)
			})
		})
	}
}

func mustSelector(t *testing.T, s string) labels.Selector {
	t.Helper()
	selector, err := labels.Parse(s)
	require.NoError(t, err)
	return selector
}

func withLimit(p storage.SelectionPredicate, limit int64) storage.SelectionPredicate {
	p.Limit = limit
	return p
}

// A resource version past every revision is refused rather than read as the
// current state.
func TestListRejectsResourceVersionPastEveryRevision(t *testing.T) {
	ctx, s := testSetup(t)
	createPod(ctx, t, s, "pod")
	for name, opts := range map[string]storage.ListOptions{
		"exact": {ResourceVersion: "9223372036854775808", ResourceVersionMatch: metav1.ResourceVersionMatchExact, Predicate: storage.Everything, Recursive: true},
		"paged": {ResourceVersion: "18446744073709551615", Predicate: withLimit(storage.Everything, 1), Recursive: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := s.GetList(ctx, "/pods/", opts, &example.PodList{})
			require.True(t, apierrors.IsBadRequest(err), "%v", err)
		})
	}
}
