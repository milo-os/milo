package downstreamclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
)

// Errors returned by [UpstreamNamespaceResolver] implementations. Callers that
// handle untrusted downstream data MUST distinguish them: [ErrCacheNotSynced]
// is retryable and means "ask again shortly", while [ErrUpstreamNamespaceUnknown]
// is terminal and means the caller has no safe attribution for the record.
//
// Neither error may be treated as "attribute to the platform": a downstream
// namespace that cannot be resolved has no known tenant, and defaulting it to a
// tenant-less bucket leaks a customer's data across the tenancy boundary.
var (
	// ErrCacheNotSynced indicates the resolver's backing caches have not
	// finished their initial sync. The lookup may succeed once they have.
	ErrCacheNotSynced = errors.New("downstreamclient: upstream namespace cache has not synced")

	// ErrUpstreamNamespaceUnknown indicates no upstream namespace is known for
	// the requested downstream namespace.
	ErrUpstreamNamespaceUnknown = errors.New("downstreamclient: no upstream namespace known for downstream namespace")
)

// UpstreamNamespaceRef identifies the upstream namespace that a downstream
// ns-<uid> namespace was projected from.
type UpstreamNamespaceRef struct {
	// ClusterName is the decoded upstream cluster name, i.e. the value of
	// [UpstreamOwnerClusterNameLabel] with its "cluster-" prefix removed and
	// "_" restored to "/".
	ClusterName string

	// Namespace is the upstream namespace name.
	Namespace string
}

// IsZero reports whether the reference carries no cluster or namespace.
func (r UpstreamNamespaceRef) IsZero() bool {
	return r.ClusterName == "" && r.Namespace == ""
}

// UpstreamNamespaceResolver reverses the ns-<uid> mapping applied by
// [MappedNamespaceResourceStrategy].
//
// The contract is deliberately fail-closed. Implementations must return
// [ErrCacheNotSynced] while their backing caches are still warming and
// [ErrUpstreamNamespaceUnknown] when the namespace is genuinely unknown, and
// must never return a zero-valued reference with a nil error.
type UpstreamNamespaceResolver interface {
	// ResolveUpstreamNamespace returns the upstream namespace that
	// downstreamNamespace was projected from.
	ResolveUpstreamNamespace(ctx context.Context, downstreamNamespace string) (UpstreamNamespaceRef, error)

	// HasSynced reports whether every backing cache has completed its initial
	// sync. Readiness probes should gate on this rather than on a listener
	// being bound, so that traffic is never accepted against a cold cache.
	HasSynced() bool
}

// EncodeUpstreamClusterName renders a cluster name in the form stored in
// [UpstreamOwnerClusterNameLabel].
func EncodeUpstreamClusterName(clusterName string) string {
	return fmt.Sprintf("cluster-%s", strings.ReplaceAll(clusterName, "/", "_"))
}

// DecodeUpstreamClusterName reverses [EncodeUpstreamClusterName].
func DecodeUpstreamClusterName(encoded string) string {
	return strings.ReplaceAll(strings.TrimPrefix(encoded, "cluster-"), "_", "/")
}

// DownstreamNamespaceName renders the downstream namespace name for an upstream
// namespace UID.
func DownstreamNamespaceName(upstreamNamespaceUID types.UID) string {
	return fmt.Sprintf("ns-%s", upstreamNamespaceUID)
}

// UpstreamNamespaceRefFromDownstreamNamespace reads the meta.datumapis.com/*
// labels a projected namespace carries and returns the upstream namespace they
// describe. The second return value is false when the namespace carries no
// upstream labels, which is the normal case for namespaces that were not
// created by [MappedNamespaceResourceStrategy].
func UpstreamNamespaceRefFromDownstreamNamespace(namespace *corev1.Namespace) (UpstreamNamespaceRef, bool) {
	if namespace == nil {
		return UpstreamNamespaceRef{}, false
	}

	encodedCluster, hasCluster := namespace.Labels[UpstreamOwnerClusterNameLabel]
	upstreamNamespace, hasNamespace := namespace.Labels[UpstreamOwnerNamespaceLabel]
	if !hasCluster || !hasNamespace || encodedCluster == "" || upstreamNamespace == "" {
		return UpstreamNamespaceRef{}, false
	}

	return UpstreamNamespaceRef{
		ClusterName: DecodeUpstreamClusterName(encodedCluster),
		Namespace:   upstreamNamespace,
	}, true
}

// RetainingNamespaceIndex is an [UpstreamNamespaceResolver] backed by an
// in-memory index that only ever grows.
//
// Retention is the point. An informer cache drops an object when it is deleted,
// but records *about* a deleted namespace stay queryable for their full
// retention window and must keep resolving long after the namespace is gone.
// A plain informer cache would start failing those lookups the moment a project
// namespace is torn down, so entries here are never removed. Use [Snapshot] and
// [Restore] to carry the index across process restarts.
//
// The zero value is not usable; construct with [NewRetainingNamespaceIndex].
type RetainingNamespaceIndex struct {
	mu      sync.RWMutex
	entries map[string]UpstreamNamespaceRef

	syncedFuncs []toolscache.InformerSynced
}

