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

	if *calls != 1 {
		t.Fatalf("expected client dialed once, got %d", *calls)
	}
	if c1 != c2 {
		t.Fatalf("expected same shared client pointer across acquisitions")
	}

	// Releasing one project's storage must NOT close the client while another holds a ref.
	rel1()
	if closed(c1) {
		t.Fatalf("client closed while a reference is still held")
	}

	// Last release closes exactly once and drops the cache entry.
	rel2()
	if !closed(c1) {
		t.Fatalf("client not closed after final release")
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

	if *calls != 2 {
		t.Fatalf("expected two clients dialed, got %d", *calls)
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
	if *calls != 1 {
		t.Fatalf("expected one dial, got %d", *calls)
	}

	// After the entry was torn down, a fresh acquire dials a new client.
	_, rel2, _ := acquireClient(tc, 0)
	if *calls != 2 {
		t.Fatalf("expected re-dial after teardown, got %d", *calls)
	}
	rel2()
}
