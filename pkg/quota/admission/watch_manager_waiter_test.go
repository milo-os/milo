package admission

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
		_, cancel, err := w.RegisterClaimWaiter(context.Background(), fmt.Sprintf("claim-%d", i), "default", time.Nanosecond)
		if err != nil {
			t.Fatalf("register waiter %d: %v", i, err)
		}
		cancel()
	}
	time.Sleep(50 * time.Millisecond) // let the last callbacks run
}

// TestClaimWaiterTimeoutWhileStopping checks that waiters timing out while the
// manager stops do not panic: Stop closes every waiter's channel.
func TestClaimWaiterTimeoutWhileStopping(t *testing.T) {
	for range 200 {
		w := startedWatchManager(t)
		for i := range 50 {
			if _, _, err := w.RegisterClaimWaiter(context.Background(), fmt.Sprintf("claim-%d", i), "default", time.Nanosecond); err != nil {
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

	_, cancelOlder, err := w.RegisterClaimWaiter(context.Background(), "claim", "default", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	newer, _, err := w.RegisterClaimWaiter(context.Background(), "claim", "default", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cancelOlder()

	w.handleClaimDeletion(claimObject("claim", "default"))
	select {
	case result := <-newer:
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
		_, cancel, err := w.RegisterClaimWaiter(context.Background(), name, "default", time.Hour)
		if err != nil {
			t.Fatalf("register waiter %d: %v", i, err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); w.handleClaimDeletion(claimObject(name, "default")) }()
		go func() { defer wg.Done(); cancel() }()
		wg.Wait()
	}
}

func claimObject(name, namespace string) *unstructured.Unstructured {
	claim := &unstructured.Unstructured{}
	claim.SetName(name)
	claim.SetNamespace(namespace)
	return claim
}
