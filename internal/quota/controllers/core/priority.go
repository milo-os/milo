package core

import (
	"context"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// undecidedClaimPriority puts the work a waiting create depends on ahead of
// ordinary events (priority 0) and of an informer's initial list
// (handler.LowPriority, -100). After a leader change the new leader re-reads
// every bucket and claim; without it, a new claim waits behind that replay
// past admission's claim wait.
const undecidedClaimPriority = 100

// ordinaryPriority is the priority of work no waiting create depends on. A
// requeue keeps the priority its item was taken at, so work that comes back
// on its own sets this; otherwise a bucket an undecided claim once raised
// would come back ahead of other work for as long as this leader runs.
const ordinaryPriority = 0

// claimUndecided reports whether a create may still be waiting on the claim:
// it has no Granted condition, or Granted=False for a reason other than
// QuotaExceeded, the denial.
// Admission's wait reads the condition the same way.
func claimUndecided(claim *quotav1alpha1.ResourceClaim) bool {
	cond := apimeta.FindStatusCondition(claim.Status.Conditions, quotav1alpha1.ResourceClaimGranted)
	if cond == nil {
		return true
	}
	return cond.Status != metav1.ConditionTrue && cond.Reason != quotav1alpha1.ResourceClaimDeniedReason
}

// claimMapped returns the handler for a watch on ResourceClaims in the named
// cluster: the library's cluster-preserving map handler, whose map function
// sets the cluster on each request, with an undecided claim's requests put
// ahead of other work.
func claimMapped(cluster multicluster.ClusterName,
	toRequests func(multicluster.ClusterName, client.Object) []mcreconcile.Request) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return undecidedFirst{mchandler.TypedEnqueueRequestsFromMapFuncWithClusterPreservation[client.Object, mcreconcile.Request](
		func(_ context.Context, obj client.Object) []mcreconcile.Request { return toRequests(cluster, obj) })}
}

// claimItself maps a claim to its own request in the named cluster.
func claimItself(cluster multicluster.ClusterName, obj client.Object) []mcreconcile.Request {
	return []mcreconcile.Request{{ClusterName: cluster, Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)}}}
}

// undecidedFirst hands the handler it wraps a queue that adds at
// undecidedClaimPriority when the event's claim is undecided. A delete or a
// generic event, a decided claim, and a queue that is not a priority queue
// pass through unchanged.
type undecidedFirst struct {
	handler.TypedEventHandler[client.Object, mcreconcile.Request]
}

func (u undecidedFirst) Create(ctx context.Context, e event.TypedCreateEvent[client.Object],
	q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
	u.TypedEventHandler.Create(ctx, e, queueFor(e.Object, q))
}

func (u undecidedFirst) Update(ctx context.Context, e event.TypedUpdateEvent[client.Object],
	q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
	u.TypedEventHandler.Update(ctx, e, queueFor(e.ObjectNew, q))
}

func queueFor(obj client.Object, q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) workqueue.TypedRateLimitingInterface[mcreconcile.Request] {
	claim, isClaim := obj.(*quotav1alpha1.ResourceClaim)
	pq, isPriorityQueue := q.(priorityqueue.PriorityQueue[mcreconcile.Request])
	if !isClaim || !isPriorityQueue || !claimUndecided(claim) {
		return q
	}
	return withPriority{PriorityQueue: pq, priority: undecidedClaimPriority}
}

// withPriority adds every item at its priority or higher. It differs from
// controller-runtime's own default-priority queue, which fills in a missing
// priority only: an undecided claim from the initial list, or a resync of it,
// which the map handler marks low, must still go ahead. The map handler adds
// through Add and AddWithOpts only; a retry goes to the controller's own
// queue at the priority its item was taken with.
type withPriority struct {
	priorityqueue.PriorityQueue[mcreconcile.Request]
	priority int
}

func (w withPriority) Add(item mcreconcile.Request) {
	w.AddWithOpts(priorityqueue.AddOpts{}, item)
}

func (w withPriority) AddWithOpts(o priorityqueue.AddOpts, items ...mcreconcile.Request) {
	if o.Priority == nil || *o.Priority < w.priority {
		o.Priority = ptr.To(w.priority)
	}
	w.PriorityQueue.AddWithOpts(o, items...)
}
