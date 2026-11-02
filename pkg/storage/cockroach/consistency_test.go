package cockroach

import (
	"fmt"
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

// A project's first writes race to create its counter row, from two replicas.
// They still get gapless revisions of their own.
func TestConcurrentFirstWrites(t *testing.T) {
	_, base := testSetup(t)
	replicas := []testStore{otherReplica(t, base), otherReplica(t, base)}
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
