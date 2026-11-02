package cockroach

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/apis/example"
	"k8s.io/apiserver/pkg/storage"
)

// markedPod is a pod whose project annotation says which test project wrote
// it, so an object that crossed into the other project shows.
func markedPod(project, name string) *example.Pod {
	pod := newPod(name)
	pod.Annotations = map[string]string{"project": project}
	return pod
}

func createMarked(t *testing.T, s testStore, project, name string) *example.Pod {
	t.Helper()
	out := &example.Pod{}
	require.NoError(t, s.Create(t.Context(), "/pods/test-ns/"+name, markedPod(project, name), out, 0))
	return out
}

func requireMarked(t *testing.T, project string, pods ...example.Pod) {
	t.Helper()
	for _, pod := range pods {
		require.Equal(t, project, pod.Annotations["project"], "%s came from another project", pod.Name)
	}
}

// Two projects that hold the same keys share one backend, and neither ever
// sees the other's objects, revisions or events: not through reads, lists,
// continue tokens or watches, nor when their writes commit at the same time.
func TestProjectsAreIsolated(t *testing.T) {
	ctx, a := testSetup(t)
	b := newTestStore(t, a.b, testConfig(a.project+"-other"))
	projects := map[string]testStore{"a": a, "b": b}

	starts := map[string]int64{}
	watches := map[string]watch.Interface{}
	for name, s := range projects {
		start := createMarked(t, s, name, "start")
		starts[name] = parseRV(t, start.ResourceVersion)
		watches[name] = watchFrom(ctx, t, s, start.ResourceVersion)
	}

	// The same names, written into both projects at once.
	const pods = 10
	errs := make(chan error, 2*pods)
	for name, s := range projects {
		for i := range pods {
			go func() {
				pod := markedPod(name, fmt.Sprintf("pod-%d", i))
				errs <- s.Create(ctx, "/pods/test-ns/"+pod.Name, pod, &example.Pod{}, 0)
			}()
		}
	}
	for range 2 * pods {
		require.NoError(t, <-errs)
	}

	// Only a's pod-0 is deleted.
	require.NoError(t, a.Delete(ctx, "/pods/test-ns/pod-0", &example.Pod{}, nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{}))

	for name, s := range projects {
		t.Run(name, func(t *testing.T) {
			// Each project's revisions move by its own writes and nothing else.
			writes, live := int64(pods), 1+pods
			if name == "a" {
				writes, live = writes+1, live-1
			}
			current, err := s.GetCurrentResourceVersion(ctx)
			require.NoError(t, err)
			require.Equal(t, starts[name]+writes, int64(current))

			got := &example.Pod{}
			err = s.Get(ctx, "/pods/test-ns/pod-0", storage.GetOptions{}, got)
			if name == "a" {
				require.True(t, storage.IsNotFound(err), "%v", err)
			} else {
				require.NoError(t, err)
				requireMarked(t, name, *got)
			}

			all := &example.PodList{}
			require.NoError(t, s.GetList(ctx, "/pods/", storage.ListOptions{Predicate: storage.Everything, Recursive: true}, all))
			require.Len(t, all.Items, live)
			requireMarked(t, name, all.Items...)
			require.Equal(t, strconv.FormatUint(current, 10), all.ResourceVersion)

			// An exact list, paged, reads this project's rows at its own
			// revision.
			page := withLimit(storage.Everything, 2)
			first := &example.PodList{}
			require.NoError(t, s.GetList(ctx, "/pods/", storage.ListOptions{
				ResourceVersion: all.ResourceVersion, ResourceVersionMatch: metav1.ResourceVersionMatchExact,
				Predicate: page, Recursive: true,
			}, first))
			requireMarked(t, name, first.Items...)
			require.NotEmpty(t, first.Continue)
			page.Continue = first.Continue
			next := &example.PodList{}
			require.NoError(t, s.GetList(ctx, "/pods/", storage.ListOptions{Predicate: page, Recursive: true}, next))
			require.Equal(t, all.ResourceVersion, next.ResourceVersion)
			require.Equal(t, all.Items[2:4], next.Items)

			// The watch sees exactly this project's writes, in revision order,
			// the delete included, then this project's sentinel.
			w := watches[name]
			for i := range writes {
				e := nextEvent(t, w)
				pod := e.Object.(*example.Pod)
				requireMarked(t, name, *pod)
				require.Equal(t, starts[name]+i+1, parseRV(t, pod.ResourceVersion), "event %d", i)
			}
			sentinel := createMarked(t, s, name, "sentinel")
			e := nextEvent(t, w)
			require.Equal(t, watch.Added, e.Type)
			require.Equal(t, sentinel.ResourceVersion, e.Object.(*example.Pod).ResourceVersion)
			requireMarked(t, name, *e.Object.(*example.Pod))
		})
	}

	// A continue token taken from a carries a's revision. While b is behind
	// that revision, b refuses the token, and once b has reached it, b serves
	// only its own rows.
	page := withLimit(storage.Everything, 2)
	fromA := &example.PodList{}
	require.NoError(t, a.GetList(ctx, "/pods/", storage.ListOptions{Predicate: page, Recursive: true}, fromA))
	tokenRev := parseRV(t, fromA.ResourceVersion)
	bRev, err := b.GetCurrentResourceVersion(ctx)
	require.NoError(t, err)
	require.Less(t, int64(bRev), tokenRev, "b should start behind a")
	page.Continue = fromA.Continue
	err = b.GetList(ctx, "/pods/", storage.ListOptions{Predicate: page, Recursive: true}, &example.PodList{})
	require.True(t, storage.IsTooLargeResourceVersion(err), "%v", err)

	for i := 0; int64(bRev) < tokenRev; i++ {
		createMarked(t, b, "b", fmt.Sprintf("catch-up-%d", i))
		bRev, err = b.GetCurrentResourceVersion(ctx)
		require.NoError(t, err)
	}
	onB := &example.PodList{}
	require.NoError(t, b.GetList(ctx, "/pods/", storage.ListOptions{Predicate: page, Recursive: true}, onB))
	require.NotEmpty(t, onB.Items)
	requireMarked(t, "b", onB.Items...)
}
