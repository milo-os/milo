package admission

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// startedWatchManager returns a watch manager that accepts waiters without a
// watch stream: these tests drive the waiters, not the stream.
func startedWatchManager(t *testing.T) *watchManager {
	t.Helper()
	w := NewWatchManager(nil, logr.Discard(), "project").(*watchManager)
	w.started.Store(true)
	return w
}

// TestClaimWaiterTimeoutWhileCancelled checks that a waiter whose timeout
// fires while it is cancelled does not panic. The timer's callback is started
// in its own goroutine, and Timer.Stop does not wait for it, so the callback
// can run after the cancel has closed the waiter's channel.
func TestClaimWaiterTimeoutWhileCancelled(t *testing.T) {
	w := startedWatchManager(t)
	defer w.Stop()

	for i := range 5000 {
		waiter, err := w.RegisterClaimWaiter(context.Background(), fmt.Sprintf("claim-%d", i), "default", time.Nanosecond)
		if err != nil {
			t.Fatalf("register waiter %d: %v", i, err)
		}
		waiter.Cancel()
	}
	time.Sleep(50 * time.Millisecond) // let the last callbacks run
}

// TestClaimWaiterTimeoutWhileStopping checks that waiters timing out while the
// manager stops do not panic: Stop closes every waiter's channel.
func TestClaimWaiterTimeoutWhileStopping(t *testing.T) {
	for range 200 {
		w := startedWatchManager(t)
		for i := range 50 {
			if _, err := w.RegisterClaimWaiter(context.Background(), fmt.Sprintf("claim-%d", i), "default", time.Nanosecond); err != nil {
				t.Fatalf("register waiter %d: %v", i, err)
			}
		}
		w.Stop()
	}
	time.Sleep(50 * time.Millisecond)
}

// TestClaimWaiterCancelKeepsANewerWaiter checks that cancelling a waiter
// removes that waiter only, not a newer one registered for the same claim.
func TestClaimWaiterCancelKeepsANewerWaiter(t *testing.T) {
	w := startedWatchManager(t)
	defer w.Stop()

	older, err := w.RegisterClaimWaiter(context.Background(), "claim", "default", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	older.Cancel()
	newer, err := w.RegisterClaimWaiter(context.Background(), "claim", "default", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	newer.Bind("newer")
	older.Cancel()

	w.handleClaimDeletion(claimObject("claim", "default", "newer"))
	select {
	case result := <-newer.Result():
		if result.Reason != "deleted" {
			t.Fatalf("the newer waiter received %q, want deleted", result.Reason)
		}
	case <-time.After(time.Second):
		t.Fatal("the newer waiter received nothing: cancelling the older one removed it")
	}
}

// TestClaimWaiterEventWhileCancelled checks that a claim event delivered while
// its waiter is cancelled does not panic.
func TestClaimWaiterEventWhileCancelled(t *testing.T) {
	w := startedWatchManager(t)
	defer w.Stop()

	for i := range 2000 {
		name := fmt.Sprintf("claim-%d", i)
		waiter, err := w.RegisterClaimWaiter(context.Background(), name, "default", time.Hour)
		if err != nil {
			t.Fatalf("register waiter %d: %v", i, err)
		}
		waiter.Bind("own")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); w.handleClaimDeletion(claimObject(name, "default", "own")) }()
		go func() { defer wg.Done(); waiter.Cancel() }()
		wg.Wait()
	}
}

// TestUnboundWaiterGetsItsOwnClaimsOutcomeAtBind checks that an outcome that
// arrives before Create returns is not lost: the waiter holds it, and Bind
// delivers it.
func TestUnboundWaiterGetsItsOwnClaimsOutcomeAtBind(t *testing.T) {
	w := startedWatchManager(t)
	defer w.Stop()
	waiter := registerWaiter(t, w)

	w.handleClaimEvent(judgedClaim("claim", "default", "own", true, quotav1alpha1.ResourceClaimGrantedReason))
	if result, ok := pendingResult(waiter); ok {
		t.Fatalf("an unbound waiter received %+v; it cannot know the claim is its own", result)
	}
	waiter.Bind("own")
	if result, ok := pendingResult(waiter); !ok || !result.Granted {
		t.Fatalf("Bind delivered %+v (received: %v), want the held grant", result, ok)
	}
}

// TestBindDropsAnotherClaimsHeldOutcome checks that Bind delivers only the
// outcome held for the waiter's own claim. The stale claim's deletion stays
// held and goes; the waiter then takes its own claim's grant.
func TestBindDropsAnotherClaimsHeldOutcome(t *testing.T) {
	w := startedWatchManager(t)
	defer w.Stop()
	waiter := registerWaiter(t, w)

	w.handleClaimDeletion(claimObject("claim", "default", "stale"))
	waiter.Bind("own")
	if result, ok := pendingResult(waiter); ok {
		t.Fatalf("Bind delivered %+v, which is the stale claim's", result)
	}
	w.handleClaimEvent(judgedClaim("claim", "default", "own", true, quotav1alpha1.ResourceClaimGrantedReason))
	if result, ok := pendingResult(waiter); !ok || !result.Granted {
		t.Fatalf("the waiter received %+v (received: %v), want its own claim's grant", result, ok)
	}
}

// TestUnboundWaiterTakesItsOwnClaimsDeletion checks that the request's own
// claim, deleted before Create returned, still ends the waiter as deleted.
func TestUnboundWaiterTakesItsOwnClaimsDeletion(t *testing.T) {
	w := startedWatchManager(t)
	defer w.Stop()
	waiter := registerWaiter(t, w)

	w.handleClaimDeletion(claimObject("claim", "default", "own"))
	waiter.Bind("own")
	if result, ok := pendingResult(waiter); !ok || result.Reason != "deleted" {
		t.Fatalf("Bind delivered %+v (received: %v), want deleted", result, ok)
	}
}

