package etcdshared

import (
	"context"
	"testing"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/kubernetes"

	"k8s.io/apiserver/pkg/storage/storagebackend"
)

func newFakeClient() *kubernetes.Client {
	return &kubernetes.Client{Client: clientv3.NewCtxClient(context.Background())}
}

func closed(c *kubernetes.Client) bool {
	return c.Ctx().Err() != nil
}

func withFakeClientConstructor(t *testing.T) *int {
	t.Helper()
	calls := 0
	orig := newSharedETCDClient
	newSharedETCDClient = func(storagebackend.TransportConfig) (*kubernetes.Client, error) {
		calls++
		return newFakeClient(), nil
	}
	t.Cleanup(func() {
		newSharedETCDClient = orig
		clientsMu.Lock()
		clients = map[string]*runningClient{}
		clientsMu.Unlock()
	})
	return &calls
}

func mkTransport(servers ...string) storagebackend.TransportConfig {
	return storagebackend.TransportConfig{ServerList: servers}
}

func TestAcquireClient_SharedAcrossProjects(t *testing.T) {
	calls := withFakeClientConstructor(t)
	tc := mkTransport("https://etcd:2379")

	c1, rel1, err := acquireClient(tc, 0)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	c2, rel2, err := acquireClient(tc, 0)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}

	// The pool for a transport is dialed once, lazily, on first acquire.
	if *calls != sharedClientPoolSize {
		t.Fatalf("expected pool dialed once (%d clients), got %d", sharedClientPoolSize, *calls)
	}
	if c1 == nil || c2 == nil {
		t.Fatalf("expected non-nil clients from the pool")
	}

	// Releasing one project's storage must NOT close the pool while another holds a ref.
	rel1()
	if closed(c1) {
		t.Fatalf("pool closed while a reference is still held")
	}

	// Last release closes the pool and drops the cache entry.
	rel2()
	if !closed(c1) || !closed(c2) {
		t.Fatalf("pool not closed after final release")
	}

	clientsMu.Lock()
	_, present := clients[transportKey(tc)]
	clientsMu.Unlock()
	if present {
		t.Fatalf("cache entry not removed after final release")
	}
}

func TestAcquireClient_DistinctTransportsDoNotShare(t *testing.T) {
	calls := withFakeClientConstructor(t)

	cA, relA, err := acquireClient(mkTransport("https://a:2379"), 0)
	if err != nil {
		t.Fatalf("acquire A: %v", err)
	}
	cB, relB, err := acquireClient(mkTransport("https://b:2379"), 0)
	if err != nil {
		t.Fatalf("acquire B: %v", err)
	}

	if *calls != 2*sharedClientPoolSize {
		t.Fatalf("expected two pools dialed (%d clients), got %d", 2*sharedClientPoolSize, *calls)
	}
	if cA == cB {
		t.Fatalf("distinct transports unexpectedly shared a client")
	}

	relA()
	relB()
	if !closed(cA) || !closed(cB) {
		t.Fatalf("clients not closed after release")
	}
}

func TestAcquireClient_ReClosedAfterFullReleaseCycle(t *testing.T) {
	calls := withFakeClientConstructor(t)
	tc := mkTransport("https://etcd:2379")

	_, rel1, _ := acquireClient(tc, 0)
	rel1()
	if *calls != sharedClientPoolSize {
		t.Fatalf("expected one pool dialed (%d clients), got %d", sharedClientPoolSize, *calls)
	}

	// After the entry was torn down, a fresh acquire dials a new pool.
	_, rel2, _ := acquireClient(tc, 0)
	if *calls != 2*sharedClientPoolSize {
		t.Fatalf("expected re-dial after teardown (%d clients), got %d", 2*sharedClientPoolSize, *calls)
	}
	rel2()
}
