package projectstorage

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"go.miloapis.com/milo/internal/apiserver/storage/etcdshared"
)

// coldWindow is how long the test holds etcd while a child's cache starts:
// long enough for many readiness polls, short of the client's keepalive.
const coldWindow = time.Second

// The premise of the readiness check: a child's ReadinessCheck passes only
// when its cache serves. While etcd cannot answer the cache's first list, the
// check fails and a watch is refused with 429. Once the check passes, a watch
// from a create's version is served and delivers the next change, as a claim
// wait needs. Real storage: Milo's shared-client cacher over envtest's etcd.
func TestAChildsCheckPassesOnlyWhenItsCacheServes(t *testing.T) {
	env := &envtest.Environment{}
	if _, err := env.Start(); err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	etcd := forwardEtcd(t, env.ControlPlane.Etcd.URL.Host)
	mux := configMapMux(t, "http://"+etcd.addr)
	etcd.hold()
	child, err := mux.childForProject("p1")
	if err != nil {
		t.Fatalf("build project p1's child: %v", err)
	}
	assertColdWhileHeld(t, child)
	etcd.release()

	waitReady(t, child)
	assertWatchDeliversTheNextChange(t, child)
}

// assertColdWhileHeld checks, for the whole cold window, that the child does
// not report ready and that a watch on it is refused with 429.
func assertColdWhileHeld(t *testing.T, child storage.Interface) {
	t.Helper()
	polls := 0
	for end := time.Now().Add(coldWindow); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if child.ReadinessCheck() == nil {
			t.Fatalf("the child reported ready after %d polls while etcd could not answer its list", polls)
		}
		polls++
	}
	w, err := child.Watch(context.Background(), "/configmaps/default/c1",
		storage.ListOptions{Predicate: storage.Everything})
	if err == nil {
		w.Stop()
		t.Fatal("a watch on a child whose cache cannot list was served")
	}
	if !apierrors.IsTooManyRequests(err) {
		t.Fatalf("want a watch on a cold child refused with 429, got %v", err)
	}
}

// assertWatchDeliversTheNextChange creates an object, watches it from the
// create's version, updates it, and checks the watch delivers the update.
func assertWatchDeliversTheNextChange(t *testing.T, child storage.Interface) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	created := &corev1.ConfigMap{}
	in := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "default"}}
	if err := child.Create(ctx, "/configmaps/default/c1", in, created, 0); err != nil {
		t.Fatalf("create: %v", err)
	}
	w, err := child.Watch(ctx, "/configmaps/default/c1",
		storage.ListOptions{ResourceVersion: created.ResourceVersion, Predicate: storage.Everything})
	if err != nil {
		t.Fatalf("a watch on a child that reports ready was refused: %v", err)
	}
	defer w.Stop()
	update(ctx, t, child)
	select {
	case ev := <-w.ResultChan():
		if ev.Type != watch.Modified {
			t.Fatalf("want the update as Modified, got %s %v", ev.Type, ev.Object)
		}
	case <-ctx.Done():
		t.Fatal("the watch delivered nothing after the update")
	}
}

// configMapMux builds a ConfigMap storage through the project decorator over
// Milo's shared-client cacher, as the server builds a resource's storage.
func configMapMux(t *testing.T, etcdURL string) *projectMux {
	t.Helper()
	cfg := storagebackend.NewDefaultConfig("", configMapCodec(t))
	cfg.Transport.ServerList = []string{etcdURL}
	gr := schema.GroupResource{Resource: "configmaps"}
	keyFunc := func(obj runtime.Object) (string, error) { return storage.NamespaceKeyFunc("/configmaps/", obj) }
	s, destroy, err := ProjectAwareDecorator(gr, etcdshared.StorageWithSharedCacher(), everyProjectKnown(), nil, testDrain)(
		cfg.ForResource(gr), "/configmaps", keyFunc,
		func() runtime.Object { return &corev1.ConfigMap{} }, func() runtime.Object { return &corev1.ConfigMapList{} },
		storage.DefaultNamespaceScopedAttr, nil, &cache.Indexers{})
	if err != nil {
		t.Fatalf("build the storage: %v", err)
	}
	t.Cleanup(destroy)
	return s.(*projectMux)
}

// configMapCodec decodes to v1, not to the internal version LegacyCodec
// targets: this scheme registers no internal types, so every decode in a
// watch would fail.
func configMapCodec(t *testing.T) runtime.Codec {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	codecs := serializer.NewCodecFactory(scheme)
	json, _ := runtime.SerializerInfoForMediaType(codecs.SupportedMediaTypes(), runtime.ContentTypeJSON)
	return codecs.CodecForVersions(json.Serializer, codecs.UniversalDeserializer(),
		corev1.SchemeGroupVersion, corev1.SchemeGroupVersion)
}

func waitReady(t *testing.T, child storage.Interface) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); child.ReadinessCheck() != nil; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the child did not report ready in 30 s: %v", child.ReadinessCheck())
		}
	}
}

func update(ctx context.Context, t *testing.T, child storage.Interface) {
	t.Helper()
	out := &corev1.ConfigMap{}
	err := child.GuaranteedUpdate(ctx, "/configmaps/default/c1", out, false, nil,
		func(in runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
			cm := in.(*corev1.ConfigMap).DeepCopy()
			cm.Data = map[string]string{"granted": "true"}
			return cm, nil, nil
		}, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
}

// heldEtcd forwards TCP connections to etcd and can hold every byte in both
// directions: etcd made slow at will, so a cache that starts while it holds
// cannot finish its first list.
type heldEtcd struct {
	addr string
	mu   sync.Mutex
	held chan struct{} // non-nil while held; closed on release
}

func forwardEtcd(t *testing.T, target string) *heldEtcd {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &heldEtcd{addr: l.Addr().String()}
	t.Cleanup(func() { h.release(); _ = l.Close() })
	go func() {
		for {
			in, err := l.Accept()
			if err != nil {
				return
			}
			go h.pipe(in, target)
		}
	}()
	return h
}

func (h *heldEtcd) pipe(in net.Conn, target string) {
	out, err := net.Dial("tcp", target)
	if err != nil {
		_ = in.Close()
		return
	}
	go func() { _, _ = io.Copy(heldWriter{out, h}, in); _ = out.Close() }()
	_, _ = io.Copy(heldWriter{in, h}, out)
	_ = in.Close()
}

func (h *heldEtcd) hold() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.held = make(chan struct{})
}

func (h *heldEtcd) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held != nil {
		close(h.held)
		h.held = nil
	}
}

func (h *heldEtcd) wait() {
	h.mu.Lock()
	held := h.held
	h.mu.Unlock()
	if held != nil {
		<-held
	}
}

type heldWriter struct {
	w io.Writer
	h *heldEtcd
}

func (hw heldWriter) Write(p []byte) (int, error) {
	hw.h.wait()
	return hw.w.Write(p)
}
