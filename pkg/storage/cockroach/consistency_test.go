package cockroach

import (
	"cmp"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/apis/example"
	"k8s.io/apiserver/pkg/storage"
)

// A project's first writes race to create its counter row, from two replicas
// with both commit paths. They still get gapless revisions of their own.
func TestConcurrentFirstWrites(t *testing.T) {
	_, base := testSetup(t)
	replicas := []testStore{otherReplica(t, base), otherReplica(t, base, GroupCommit(false))}
	ctx := t.Context()
	const writers = 32
	revs := make([]int64, writers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			<-start
			revs[i] = parseRV(t, createPod(ctx, t, replicas[i%2], fmt.Sprintf("pod-%02d", i)).ResourceVersion)
		})
	}
	close(start)
	wg.Wait()
	slices.Sort(revs)
	for i, rev := range revs {
		require.Equal(t, int64(initialRevision+1+i), rev, "revisions %v", revs)
	}
	current, err := base.GetCurrentResourceVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(initialRevision+writers), current)
}

// TestConcurrentWritesInOneProject drives many writers at one project. No
// update may be lost and no two writes may share a resource version.
func TestConcurrentWritesInOneProject(t *testing.T) {
	ctx, s := testSetup(t)
	const updaters, updatesEach, creators = 30, 10, 20

	counterKey := "/pods/ns/counter"
	if err := s.Create(ctx, counterKey, &example.Pod{ObjectMeta: metav1.ObjectMeta{Name: "counter", Namespace: "ns"}}, nil, 0); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	revs := map[string]string{}
	record := func(rv, what string) {
		mu.Lock()
		defer mu.Unlock()
		if prev, ok := revs[rv]; ok {
			t.Errorf("resource version %s returned for both %s and %s", rv, prev, what)
		}
		revs[rv] = what
	}

	var wg sync.WaitGroup
	for u := range updaters {
		wg.Go(func() {
			for i := range updatesEach {
				out := &example.Pod{}
				err := s.GuaranteedUpdate(ctx, counterKey, out, false, nil,
					func(obj runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
						pod := obj.(*example.Pod)
						n, _ := strconv.Atoi(pod.Annotations["n"])
						pod.Annotations = map[string]string{"n": strconv.Itoa(n + 1)}
						return pod, nil, nil
					}, nil)
				if err != nil {
					t.Errorf("update: %v", err)
					return
				}
				record(out.ResourceVersion, fmt.Sprintf("update %d/%d", u, i))
			}
		})
	}
	for c := range creators {
		wg.Go(func() {
			name := fmt.Sprintf("created-%d", c)
			out := &example.Pod{}
			if err := s.Create(ctx, "/pods/ns/"+name, &example.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"}}, out, 0); err != nil {
				t.Errorf("create: %v", err)
				return
			}
			record(out.ResourceVersion, "create "+name)
		})
	}
	wg.Wait()

	final := &example.Pod{}
	if err := s.Get(ctx, counterKey, storage.GetOptions{}, final); err != nil {
		t.Fatal(err)
	}
	if got, want := final.Annotations["n"], strconv.Itoa(updaters*updatesEach); got != want {
		t.Errorf("counter = %s, want %s: an update was lost", got, want)
	}
	if got, want := len(revs), updaters*updatesEach+creators; got != want {
		t.Errorf("recorded %d distinct resource versions, want %d", got, want)
	}
}

// TestExactListMatchesReplay writes a random mix of creates, updates and
// deletes from several goroutines, then checks that an exact list at every
// resource version a write returned equals the state the writes produce when
// replayed in resource-version order.
func TestExactListMatchesReplay(t *testing.T) {
	ctx, s := testSetup(t)
	type write struct {
		rv      uint64
		name    string
		deleted bool
		value   string
	}
	var mu sync.Mutex
	var writes []write
	keys := []string{"a", "b", "c", "d", "e", "f"}

	var wg sync.WaitGroup
	for w := range 12 {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(w), 0))
			for i := range 60 {
				name := keys[rng.IntN(len(keys))]
				key := "/pods/ns/" + name
				value := fmt.Sprintf("w%d-%d", w, i)
				out := &example.Pod{}
				var err error
				deleted := false
				switch rng.IntN(3) {
				case 0:
					err = s.Create(ctx, key, &example.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns",
						Annotations: map[string]string{"v": value}}}, out, 0)
				case 1:
					err = s.GuaranteedUpdate(ctx, key, out, false, nil,
						func(obj runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
							pod := obj.(*example.Pod)
							pod.Annotations = map[string]string{"v": value}
							return pod, nil, nil
						}, nil)
				default:
					deleted = true
					err = s.Delete(ctx, key, out, nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{})
				}
				if storage.IsExist(err) || storage.IsNotFound(err) {
					continue
				}
				if err != nil {
					t.Errorf("write: %v", err)
					return
				}
				rv, err := s.versioner.ParseResourceVersion(out.ResourceVersion)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				writes = append(writes, write{rv: rv, name: name, deleted: deleted, value: out.Annotations["v"]})
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	slices.SortFunc(writes, func(a, b write) int { return cmp.Compare(a.rv, b.rv) })

	state := map[string]string{}
	for _, w := range writes {
		if w.deleted {
			delete(state, w.name)
		} else {
			state[w.name] = fmt.Sprintf("%s@%d", w.value, w.rv)
		}
		list := &example.PodList{}
		err := s.GetList(ctx, "/pods/", storage.ListOptions{
			ResourceVersion:      strconv.FormatUint(w.rv, 10),
			ResourceVersionMatch: metav1.ResourceVersionMatchExact,
			Predicate:            storage.Everything,
			Recursive:            true,
		}, list)
		if err != nil {
			t.Fatalf("exact list at %d: %v", w.rv, err)
		}
		got := map[string]string{}
		for _, p := range list.Items {
			got[p.Name] = fmt.Sprintf("%s@%s", p.Annotations["v"], p.ResourceVersion)
		}
		if !maps.Equal(got, state) {
			t.Fatalf("exact list at %d = %v, replay = %v", w.rv, got, state)
		}
	}
	if len(writes) < 100 {
		t.Fatalf("only %d writes succeeded; the test needs more to mean anything", len(writes))
	}
}
