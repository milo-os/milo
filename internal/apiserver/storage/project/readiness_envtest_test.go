package projectstorage

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
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

// The premise of the readiness check (storage/DESIGN.md §3, V40's "what would
// make it wrong"): when a child built outside a request reports ready through
// its own ReadinessCheck, a watch from a create's version is served, not
// refused with 429, and it delivers the next change, as a claim wait needs.
// Real storage: Milo's shared-client cacher over envtest's etcd.
func TestAChildThatReportsReadyServesAWatchFromACreatesVersion(t *testing.T) {
	env := &envtest.Environment{}
	if _, err := env.Start(); err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	child := configMapChild(t, env.ControlPlane.Etcd.URL.String(), "p1")
	waitReady(t, child)

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

// configMapChild builds a ConfigMap storage through the project decorator over
// Milo's shared-client cacher, and the named project's child in-process, as
// the readiness check builds it.
func configMapChild(t *testing.T, etcdURL, project string) storage.Interface {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cfg := storagebackend.NewDefaultConfig("", serializer.NewCodecFactory(scheme).LegacyCodec(corev1.SchemeGroupVersion))
	cfg.Transport.ServerList = []string{etcdURL}
	gr := schema.GroupResource{Resource: "configmaps"}
	keyFunc := func(obj runtime.Object) (string, error) { return storage.NamespaceKeyFunc("/configmaps", obj) }
	s, destroy, err := ProjectAwareDecorator(gr, etcdshared.StorageWithSharedCacher(), everyProjectKnown(), nil, testDrain)(
		cfg.ForResource(gr), "/configmaps", keyFunc,
		func() runtime.Object { return &corev1.ConfigMap{} }, func() runtime.Object { return &corev1.ConfigMapList{} },
		storage.DefaultNamespaceScopedAttr, nil, &cache.Indexers{})
	if err != nil {
		t.Fatalf("build the storage: %v", err)
	}
	t.Cleanup(destroy)
	child, err := s.(*projectMux).childForProject(project)
	if err != nil {
		t.Fatalf("build project %s's child: %v", project, err)
	}
	return child
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
