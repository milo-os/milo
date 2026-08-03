package milo

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

// TODO(datum-cloud/compute#117): this file works around a bug in
// sigs.k8s.io/multicluster-runtime, not something specific to this provider.
// mcManager.Start auto-adds a ProviderRunnable's Start method as a plain
// manager.RunnableFunc, which does not implement NeedLeaderElection.
// controller-runtime's runnable-group router silently routes any Runnable
// that isn't a LeaderElectionRunnable into the leader-election group "for
// backwards compatibility" (pkg/manager/runnable_group.go). That leader-gates
// cluster engagement: on every replica but the elected leader, Start never
// runs, so Provider.mcAware stays nil forever and Reconcile requeues without
// ever engaging a cluster. Any consumer running this provider with more than
// one replica -- including our own controller-manager's quota system, see
// cmd/milo/controller-manager/controllermanager.go -- silently only serves
// multicluster requests correctly from the leader pod.
//
// The right fix is upstream in sigs.k8s.io/multicluster-runtime: the runnable
// mcManager.Start adds for a ProviderRunnable should be forced non-leader-gated
// the same way WithoutAutoStart/EngageAlways do here, since cluster engagement
// is local per-pod cache/watch bookkeeping (like the manager's own informer
// cache, which controller-runtime never leader-gates), not reconciliation that
// needs deduplicating across replicas. Once that lands and is vendored here,
// delete this file, drop the two call sites, and pass the provider directly to
// mcmanager.New again.

// WithoutAutoStart returns p as a multicluster.Provider that does not also
// satisfy multicluster.ProviderRunnable, so mcmanager.New/mcManager.Start will
// not auto-wire (and therefore not silently leader-gate) p.Start. Pass the
// result to mcmanager.New in place of p, and call EngageAlways once mgr is
// constructed to wire p.Start back in without the leader-gating.
func WithoutAutoStart(p *Provider) multicluster.Provider {
	return struct{ providerOnly }{p}
}

// providerOnly is multicluster.Provider without the Start method that
// ProviderRunnable adds.
type providerOnly interface {
	Get(ctx context.Context, clusterName multicluster.ClusterName) (cluster.Cluster, error)
	IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error
}

// EngageAlways registers p's Start method with mgr's local manager, forced
// into controller-runtime's always-running runnable group so cluster
// discovery and engagement happen on every replica regardless of leader
// election. Call once, after constructing mgr with WithoutAutoStart(p), and
// before mgr.Start.
func EngageAlways(mgr mcmanager.Manager, p *Provider) error {
	return mgr.GetLocalManager().Add(nonLeaderGatedRunnable{
		Runnable: manager.RunnableFunc(func(ctx context.Context) error {
			return p.Start(ctx, mgr)
		}),
	})
}

// nonLeaderGatedRunnable forces a manager.Runnable into controller-runtime's
// always-running runnable group, regardless of leader-election state.
type nonLeaderGatedRunnable struct {
	manager.Runnable
}

func (nonLeaderGatedRunnable) NeedLeaderElection() bool { return false }
