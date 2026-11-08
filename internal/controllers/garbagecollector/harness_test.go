package garbagecollector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/meta/testrestmapper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	restclient "k8s.io/client-go/rest"
	"k8s.io/controller-manager/pkg/informerfactory"
	"k8s.io/klog/v2/ktesting"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
)

// The harness follows Kubernetes' own garbage collector unit tests at the version Milo pins (k8s.io/kubernetes
// v1.37.1, pkg/controller/garbagecollector/garbagecollector_test.go: TestGarbageCollectorSync's fake discovery, its
// test API server and assertMonitors), extended with project builders.

var (
	widgets = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}
	gizmos  = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "gizmos"}
	sprites = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "sprites"}
)

// testResources returns discovery's answer for the given example.com resources, each deletable.
func testResources(resources ...schema.GroupVersionResource) []*metav1.APIResourceList {
	list := &metav1.APIResourceList{GroupVersion: "example.com/v1"}
	for _, r := range resources {
		list.APIResources = append(list.APIResources, metav1.APIResource{
			Name: r.Resource, Namespaced: true, Kind: kindOf(r),
			Verbs: metav1.Verbs{"delete", "list", "watch"},
		})
	}
	return []*metav1.APIResourceList{list}
}

// kindOf names a test resource's kind: "widgets" serves "Widget".
func kindOf(r schema.GroupVersionResource) string {
	singular := strings.TrimSuffix(r.Resource, "s")
	return strings.ToUpper(singular[:1]) + singular[1:]
}

type testRESTMapper struct {
	meta.RESTMapper
}

func (m *testRESTMapper) Reset() {
	meta.MaybeResetRESTMapper(m.RESTMapper)
}

func newTestRESTMapper() *testRESTMapper {
	rm := meta.NewDefaultRESTMapper(nil)
	for _, r := range []schema.GroupVersionResource{widgets, gizmos, sprites} {
		rm.AddSpecific(r.GroupVersion().WithKind(kindOf(r)), r, r.GroupVersion().WithResource(strings.ToLower(kindOf(r))),
			meta.RESTScopeNamespace)
	}
	return &testRESTMapper{meta.MultiRESTMapper{rm, testrestmapper.TestOnlyStaticRESTMapper(legacyscheme.Scheme)}}
}

// fakeServerResources is upstream's fake discovery, unchanged in behaviour.
type fakeServerResources struct {
	lock               sync.Mutex
	preferredResources []*metav1.APIResourceList
	err                error
	interfaceUsedCount int
}

func (*fakeServerResources) ServerResourcesForGroupVersion(string) (*metav1.APIResourceList, error) {
	return nil, nil
}

func (*fakeServerResources) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, nil, nil
}

func (f *fakeServerResources) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.interfaceUsedCount++
	return f.preferredResources, f.err
}

func (*fakeServerResources) ServerPreferredNamespacedResources() ([]*metav1.APIResourceList, error) {
	return nil, nil
}

func (f *fakeServerResources) setPreferredResources(resources []*metav1.APIResourceList, err error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.preferredResources, f.err = resources, err
}

func (f *fakeServerResources) usedCount() int {
	f.lock.Lock()
	defer f.lock.Unlock()
	return f.interfaceUsedCount
}

const initialEventsEnd = `{"type": "BOOKMARK", "object": {"apiVersion": "meta.k8s.io/v1", "kind": "PartialObjectMetadata", ` +
	`"metadata": {"resourceVersion": "1", "annotations": {"k8s.io/initial-events-end": "true"}}}}` + "\n"

// newListServer answers every LIST with an empty list and holds every WATCH open until the test ends, as upstream's
// fakeActionHandler does. Its answers are the metadata client's kinds, so a monitor syncs.
func newListServer(t *testing.T) *restclient.Config {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.RawQuery, "watch=true") {
			w.WriteHeader(http.StatusOK)
			if strings.Contains(r.URL.RawQuery, "sendInitialEvents=true") {
				// client-go's watch-list waits for this bookmark to end the initial events, as an API server sends it.
				_, _ = w.Write([]byte(initialEventsEnd))
			}
			w.(http.Flusher).Flush()
			select {
			case <-done:
			case <-r.Context().Done():
			}
			return
		}
		_, _ = w.Write([]byte(`{"apiVersion": "meta.k8s.io/v1", "kind": "PartialObjectMetadataList", "metadata": {"resourceVersion": "1"}}`))
	}))
	t.Cleanup(func() {
		close(done)
		srv.Close()
	})
	return &restclient.Config{Host: srv.URL}
}

// testGC is a garbage collector against the list server, with its root discovery under the test's control.
type testGC struct {
	*GarbageCollector
	ctx       context.Context
	cfg       *restclient.Config
	mapper    *testRESTMapper
	discovery *fakeServerResources
}

func newTestGC(t *testing.T, root ...schema.GroupVersionResource) *testGC {
	_, logCtx := ktesting.NewTestContext(t)
	tCtx, cancel := context.WithCancel(logCtx)
	t.Cleanup(cancel)
	cfg := newListServer(t)
	md, err := metadata.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := fake.NewSimpleClientset()
	started := make(chan struct{})
	close(started)
	mapper := newTestRESTMapper()
	gc, err := NewGarbageCollector(tCtx, client, md, mapper, map[schema.GroupResource]struct{}{},
		informerfactory.NewInformerFactory(informers.NewSharedInformerFactory(client, 0),
			metadatainformer.NewSharedInformerFactory(md, 0)), started)
	if err != nil {
		t.Fatal(err)
	}
	return &testGC{GarbageCollector: gc, ctx: tCtx, cfg: cfg, mapper: mapper,
		discovery: &fakeServerResources{preferredResources: testResources(root...)}}
}

// addProject engages a project whose own discovery answers with the given resources, as GCSink does.
func (g *testGC) addProject(t *testing.T, project string, own ...schema.GroupVersionResource) {
	t.Helper()
	md, err := metadata.NewForConfig(g.cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := fake.NewSimpleClientset()
	started := make(chan struct{})
	close(started)
	shared := informerfactory.NewInformerFactory(informers.NewSharedInformerFactory(client, 0),
		metadatainformer.NewSharedInformerFactory(md, 0))
	err = g.AddProject(g.ctx, project, md, g.mapper, map[schema.GroupResource]struct{}{}, shared, started,
		&fakeServerResources{preferredResources: testResources(own...)}, wait.ForeverTestTimeout)
	if err != nil {
		t.Fatal(err)
	}
}

// monitored returns the resources a builder watches, sorted.
func monitored(gb *GraphBuilder) []string {
	gb.monitorLock.RLock()
	defer gb.monitorLock.RUnlock()
	var out []string
	for r := range gb.monitors {
		out = append(out, r.Resource)
	}
	sort.Strings(out)
	return out
}

// eventuallyMonitors waits for a builder to watch exactly the given resources.
func eventuallyMonitors(t *testing.T, gb *GraphBuilder, want ...string) {
	t.Helper()
	sort.Strings(want)
	var got []string
	err := wait.PollUntilContextTimeout(context.Background(), 20*time.Millisecond, wait.ForeverTestTimeout, true,
		func(context.Context) (bool, error) {
			got = monitored(gb)
			return strings.Join(got, ",") == strings.Join(want, ","), nil
		})
	if err != nil {
		t.Fatalf("builder %q: want monitors %v, got %v", gb.project, want, got)
	}
}
