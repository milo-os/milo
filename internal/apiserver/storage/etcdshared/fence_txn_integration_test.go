package etcdshared

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/kubernetes"
	"go.miloapis.com/milo/internal/apiserver/projectfence"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	txnTestProjectKey = "/registry/projects/fence-project"
	txnTestTargetKey  = "/projects/fence-project/things/default/one"
)

func TestFencedClientOptimisticTransactionsAgainstEtcd(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real etcd process")
	}

	t.Run("valid root and target revisions commit atomically", func(t *testing.T) {
		client, manager, fenced := startFenceTxnFixture(t)
		ctx, rootRevision, targetRevision := seedFenceTxnObjects(t, client, manager)
		ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)

		response, err := fenced.OptimisticPut(ctx, txnTestTargetKey, []byte("updated"), targetRevision, kubernetes.PutOptions{})
		require.NoError(t, err)
		require.True(t, response.Succeeded)
		verifyFenceTxnValue(t, client, txnTestTargetKey, "updated")
		require.Equal(t, rootRevision, readFenceTxnModRevision(t, client, txnTestProjectKey))
	})

	t.Run("changed root revision conflicts without changing target", func(t *testing.T) {
		client, manager, fenced := startFenceTxnFixture(t)
		ctx, _, targetRevision := seedFenceTxnObjects(t, client, manager)
		ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)
		before := readFenceTxnValue(t, client, txnTestTargetKey)
		_, err := client.Put(context.Background(), txnTestProjectKey, "root-changed")
		require.NoError(t, err)

		response, err := fenced.OptimisticPut(ctx, txnTestTargetKey, []byte("must-not-write"), targetRevision, kubernetes.PutOptions{})
		require.True(t, apierrors.IsConflict(err), "expected terminal root conflict, got response=%+v err=%v", response, err)
		verifyFenceTxnValue(t, client, txnTestTargetKey, before)
	})

	t.Run("missing root conflicts without changing target", func(t *testing.T) {
		client, manager, fenced := startFenceTxnFixture(t)
		ctx, _, targetRevision := seedFenceTxnObjects(t, client, manager)
		ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)
		before := readFenceTxnValue(t, client, txnTestTargetKey)
		_, err := client.Delete(context.Background(), txnTestProjectKey)
		require.NoError(t, err)

		response, err := fenced.OptimisticPut(ctx, txnTestTargetKey, []byte("must-not-write"), targetRevision, kubernetes.PutOptions{})
		require.True(t, apierrors.IsConflict(err), "expected terminal missing-root conflict, got response=%+v err=%v", response, err)
		verifyFenceTxnValue(t, client, txnTestTargetKey, before)
	})

	t.Run("same-name root delete and recreate conflicts without changing target", func(t *testing.T) {
		client, manager, fenced := startFenceTxnFixture(t)
		ctx, rootRevision, targetRevision := seedFenceTxnObjects(t, client, manager)
		ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)
		before := readFenceTxnValue(t, client, txnTestTargetKey)
		_, err := client.Delete(context.Background(), txnTestProjectKey)
		require.NoError(t, err)
		recreated, err := client.Put(context.Background(), txnTestProjectKey, "root-recreated-with-same-name")
		require.NoError(t, err)
		require.Greater(t, recreated.Header.Revision, rootRevision)

		response, err := fenced.OptimisticPut(ctx, txnTestTargetKey, []byte("must-not-write"), targetRevision, kubernetes.PutOptions{})
		require.True(t, apierrors.IsConflict(err), "expected same-name recreation conflict, got response=%+v err=%v", response, err)
		verifyFenceTxnValue(t, client, txnTestTargetKey, before)
	})

	t.Run("target-only CAS conflict returns current KV and a fresh retry succeeds", func(t *testing.T) {
		client, manager, fenced := startFenceTxnFixture(t)
		ctx, _, staleRevision := seedFenceTxnObjects(t, client, manager)
		ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)
		_, err := client.Put(context.Background(), txnTestTargetKey, "concurrent-target-update")
		require.NoError(t, err)

		stale, err := fenced.OptimisticPut(ctx, txnTestTargetKey, []byte("stale"), staleRevision, kubernetes.PutOptions{GetOnFailure: true})
		require.NoError(t, err, "a target-only CAS miss is retryable when the root is unchanged")
		require.False(t, stale.Succeeded)
		require.NotNil(t, stale.KV)
		require.Equal(t, []byte("concurrent-target-update"), stale.KV.Value)

		fresh, err := fenced.OptimisticPut(ctx, txnTestTargetKey, []byte("fresh-retry"), stale.KV.ModRevision, kubernetes.PutOptions{})
		require.NoError(t, err)
		require.True(t, fresh.Succeeded)
		verifyFenceTxnValue(t, client, txnTestTargetKey, "fresh-retry")
	})

	t.Run("a proof bound to another target is rejected before any write", func(t *testing.T) {
		client, manager, fenced := startFenceTxnFixture(t)
		ctx, _, _ := seedFenceTxnObjects(t, client, manager)
		ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)
		before := readFenceTxnValue(t, client, txnTestTargetKey)
		otherKey := "/projects/fence-project/things/default/two"
		_, err := client.Put(context.Background(), otherKey, "untouched")
		require.NoError(t, err)
		otherBefore := readFenceTxnValue(t, client, otherKey)

		response, err := fenced.OptimisticPut(ctx, otherKey, []byte("must-not-write"), 0, kubernetes.PutOptions{})
		require.True(t, apierrors.IsConflict(err), "expected wrong-target conflict, got response=%+v err=%v", response, err)
		verifyFenceTxnValue(t, client, txnTestTargetKey, before)
		verifyFenceTxnValue(t, client, otherKey, otherBefore)
	})

	t.Run("conflicting root registration and backend cluster mismatch fail closed", func(t *testing.T) {
		t.Run("conflicting registration", func(t *testing.T) {
			client, manager, fenced := startFenceTxnFixture(t)
			ctx, _, targetRevision := seedFenceTxnObjects(t, client, manager)
			ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)
			before := readFenceTxnValue(t, client, txnTestTargetKey)
			config := storagebackend.ConfigForResource{Config: storagebackend.Config{Prefix: "/different-root"}, GroupResource: rootProjectResource}
			keyFunc := func(obj runtime.Object) (string, error) {
				name := obj.(*unstructured.Unstructured).GetName()
				return "/projects/" + name, nil
			}
			err := manager.registerRoot(config, keyFunc, client.KV)
			require.Error(t, err)
			require.Contains(t, err.Error(), "conflicting root Project storage registration")

			response, err := fenced.OptimisticPut(ctx, txnTestTargetKey, []byte("must-not-write"), targetRevision, kubernetes.PutOptions{})
			require.True(t, apierrors.IsConflict(err), "expected invalid-registration conflict, got response=%+v err=%v", response, err)
			verifyFenceTxnValue(t, client, txnTestTargetKey, before)
		})

		t.Run("backend cluster mismatch", func(t *testing.T) {
			client, manager, fenced := startFenceTxnFixture(t)
			ctx, _, targetRevision := seedFenceTxnObjects(t, client, manager)
			ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)
			before := readFenceTxnValue(t, client, txnTestTargetKey)
			manager.mu.Lock()
			manager.root.cluster++
			manager.mu.Unlock()

			response, err := fenced.OptimisticPut(ctx, txnTestTargetKey, []byte("must-not-write"), targetRevision, kubernetes.PutOptions{})
			require.True(t, apierrors.IsConflict(err), "expected backend-cluster conflict, got response=%+v err=%v", response, err)
			verifyFenceTxnValue(t, client, txnTestTargetKey, before)
		})
	})

	t.Run("delete uses the same root and target comparisons", func(t *testing.T) {
		client, manager, fenced := startFenceTxnFixture(t)
		ctx, rootRevision, targetRevision := seedFenceTxnObjects(t, client, manager)
		ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)
		response, err := fenced.OptimisticDelete(ctx, txnTestTargetKey, targetRevision, kubernetes.DeleteOptions{})
		require.NoError(t, err)
		require.True(t, response.Succeeded)
		require.Equal(t, rootRevision, readFenceTxnModRevision(t, client, txnTestProjectKey))
		require.Empty(t, readFenceTxnValueIfPresent(t, client, txnTestTargetKey))
	})

	t.Run("delete root change conflicts without deleting target", func(t *testing.T) {
		client, manager, fenced := startFenceTxnFixture(t)
		ctx, _, targetRevision := seedFenceTxnObjects(t, client, manager)
		ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)
		before := readFenceTxnValue(t, client, txnTestTargetKey)
		_, err := client.Put(context.Background(), txnTestProjectKey, "root-changed")
		require.NoError(t, err)
		response, err := fenced.OptimisticDelete(ctx, txnTestTargetKey, targetRevision, kubernetes.DeleteOptions{})
		require.True(t, apierrors.IsConflict(err), "expected terminal root conflict, got response=%+v err=%v", response, err)
		verifyFenceTxnValue(t, client, txnTestTargetKey, before)
	})

	t.Run("delete with a missing root conflicts without deleting target", func(t *testing.T) {
		client, manager, fenced := startFenceTxnFixture(t)
		ctx, _, targetRevision := seedFenceTxnObjects(t, client, manager)
		ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)
		before := readFenceTxnValue(t, client, txnTestTargetKey)
		_, err := client.Delete(context.Background(), txnTestProjectKey)
		require.NoError(t, err)

		response, err := fenced.OptimisticDelete(ctx, txnTestTargetKey, targetRevision, kubernetes.DeleteOptions{})
		require.True(t, apierrors.IsConflict(err), "expected missing-root conflict, got response=%+v err=%v", response, err)
		verifyFenceTxnValue(t, client, txnTestTargetKey, before)
	})

	t.Run("delete with same-name root delete and recreate conflicts without deleting target", func(t *testing.T) {
		client, manager, fenced := startFenceTxnFixture(t)
		ctx, rootRevision, targetRevision := seedFenceTxnObjects(t, client, manager)
		ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)
		before := readFenceTxnValue(t, client, txnTestTargetKey)
		_, err := client.Delete(context.Background(), txnTestProjectKey)
		require.NoError(t, err)
		recreated, err := client.Put(context.Background(), txnTestProjectKey, "root-recreated-with-same-name")
		require.NoError(t, err)
		require.Greater(t, recreated.Header.Revision, rootRevision)

		response, err := fenced.OptimisticDelete(ctx, txnTestTargetKey, targetRevision, kubernetes.DeleteOptions{})
		require.True(t, apierrors.IsConflict(err), "expected same-name recreation conflict, got response=%+v err=%v", response, err)
		verifyFenceTxnValue(t, client, txnTestTargetKey, before)
	})

	t.Run("delete target-only CAS conflict returns current KV and fresh retry succeeds", func(t *testing.T) {
		client, manager, fenced := startFenceTxnFixture(t)
		ctx, _, staleRevision := seedFenceTxnObjects(t, client, manager)
		ctx = bindFenceTxnTarget(t, ctx, txnTestTargetKey)
		_, err := client.Put(context.Background(), txnTestTargetKey, "concurrent-target-update")
		require.NoError(t, err)

		stale, err := fenced.OptimisticDelete(ctx, txnTestTargetKey, staleRevision, kubernetes.DeleteOptions{GetOnFailure: true})
		require.NoError(t, err, "a target-only CAS miss is retryable when the root is unchanged")
		require.False(t, stale.Succeeded)
		require.NotNil(t, stale.KV)
		require.Equal(t, []byte("concurrent-target-update"), stale.KV.Value)

		fresh, err := fenced.OptimisticDelete(ctx, txnTestTargetKey, stale.KV.ModRevision, kubernetes.DeleteOptions{})
		require.NoError(t, err)
		require.True(t, fresh.Succeeded)
		require.Empty(t, readFenceTxnValueIfPresent(t, client, txnTestTargetKey))
	})
}

