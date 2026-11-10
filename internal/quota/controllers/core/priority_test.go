package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

const project multicluster.ClusterName = "p1"

func claimOf(name, resourceType string, conditions ...metav1.Condition) *quotav1alpha1.ResourceClaim {
	return &quotav1alpha1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: quotav1alpha1.ResourceClaimSpec{
			ConsumerRef: quotav1alpha1.ConsumerRef{APIGroup: "resourcemanager.miloapis.com", Kind: "Project", Name: "p1"},
			Requests:    []quotav1alpha1.ResourceRequest{{ResourceType: resourceType, Amount: 1}},
		},
		Status: quotav1alpha1.ResourceClaimStatus{Conditions: conditions},
	}
}

func granted(status metav1.ConditionStatus, reason string) metav1.Condition {
	return metav1.Condition{Type: quotav1alpha1.ResourceClaimGranted, Status: status, Reason: reason}
}

// bucketOf returns the request the bucket handler makes for a claim of resourceType in project.
func bucketOf(resourceType string) mcreconcile.Request {
	return affectedBuckets(project, claimOf("any", resourceType))[0]
}

func bucketHandler() handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return claimMapped(project, affectedBuckets)
}

// next takes the next ready item and its priority, or fails after a second.
func next(t *testing.T, q priorityqueue.PriorityQueue[mcreconcile.Request]) (mcreconcile.Request, int) {
	t.Helper()
	type got struct {
		item     mcreconcile.Request
		priority int
	}
	ch := make(chan got, 1)
	go func() { item, priority, _ := q.GetWithPriority(); ch <- got{item, priority} }()
	select {
	case g := <-ch:
		q.Done(g.item)
		return g.item, g.priority
	case <-time.After(time.Second):
		t.Fatal("no item became ready in a second")
		return mcreconcile.Request{}, 0
	}
}

func newQueue(t *testing.T) priorityqueue.PriorityQueue[mcreconcile.Request] {
	t.Helper()
	q := priorityqueue.New[mcreconcile.Request]("test")
	t.Cleanup(q.ShutDown)
	return q
}

func replay(q priorityqueue.PriorityQueue[mcreconcile.Request], resourceTypes ...string) {
	for _, rt := range resourceTypes {
		q.Add(bucketOf(rt))
	}
}

func TestClaimUndecided(t *testing.T) {
	for name, tc := range map[string]struct {
		claim *quotav1alpha1.ResourceClaim
		want  bool
	}{
		"no Granted condition":     {claimOf("c", "a"), true},
		"Granted=False, pending":   {claimOf("c", "a", granted(metav1.ConditionFalse, quotav1alpha1.ResourceClaimPendingReason)), true},
		"Granted=Unknown":          {claimOf("c", "a", granted(metav1.ConditionUnknown, "")), true},
		"Granted=True":             {claimOf("c", "a", granted(metav1.ConditionTrue, quotav1alpha1.ResourceClaimGrantedReason)), false},
		"Granted=False and Denied": {claimOf("c", "a", granted(metav1.ConditionFalse, quotav1alpha1.ResourceClaimDeniedReason)), false},
	} {
		t.Run(name, func(t *testing.T) { assert.Equal(t, tc.want, claimUndecided(tc.claim)) })
	}
}

// A new claim in an engaged project during a replay: its bucket goes ahead of the replayed ones.
func TestAnUndecidedClaimsBucketGoesAheadOfTheReplay(t *testing.T) {
	q := newQueue(t)
	replay(q, "a", "b", "c")
	bucketHandler().Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: claimOf("new", "d")}, q)

	item, priority := next(t, q)
	assert.Equal(t, bucketOf("d"), item)
	assert.Equal(t, undecidedClaimPriority, priority)
}

// The claim's bucket is already queued by the replay: it is raised in place, not queued twice.
func TestAnUndecidedClaimRaisesItsQueuedBucket(t *testing.T) {
	q := newQueue(t)
	replay(q, "a", "b", "c")
	bucketHandler().Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: claimOf("new", "c")}, q)

	item, priority := next(t, q)
	assert.Equal(t, bucketOf("c"), item)
	assert.Equal(t, undecidedClaimPriority, priority)
	assert.Equal(t, 2, q.Len(), "the raised bucket is not queued twice")
}

// A decided claim's events add at the default priority, as today.
func TestADecidedClaimAddsAtDefault(t *testing.T) {
	for name, cond := range map[string]metav1.Condition{
		"granted": granted(metav1.ConditionTrue, quotav1alpha1.ResourceClaimGrantedReason),
		"denied":  granted(metav1.ConditionFalse, quotav1alpha1.ResourceClaimDeniedReason),
	} {
		t.Run(name, func(t *testing.T) {
			q := newQueue(t)
			replay(q, "a")
			bucketHandler().Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: claimOf("old", "b", cond)}, q)

			item, priority := next(t, q)
			assert.Equal(t, bucketOf("a"), item, "the replay's bucket stays first")
			assert.Equal(t, 0, priority)
		})
	}
}

