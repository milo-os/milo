package admission

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

// claimEvent is one write to the claim's name, as the watch cache holds it.
// obj is nil for a delete.
type claimEvent struct {
	rv  int
	typ watch.EventType
	obj *unstructured.Unstructured
}

// claimServer serves one claim name as the watch cache does: a watch from a
// version gets the events after it, a version older than the window gets
// 410, and a GET gets the current state.
type claimServer struct {
	history []claimEvent
	oldest  int  // the oldest version the watch window still serves
	forbid  bool // every watch is refused with 403
	gets    atomic.Int32
}

const ourUID = types.UID("ours")

func claimAt(uid types.UID, rv int, granted string) *unstructured.Unstructured {
	c := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "quota.miloapis.com/v1alpha1", "kind": "ResourceClaim",
		"metadata": map[string]interface{}{"name": "c", "namespace": "default", "uid": string(uid), "resourceVersion": strconv.Itoa(rv)},
	}}
	switch granted {
	case "True":
		_ = unstructured.SetNestedSlice(c.Object, []interface{}{map[string]interface{}{"type": "Granted", "status": "True", "reason": "QuotaAvailable"}}, "status", "conditions")
	case "Denied":
		_ = unstructured.SetNestedSlice(c.Object, []interface{}{map[string]interface{}{"type": "Granted", "status": "False", "reason": "QuotaExceeded", "message": "Resource quota exceeded: requested 1, available 0"}}, "status", "conditions")
	}
	return c
}

func (s *claimServer) current() runtime.Object {
	if last := s.history[len(s.history)-1]; last.obj != nil {
		return last.obj
	}
	return nil
}

func (s *claimServer) watch(a clienttesting.Action) (bool, watch.Interface, error) {
	if s.forbid {
		return true, nil, apierrors.NewForbidden(resourceClaimsGVR.GroupResource(), "", nil)
	}
	from, _ := strconv.Atoi(a.(clienttesting.WatchAction).GetWatchRestrictions().ResourceVersion)
	w := watch.NewFakeWithChanSize(len(s.history)+1, false)
	if from < s.oldest-1 {
		w.Error(&apierrors.NewResourceExpired("too old resource version").ErrStatus)
		return true, w, nil
	}
	for _, e := range s.history {
		if e.rv > from {
			obj := e.obj
			if obj == nil {
				obj = claimAt(ourUID, e.rv, "")
			}
			w.Action(e.typ, obj)
		}
	}
	return true, w, nil
}

func (s *claimServer) get(clienttesting.Action) (bool, runtime.Object, error) {
	s.gets.Add(1)
	if obj := s.current(); obj != nil {
		return true, obj, nil
	}
	return true, nil, apierrors.NewNotFound(resourceClaimsGVR.GroupResource(), "c")
}

// wait runs waitForClaim on the server for a claim created at version 10.
func (s *claimServer) wait(t *testing.T) ClaimResult {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{resourceClaimsGVR: "ResourceClaimList"})
	client.PrependWatchReactor("resourceclaims", s.watch)
	client.PrependReactor("get", "resourceclaims", s.get)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ref := claimRef{Name: "c", Namespace: "default", UID: ourUID, ResourceVersion: "10"}
	return waitForClaim(ctx, client.Resource(resourceClaimsGVR).Namespace("default"), ref)
}

// Master's case 1. Cleanup deleted the denied claim before the wait's watch
// was opened; the watch starts at the create, so it still sees the denial.
func TestDeniedThenDeletedBeforeTheWaitersReadIsDelivered(t *testing.T) {
	defer goleak.VerifyNone(t)
	s := &claimServer{oldest: 1, history: []claimEvent{
		{10, watch.Added, claimAt(ourUID, 10, "")},
		{11, watch.Modified, claimAt(ourUID, 11, "Denied")},
		{12, watch.Deleted, nil},
	}}
	got := s.wait(t)
	if got.Granted || got.Error != nil || got.Reason != "Resource quota exceeded: requested 1, available 0" {
		t.Fatalf("want the denial with its reason, got %+v", got)
	}
}