func startFenceTxnFixture(t *testing.T) (*clientv3.Client, *CommitFence, *fencedClient) {
	t.Helper()
	path := findFenceTxnEtcd(t)
	etcd := &envtest.Etcd{Path: path, StartTimeout: 20 * time.Second, StopTimeout: 10 * time.Second}
	require.NoError(t, etcd.Start())
	t.Cleanup(func() { require.NoError(t, etcd.Stop()) })
	parsed, err := url.Parse(etcd.URL.String())
	require.NoError(t, err)
	transport := transportKey(storagebackend.TransportConfig{ServerList: []string{etcd.URL.String()}})
	client, err := clientv3.New(clientv3.Config{Endpoints: []string{parsed.Host}, DialTimeout: 3 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	manager := NewCommitFence()
	status, err := client.Status(context.Background(), parsed.Host)
	require.NoError(t, err)
	manager.root = &rootProjectStore{
		transport: transport,
		cluster:   status.Header.ClusterId,
		prefix:    "/registry",
		probe:     "/projects/storage-fence-key-probe",
		key: func(obj runtime.Object) (string, error) {
			name := obj.(*unstructured.Unstructured).GetName()
			return "/projects/" + name, nil
		},
	}
	base := &kubernetes.Client{Client: client}
	base.Kubernetes = base
	fenced := &fencedClient{Interface: base, kv: client.KV, manager: manager, transport: transport}
	return client, manager, fenced
}

func findFenceTxnEtcd(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("short mode skips real-etcd tests")
	}
	if path := os.Getenv("TEST_ASSET_ETCD"); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		t.Fatalf("TEST_ASSET_ETCD is set but not executable/readable: %s", path)
	}
	if assets := os.Getenv("KUBEBUILDER_ASSETS"); assets != "" {
		path := filepath.Join(assets, "etcd")
		if _, err := os.Stat(path); err == nil {
			return path
		}
		t.Fatalf("KUBEBUILDER_ASSETS is set but etcd is missing: %s", path)
	}
	path, err := exec.LookPath("etcd")
	if err != nil {
		t.Fatalf("real-etcd transaction test requires etcd in PATH, TEST_ASSET_ETCD, or KUBEBUILDER_ASSETS: %v", err)
	}
	return path
}