// An undecided claim from the initial list goes at undecidedClaimPriority even when the event marks it low, as
// controller-runtime's map handler does once the source marks the initial list.
func TestAReplayedUndecidedClaimGoesAhead(t *testing.T) {
	q := newQueue(t)
	replay(q, "a")
	bucketHandler().Create(context.Background(),
		event.TypedCreateEvent[client.Object]{Object: claimOf("waiting", "b"), IsInInitialList: true}, q)

	item, priority := next(t, q)
	assert.Equal(t, bucketOf("b"), item)
	assert.Equal(t, undecidedClaimPriority, priority)
}

// A claim stuck undecided keeps its priority on a retry, but each retry waits out its backoff, so it cannot starve
// the queue: the controller re-adds a failed item rate-limited at the priority it was taken with.
func TestAStuckClaimsRetryWaitsOutItsBackoff(t *testing.T) {
	q := priorityqueue.New[mcreconcile.Request]("test", func(o *priorityqueue.Opts[mcreconcile.Request]) {
		o.RateLimiter = workqueue.NewTypedItemExponentialFailureRateLimiter[mcreconcile.Request](time.Hour, time.Hour)
	})
	t.Cleanup(q.ShutDown)
	bucketHandler().Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: claimOf("stuck", "b")}, q)
	item, priority := next(t, q)
	require.Equal(t, undecidedClaimPriority, priority)

	q.AddWithOpts(priorityqueue.AddOpts{RateLimited: true, Priority: &priority}, item) // the controller's retry
	replay(q, "a")

	other, otherPriority := next(t, q)
	assert.Equal(t, bucketOf("a"), other, "the retry waits out its backoff")
	assert.Equal(t, 0, otherPriority)
	assert.Equal(t, 0, q.Len(), "the stuck claim's bucket is not ready yet")
}

// Without a priority queue (UsePriorityQueue=false) the handler adds plainly, as today.
func TestWithoutAPriorityQueueTheHandlerAddsPlainly(t *testing.T) {
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[mcreconcile.Request]())
	t.Cleanup(q.ShutDown)
	bucketHandler().Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: claimOf("new", "b")}, q)

	require.Equal(t, 1, q.Len())
	item, _ := q.Get()
	assert.Equal(t, bucketOf("b"), item)
}

// The resource-claim handler enqueues the claim itself, in its cluster, on a status-only update, and an undecided
// claim goes ahead.
func TestTheClaimHandlerEnqueuesAStatusOnlyUpdate(t *testing.T) {
	q := newQueue(t)
	q.Add(mcreconcile.Request{ClusterName: project, Request: reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "older"}}})
	old := claimOf("c1", "a")
	old.ResourceVersion = "1" // a real update changes it; equal versions read as a resync, which the map handler lowers
	updated := old.DeepCopy()
	updated.ResourceVersion = "2"
	updated.Status.Allocations = []quotav1alpha1.ResourceClaimAllocationStatus{{ResourceType: "a",
		Status: quotav1alpha1.ResourceClaimAllocationStatusGranted}}

	claimMapped(project, claimItself).Update(context.Background(),
		event.TypedUpdateEvent[client.Object]{ObjectOld: old, ObjectNew: updated}, q)

	item, priority := next(t, q)
	assert.Equal(t, mcreconcile.Request{ClusterName: project,
		Request: reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "c1"}}}, item)
	assert.Equal(t, undecidedClaimPriority, priority)
}

// Requests carry the cluster the handler was built for: the library's cluster-preserving handler leaves it to the
// map function.
func TestRequestsCarryTheHandlersCluster(t *testing.T) {
	q := newQueue(t)
	claimMapped("p7", affectedBuckets).Create(context.Background(),
		event.TypedCreateEvent[client.Object]{Object: claimOf("new", "a")}, q)

	item, _ := next(t, q)
	assert.Equal(t, multicluster.ClusterName("p7"), item.ClusterName)
}

// An update that decides the claim (pending to Granted) adds at the default priority: no create waits on it any more.
func TestAClaimDecidedByTheUpdateAddsAtDefault(t *testing.T) {
	q := newQueue(t)
	old := claimOf("c1", "a", granted(metav1.ConditionFalse, quotav1alpha1.ResourceClaimPendingReason))
	decided := claimOf("c1", "a", granted(metav1.ConditionTrue, quotav1alpha1.ResourceClaimGrantedReason))
	old.ResourceVersion, decided.ResourceVersion = "1", "2"

	claimMapped(project, claimItself).Update(context.Background(),
		event.TypedUpdateEvent[client.Object]{ObjectOld: old, ObjectNew: decided}, q)

	_, priority := next(t, q)
	assert.Equal(t, 0, priority)
}