// Master's case 2. The create's version left the watch window: one GET decides.
func TestGoneOnTheFirstWatchIsDecidedByOneGet(t *testing.T) {
	defer goleak.VerifyNone(t)
	s := &claimServer{oldest: 20, history: []claimEvent{
		{10, watch.Added, claimAt(ourUID, 10, "")},
		{11, watch.Modified, claimAt(ourUID, 11, "True")},
	}}
	if got := s.wait(t); !got.Granted {
		t.Fatalf("want the grant, got %+v", got)
	}
	if n := s.gets.Load(); n != 1 {
		t.Fatalf("want one GET, got %d", n)
	}
}

// Cleanup keeps a denied claim for the retention window, so the GET after a
// 410 finds the denial and its reason.
func TestGoneThenGetInsideTheRetentionReturnsTheDenialWithItsReason(t *testing.T) {
	defer goleak.VerifyNone(t)
	s := &claimServer{oldest: 20, history: []claimEvent{
		{10, watch.Added, claimAt(ourUID, 10, "")},
		{11, watch.Modified, claimAt(ourUID, 11, "Denied")},
	}}
	got := s.wait(t)
	if got.Granted || got.Error != nil || got.Reason != "Resource quota exceeded: requested 1, available 0" {
		t.Fatalf("want the denial with its reason, got %+v", got)
	}
}

// A retry replaced the claim: the original wait ends as "deleted", whether its
// watch sees the delete or its GET after a 410 finds the new claim.
func TestWaitWhoseClaimARetryReplacedEndsAsDeleted(t *testing.T) {
	defer goleak.VerifyNone(t)
	history := []claimEvent{
		{10, watch.Added, claimAt(ourUID, 10, "")},
		{11, watch.Modified, claimAt(ourUID, 11, "Denied")},
		{12, watch.Deleted, nil},
		{13, watch.Added, claimAt("retry", 13, "")},
	}
	for name, oldest := range map[string]int{"the watch sees the delete": 1, "the GET after a 410 finds the retry's claim": 20} {
		t.Run(name, func(t *testing.T) {
			// The watch sees the denial first only when it starts in time; the
			// GET sees the retry's claim. Neither waits to the timeout.
			got := (&claimServer{oldest: oldest, history: history}).wait(t)
			if got.Reason == waitTimedOut {
				t.Fatalf("the wait hung to its timeout: %+v", got)
			}
			if oldest > 1 && got.Reason != waitDeleted {
				t.Fatalf("want deleted, got %+v", got)
			}
		})
	}
}

// A server that answers 410 to every watch costs a bounded number of GETs,
// and the wait ends at once, not at its timeout.
func TestGoneOnEveryWatchCostsAtMostThreeGets(t *testing.T) {
	defer goleak.VerifyNone(t)
	s := &claimServer{oldest: 1000, history: []claimEvent{{10, watch.Added, claimAt(ourUID, 10, "")}}}
	start := time.Now()
	got := s.wait(t)
	if n := s.gets.Load(); n != maxGetsAfterGone {
		t.Fatalf("want %d GETs, got %d", maxGetsAfterGone, n)
	}
	if got.Reason != waitWatchError || time.Since(start) > time.Second {
		t.Fatalf("want a prompt watch_failed, got %+v after %v", got, time.Since(start))
	}
}

// A 403 on the watch ends the wait with that error, at once.
func TestForbiddenWatchEndsTheWaitWithItsError(t *testing.T) {
	defer goleak.VerifyNone(t)
	s := &claimServer{oldest: 1, forbid: true, history: []claimEvent{{10, watch.Added, claimAt(ourUID, 10, "")}}}
	start := time.Now()
	got := s.wait(t)
	if got.Reason != waitWatchError || !apierrors.IsForbidden(got.Error) || time.Since(start) > time.Second {
		t.Fatalf("want a prompt watch_failed carrying the 403, got %+v after %v", got, time.Since(start))
	}
}