func seedFenceTxnObjects(t *testing.T, client *clientv3.Client, manager *CommitFence) (context.Context, int64, int64) {
	t.Helper()
	rootPut, err := client.Put(context.Background(), txnTestProjectKey, "root-initial")
	require.NoError(t, err)
	targetPut, err := client.Put(context.Background(), txnTestTargetKey, "target-initial")
	require.NoError(t, err)
	proof := projectfence.Proof{
		ProjectName: "fence-project",
		ProjectUID:  "project-uid",
		AppUID:      "app-uid",
		Resource:    schema.GroupResource{Group: "test.example", Resource: "things"},
		Kind:        "Thing",
		Name:        "one",
		Namespace:   "default",
		UID:         "target-uid",
	}
	ctx := projectfence.WithContext(context.Background())
	require.True(t, projectfence.Record(ctx, proof, fmt.Sprint(rootPut.Header.Revision)))
	return ctx, rootPut.Header.Revision, targetPut.Header.Revision
}

func bindFenceTxnTarget(t *testing.T, ctx context.Context, target string) context.Context {
	t.Helper()
	require.True(t, projectfence.Bind(ctx, target))
	return ctx
}

func readFenceTxnValue(t *testing.T, client *clientv3.Client, key string) string {
	t.Helper()
	response, err := client.Get(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, response.Kvs, 1)
	return string(response.Kvs[0].Value)
}

func verifyFenceTxnValue(t *testing.T, client *clientv3.Client, key, want string) {
	t.Helper()
	require.Equal(t, want, readFenceTxnValue(t, client, key))
}

func readFenceTxnValueIfPresent(t *testing.T, client *clientv3.Client, key string) string {
	t.Helper()
	response, err := client.Get(context.Background(), key)
	require.NoError(t, err)
	if len(response.Kvs) == 0 {
		return ""
	}
	require.Len(t, response.Kvs, 1)
	return string(response.Kvs[0].Value)
}

func readFenceTxnModRevision(t *testing.T, client *clientv3.Client, key string) int64 {
	t.Helper()
	response, err := client.Get(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, response.Kvs, 1)
	return response.Kvs[0].ModRevision
}
