package downstreamclient

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUpstreamClusterNameRoundTrip(t *testing.T) {
	for _, clusterName := range []string{"my-project", "root:org:project", "a/b/c"} {
		if got := DecodeUpstreamClusterName(EncodeUpstreamClusterName(clusterName)); got != clusterName {
			t.Errorf("round trip of %q returned %q", clusterName, got)
		}
	}
}

func TestUpstreamNamespaceRefFromDownstreamNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace *corev1.Namespace
		wantRef   UpstreamNamespaceRef
		wantOK    bool
	}{
		{
			name: "labelled namespace",
			namespace: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name: "ns-9f3c8a21",
				Labels: map[string]string{
					UpstreamOwnerClusterNameLabel: "cluster-my-project",
					UpstreamOwnerNamespaceLabel:   "default",
				},
			}},
			wantRef: UpstreamNamespaceRef{ClusterName: "my-project", Namespace: "default"},
			wantOK:  true,
		},
		{
			name: "path encoded cluster name",
			namespace: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					UpstreamOwnerClusterNameLabel: "cluster-root:org_project",
					UpstreamOwnerNamespaceLabel:   "team-a",
				},
			}},
			wantRef: UpstreamNamespaceRef{ClusterName: "root:org/project", Namespace: "team-a"},
			wantOK:  true,
		},
		{
			name: "missing namespace label",
			namespace: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{UpstreamOwnerClusterNameLabel: "cluster-my-project"},
			}},
			wantOK: false,
		},
		{
			name:      "unlabelled namespace",
			namespace: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}},
			wantOK:    false,
		},
		{
			name:   "nil namespace",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, ok := UpstreamNamespaceRefFromDownstreamNamespace(tt.namespace)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && ref != tt.wantRef {
				t.Errorf("ref = %+v, want %+v", ref, tt.wantRef)
			}
		})
	}
}

func TestRetainingNamespaceIndexColdCacheIsRetryable(t *testing.T) {
	index := NewRetainingNamespaceIndex()
	synced := false
	index.AddSyncedFunc(func() bool { return synced })

	_, err := index.ResolveUpstreamNamespace(context.Background(), "ns-unknown")
	if !errors.Is(err, ErrCacheNotSynced) {
		t.Fatalf("cold cache returned %v, want ErrCacheNotSynced", err)
	}

	synced = true
	_, err = index.ResolveUpstreamNamespace(context.Background(), "ns-unknown")
	if !errors.Is(err, ErrUpstreamNamespaceUnknown) {
		t.Fatalf("synced cache returned %v, want ErrUpstreamNamespaceUnknown", err)
	}
}

func TestRetainingNamespaceIndexWithNoSourcesIsNeverSynced(t *testing.T) {
	if NewRetainingNamespaceIndex().HasSynced() {
		t.Fatal("index with no registered sources reported synced")
	}
}

func TestRetainingNamespaceIndexRetainsDeletedNamespaces(t *testing.T) {
	index := NewRetainingNamespaceIndex()
	index.AddSyncedFunc(func() bool { return true })

	ref := UpstreamNamespaceRef{ClusterName: "my-project", Namespace: "default"}
	index.Upsert("ns-9f3c8a21", ref)

	got, err := index.ResolveUpstreamNamespace(context.Background(), "ns-9f3c8a21")
	if err != nil {
		t.Fatalf("resolve returned %v", err)
	}
	if got != ref {
		t.Fatalf("resolve returned %+v, want %+v", got, ref)
	}

	restored := NewRetainingNamespaceIndex()
	restored.AddSyncedFunc(func() bool { return true })
	restored.Restore(index.Snapshot())

	got, err = restored.ResolveUpstreamNamespace(context.Background(), "ns-9f3c8a21")
	if err != nil {
		t.Fatalf("resolve after restore returned %v", err)
	}
	if got != ref {
		t.Fatalf("resolve after restore returned %+v, want %+v", got, ref)
	}
}

func TestRetainingNamespaceIndexRestoreDoesNotOverwriteLiveEntries(t *testing.T) {
	index := NewRetainingNamespaceIndex()
	index.AddSyncedFunc(func() bool { return true })

	live := UpstreamNamespaceRef{ClusterName: "project-a", Namespace: "default"}
	index.Upsert("ns-1", live)
	index.Restore(map[string]UpstreamNamespaceRef{
		"ns-1": {ClusterName: "project-b", Namespace: "stale"},
		"ns-2": {ClusterName: "project-c", Namespace: "default"},
	})

	got, err := index.ResolveUpstreamNamespace(context.Background(), "ns-1")
	if err != nil {
		t.Fatalf("resolve returned %v", err)
	}
	if got != live {
		t.Errorf("live entry was overwritten by restore: %+v", got)
	}

	if _, err := index.ResolveUpstreamNamespace(context.Background(), "ns-2"); err != nil {
		t.Errorf("restored entry not resolvable: %v", err)
	}
}

func TestRetainingNamespaceIndexIgnoresIncompleteEntries(t *testing.T) {
	index := NewRetainingNamespaceIndex()
	index.AddSyncedFunc(func() bool { return true })

	index.Upsert("", UpstreamNamespaceRef{ClusterName: "a", Namespace: "b"})
	index.Upsert("ns-1", UpstreamNamespaceRef{})

	if index.Len() != 0 {
		t.Fatalf("index recorded %d incomplete entries", index.Len())
	}
}

func TestRetainingNamespaceIndexHasSyncedRequiresAllSources(t *testing.T) {
	index := NewRetainingNamespaceIndex()
	first, second := true, false
	index.AddSyncedFunc(func() bool { return first })
	index.AddSyncedFunc(func() bool { return second })

	if index.HasSynced() {
		t.Fatal("index reported synced while one source was cold")
	}

	second = true
	if !index.HasSynced() {
		t.Fatal("index reported unsynced once all sources were warm")
	}
}