var _ UpstreamNamespaceResolver = (*RetainingNamespaceIndex)(nil)

// NewRetainingNamespaceIndex returns an empty index.
func NewRetainingNamespaceIndex() *RetainingNamespaceIndex {
	return &RetainingNamespaceIndex{entries: map[string]UpstreamNamespaceRef{}}
}

// Upsert records the upstream namespace for a downstream namespace name.
// Existing entries are overwritten; entries are never removed.
func (i *RetainingNamespaceIndex) Upsert(downstreamNamespace string, ref UpstreamNamespaceRef) {
	if downstreamNamespace == "" || ref.IsZero() {
		return
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	i.entries[downstreamNamespace] = ref
}

// Len returns the number of indexed namespaces.
func (i *RetainingNamespaceIndex) Len() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.entries)
}

// ResolveUpstreamNamespace implements [UpstreamNamespaceResolver].
func (i *RetainingNamespaceIndex) ResolveUpstreamNamespace(_ context.Context, downstreamNamespace string) (UpstreamNamespaceRef, error) {
	i.mu.RLock()
	ref, ok := i.entries[downstreamNamespace]
	i.mu.RUnlock()

	if ok {
		return ref, nil
	}

	if !i.HasSynced() {
		return UpstreamNamespaceRef{}, fmt.Errorf("%w: %q", ErrCacheNotSynced, downstreamNamespace)
	}

	return UpstreamNamespaceRef{}, fmt.Errorf("%w: %q", ErrUpstreamNamespaceUnknown, downstreamNamespace)
}

// HasSynced implements [UpstreamNamespaceResolver]. An index with no registered
// sources is never considered synced, so that a misconfigured deployment fails
// its readiness probe instead of silently failing every lookup closed.
func (i *RetainingNamespaceIndex) HasSynced() bool {
	i.mu.RLock()
	syncedFuncs := make([]toolscache.InformerSynced, len(i.syncedFuncs))
	copy(syncedFuncs, i.syncedFuncs)
	i.mu.RUnlock()

	if len(syncedFuncs) == 0 {
		return false
	}

	for _, synced := range syncedFuncs {
		if !synced() {
			return false
		}
	}
	return true
}

// Snapshot returns a copy of the index contents, keyed by downstream namespace
// name. Callers persist this so that mappings for namespaces deleted while the
// process was down survive a restart.
func (i *RetainingNamespaceIndex) Snapshot() map[string]UpstreamNamespaceRef {
	i.mu.RLock()
	defer i.mu.RUnlock()

	out := make(map[string]UpstreamNamespaceRef, len(i.entries))
	for k, v := range i.entries {
		out[k] = v
	}
	return out
}

// Restore merges a previously taken [Snapshot] back into the index. Entries
// already present are left untouched, so live informer state always wins over
// persisted state.
func (i *RetainingNamespaceIndex) Restore(snapshot map[string]UpstreamNamespaceRef) {
	i.mu.Lock()
	defer i.mu.Unlock()

	for downstreamNamespace, ref := range snapshot {
		if downstreamNamespace == "" || ref.IsZero() {
			continue
		}
		if _, exists := i.entries[downstreamNamespace]; !exists {
			i.entries[downstreamNamespace] = ref
		}
	}
}

// AddSyncedFunc registers a cache-sync predicate that [HasSynced] must observe
// as true. Sources that populate the index without an informer (a restored
// snapshot, a test fixture) should register a func that returns true.
func (i *RetainingNamespaceIndex) AddSyncedFunc(synced toolscache.InformerSynced) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.syncedFuncs = append(i.syncedFuncs, synced)
}

// IndexUpstreamCluster wires an upstream cluster's namespace informer into the
// index. Every namespace observed on that cluster is recorded under the
// downstream name it projects to, so the reverse lookup needs no read against
// the downstream cluster and no credential into it.
//
// The returned error only covers informer setup; population happens
// asynchronously and is observable through [RetainingNamespaceIndex.HasSynced].
func (i *RetainingNamespaceIndex) IndexUpstreamCluster(ctx context.Context, clusterName string, upstreamCache cache.Cache) error {
	informer, err := upstreamCache.GetInformer(ctx, &corev1.Namespace{})
	if err != nil {
		return fmt.Errorf("failed getting namespace informer for cluster %q: %w", clusterName, err)
	}

	record := func(obj any) {
		namespace, ok := obj.(*corev1.Namespace)
		if !ok {
			return
		}
		i.Upsert(DownstreamNamespaceName(namespace.UID), UpstreamNamespaceRef{
			ClusterName: clusterName,
			Namespace:   namespace.Name,
		})
	}

	if _, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { record(obj) },
		UpdateFunc: func(_, obj any) { record(obj) },
	}); err != nil {
		return fmt.Errorf("failed adding namespace event handler for cluster %q: %w", clusterName, err)
	}

	i.AddSyncedFunc(informer.HasSynced)

	return nil
}
