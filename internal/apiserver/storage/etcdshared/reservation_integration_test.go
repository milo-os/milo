package etcdshared

import (
	"context"
	cryptoaes "crypto/aes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/value/encrypt/aes"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestProjectReservationRegisteredStorageAgainstEtcd(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real etcd process")
	}
	etcd := &envtest.Etcd{Path: findFenceTxnEtcd(t), StartTimeout: 20 * time.Second, StopTimeout: 10 * time.Second}
	require.NoError(t, etcd.Start())
	t.Cleanup(func() { require.NoError(t, etcd.Stop()) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	config := storagebackend.NewDefaultConfig("/reservation-encrypted", unstructured.UnstructuredJSONScheme)
	config.Transport.ServerList = []string{etcd.URL.String()}
	block, err := cryptoaes.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	config.Transformer, err = aes.NewGCMTransformer(block)
	require.NoError(t, err)
	c := storagebackend.ConfigForResource{Config: *config, GroupResource: rootProjectResource}
	m := NewCommitFence()
	cap := m.ProjectReservations()
	key := func(obj runtime.Object) (string, error) {
		return "/projects/" + obj.(*unstructured.Unstructured).GetName(), nil
	}
	newObj := func() runtime.Object { return &unstructured.Unstructured{} }
	newList := func() runtime.Object { return &unstructured.UnstructuredList{} }
	// A child alone cannot initialize the capability, and its retirement
	// does not prevent subsequent genuine root registration.
	childOnly := c
	childOnly.Prefix = "/child-before-root"
	_, destroyFirstChild, err := newRawStorage(childOnly, newObj, newList, "/projects", key, FenceOptions{Manager: m, Project: "tenant"})
	require.NoError(t, err)
	_, err = cap.ReadHeld(ctx, "probe", "project-uid", reservationTestUID)
	require.ErrorIs(t, err, ErrReservationUnavailable)
	destroyFirstChild()
	root, destroyRoot, err := newRawStorage(c, newObj, newList, "/projects", key, FenceOptions{Manager: m, Root: true})
	require.NoError(t, err)
	t.Cleanup(destroyRoot)
	original := reservationProject()
	original.SetResourceVersion("")
	created := &unstructured.Unstructured{}
	require.NoError(t, root.Create(ctx, "/projects/probe", original, created, 0))
	held, err := cap.Acquire(ctx, "probe", "project-uid", created.GetResourceVersion(), reservationTestUID)
	require.NoError(t, err)
	recovered, err := cap.ReadHeld(ctx, "probe", "project-uid", reservationTestUID)
	require.NoError(t, err)
	require.Equal(t, held, recovered)
	kv, err := clientv3.New(clientv3.Config{Endpoints: []string{etcd.URL.String()}, DialTimeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { kv.Close() })
	encrypted, err := kv.Get(ctx, "/reservation-encrypted/projects/probe")
	require.NoError(t, err)
	require.Len(t, encrypted.Kvs, 1)
	require.NotContains(t, string(encrypted.Kvs[0].Value), projectReservationAnnotation)
	require.NotContains(t, string(encrypted.Kvs[0].Value), reservationTestUID)

	// A project-scoped store of the same GVR must neither supply nor retire the
	// root capability. Its different prefix cannot redirect the acquisition key.
	childConfig := c
	childConfig.Prefix = "/tenant-child"
	child, destroyChild, err := newRawStorage(childConfig, newObj, newList, "/projects", key, FenceOptions{Manager: m, Project: "tenant"})
	require.NoError(t, err)
	var childObject unstructured.Unstructured
	require.Error(t, child.Get(ctx, "/projects/probe", storage.GetOptions{}, &childObject))
	destroyChild()
	recovered, err = cap.ReadHeld(ctx, "probe", "project-uid", reservationTestUID)
	require.NoError(t, err)
	require.Equal(t, held, recovered)

	// Equivalent root registrations are legitimate during CRD store recreation.
	// Retiring either disables the capability, but ordinary storage survives.
	replacement, destroyReplacement, err := newRawStorage(c, newObj, newList, "/projects", key, FenceOptions{Manager: m, Root: true})
	require.NoError(t, err)
	t.Cleanup(destroyReplacement)
	destroyRoot()
	_, err = cap.ReadHeld(ctx, "probe", "project-uid", reservationTestUID)
	require.ErrorIs(t, err, ErrReservationUnavailable)
	var stored unstructured.Unstructured
	require.NoError(t, replacement.Get(ctx, "/projects/probe", storage.GetOptions{}, &stored))
	later, destroyLater, err := newRawStorage(c, newObj, newList, "/projects", key, FenceOptions{Manager: m, Root: true})
	require.NoError(t, err)
	t.Cleanup(destroyLater)
	require.NoError(t, later.Get(ctx, "/projects/probe", storage.GetOptions{}, &stored))
	ordinary := reservationProject()
	ordinary.SetName("ordinary-after-retirement")
	ordinary.SetUID("ordinary-uid")
	ordinary.SetResourceVersion("")
	require.NoError(t, later.Create(ctx, "/projects/ordinary-after-retirement", ordinary, &unstructured.Unstructured{}, 0))
	_, err = cap.ReadHeld(ctx, "probe", "project-uid", reservationTestUID)
	require.ErrorIs(t, err, ErrReservationUnavailable)

	// A different root descriptor permanently invalidates old handles as well.
	// Use another manager to avoid the already-retired state hiding this check.
	otherManager := NewCommitFence()
	_, destroyOther, err := newRawStorage(c, newObj, newList, "/projects", key, FenceOptions{Manager: otherManager, Root: true})
	require.NoError(t, err)
	t.Cleanup(destroyOther)
	otherCap := otherManager.ProjectReservations()
	_, err = otherCap.ReadHeld(ctx, "probe", "project-uid", reservationTestUID)
	require.NoError(t, err)
	conflicting := c
	conflicting.Prefix = "/conflicting-root"
	_, _, err = newRawStorage(conflicting, newObj, newList, "/projects", key, FenceOptions{Manager: otherManager, Root: true})
	require.Error(t, err)
	_, err = otherCap.ReadHeld(ctx, "probe", "project-uid", reservationTestUID)
	require.ErrorIs(t, err, ErrReservationUnavailable)
}