// TestUnboundWaiterHoldsABoundedSet checks that a name whose claims churn
// cannot grow an unbound waiter without limit, and that the bound drops the
// oldest outcome: the request's own claim is the latest of its name.
func TestUnboundWaiterHoldsABoundedSet(t *testing.T) {
	w := startedWatchManager(t)
	defer w.Stop()
	waiter := registerWaiter(t, w)

	const claims = 1000
	for i := range claims {
		uid := types.UID(fmt.Sprintf("claim-%d", i))
		w.handleClaimEvent(judgedClaim("claim", "default", uid, true, quotav1alpha1.ResourceClaimGrantedReason))
	}
	if held := heldCount(w, waiter); held > maxHeldOutcomes {
		t.Fatalf("an unbound waiter holds %d outcomes after %d claims; the bound is %d", held, claims, maxHeldOutcomes)
	}
	waiter.Bind(types.UID(fmt.Sprintf("claim-%d", claims-1)))
	if result, ok := pendingResult(waiter); !ok || !result.Granted {
		t.Fatalf("Bind delivered %+v (received: %v), want the latest claim's grant", result, ok)
	}
}

// TestHeldOutcomesGoWithTheWaiter checks that a waiter that times out unbound
// drops what it holds.
func TestHeldOutcomesGoWithTheWaiter(t *testing.T) {
	w := startedWatchManager(t)
	defer w.Stop()
	waiter, err := w.RegisterClaimWaiter(context.Background(), "claim", "default", 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	w.handleClaimDeletion(claimObject("claim", "default", "stale"))

	if result := <-waiter.Result(); result.Reason != "timeout" {
		t.Fatalf("the waiter received %+v, want its timeout", result)
	}
	if held := heldCount(w, waiter); held != 0 {
		t.Fatalf("a waiter that timed out still holds %d outcomes", held)
	}
}

func registerWaiter(t *testing.T, w *watchManager) ClaimWaiter {
	t.Helper()
	waiter, err := w.RegisterClaimWaiter(context.Background(), "claim", "default", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return waiter
}

// pendingResult returns the result the waiter has received, if any. The
// handlers and Bind deliver before they return, so no wait is needed.
func pendingResult(waiter ClaimWaiter) (ClaimResult, bool) {
	select {
	case result, ok := <-waiter.Result():
		return result, ok
	default:
		return ClaimResult{}, false
	}
}

func heldCount(w *watchManager, waiter ClaimWaiter) int {
	w.waitersLock.RLock()
	defer w.waitersLock.RUnlock()
	return len(waiter.(*claimWaiter).held)
}

func claimObject(name, namespace string, uid types.UID) *unstructured.Unstructured {
	claim := &unstructured.Unstructured{}
	claim.SetName(name)
	claim.SetNamespace(namespace)
	claim.SetUID(uid)
	return claim
}

func TestDuplicateClaimWaiterPreservesInFlightRequest(t *testing.T) {
	w := startedWatchManager(t)
	defer w.Stop()
	original := registerWaiter(t, w)
	original.Bind("own")
	if _, err := w.RegisterClaimWaiter(context.Background(), "claim", "default", time.Hour); err == nil {
		t.Fatal("duplicate waiter replaced original")
	} else if failure, ok := asClaimFailure(err); !ok || failure.kind != claimFailureConflict {
		t.Fatalf("expected retryable conflict, got %v", err)
	}
	w.handleClaimEvent(judgedClaim("claim", "default", "own", true, quotav1alpha1.ResourceClaimGrantedReason))
	if result, ok := pendingResult(original); !ok || !result.Granted {
		t.Fatalf("original waiter lost outcome: %+v (%v)", result, ok)
	}
}

func TestDeletedDeniedClaimPreservesQuotaDenial(t *testing.T) {
	w := startedWatchManager(t)
	defer w.Stop()
	waiter, err := w.RegisterClaimWaiter(context.Background(), "claim", "default", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	waiter.Bind("own")
	claim := judgedClaim("claim", "default", "own", false, quotav1alpha1.ResourceClaimDeniedReason)
	w.handleClaimDeletion(claim)
	result, ok := pendingResult(waiter)
	if !ok || result.Error != nil || result.Granted || result.Reason != quotav1alpha1.ResourceClaimDeniedReason {
		t.Fatalf("got %+v (%v), want quota denial", result, ok)
	}
}

func TestDeletedGrantDoesNotAdmitRequest(t *testing.T) {
	w := startedWatchManager(t)
	defer w.Stop()
	waiter, err := w.RegisterClaimWaiter(context.Background(), "claim", "default", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	waiter.Bind("own")
	w.handleClaimDeletion(judgedClaim("claim", "default", "own", true, quotav1alpha1.ResourceClaimGrantedReason))
	result, ok := pendingResult(waiter)
	if !ok || result.Error == nil || result.Granted {
		t.Fatalf("deleted capacity must not admit: %+v (%v)", result, ok)
	}
}

func TestDeletedDenialForAnotherUIDDoesNotEndWaiter(t *testing.T) {
	w := startedWatchManager(t)
	defer w.Stop()
	waiter, err := w.RegisterClaimWaiter(context.Background(), "claim", "default", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	waiter.Bind("own")
	w.handleClaimDeletion(judgedClaim("claim", "default", "older", false, quotav1alpha1.ResourceClaimDeniedReason))
	if result, ok := pendingResult(waiter); ok {
		t.Fatalf("old claim ended new waiter: %+v", result)
	}
}
