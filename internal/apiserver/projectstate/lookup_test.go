package projectstate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"go.miloapis.com/milo/internal/apiserver/projectstate"
	"go.miloapis.com/milo/internal/apiserver/projectstate/projectstatetest"
)

// TestLookup_ReadsAnUnseenProjectLive covers a Project the reflector has not
// seen, as in a new Project's first milliseconds: Lookup reads it from
// storage rather than answering "unknown".
func TestLookup_ReadsAnUnseenProjectLive(t *testing.T) {
	client := projectstatetest.NewClient(t, projectstatetest.NewProjectWithUID("p", "uid-1"))
	c := projectstate.NewCache(client) // not started: the reflector has seen nothing

	got, err := c.Lookup(context.Background(), "p")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.UID != "uid-1" || got.Deleting {
		t.Fatalf("expected the live Project uid-1, got %+v", got)
	}
}

// TestLookup_MissingProjectIsNotFound covers a name storage does not hold:
// the caller must be able to tell it from a failed read.
func TestLookup_MissingProjectIsNotFound(t *testing.T) {
	c := projectstate.NewCache(projectstatetest.NewClient(t))

	_, err := c.Lookup(context.Background(), "p")
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

// TestLookup_FailedReadIsAnInternalError covers a live GET that fails for any
// reason but NotFound, which upstream's NamespaceLifecycle returns as
// an internal error.
func TestLookup_FailedReadIsAnInternalError(t *testing.T) {
	client := projectstatetest.NewClient(t)
	projectstatetest.StorageAnswers(client, nil, errors.New("loopback unavailable"))
	c := projectstate.NewCache(client)

	_, err := c.Lookup(context.Background(), "p")
	if !apierrors.IsInternalError(err) {
		t.Fatalf("expected an InternalError, got %v", err)
	}
}

// TestLookup_SeenProjectIsAnsweredByTheReflector keeps the steady state
// free of live reads: a Project the reflector holds costs no GET.
func TestLookup_SeenProjectIsAnsweredByTheReflector(t *testing.T) {
	c, client := projectstatetest.NewCache(t, projectstatetest.NewProjectWithUID("p", "uid-1"))
	waitForProject(t, c, "p", projectstate.Project{UID: "uid-1"}, true)
	gets := projectstatetest.StorageAnswers(client, projectstatetest.NewProjectWithUID("p", "uid-2"), nil)

	got, err := c.Lookup(context.Background(), "p")
	if err != nil || got.UID != "uid-1" {
		t.Fatalf("expected the reflector's uid-1, got %+v, %v", got, err)
	}
	if n := gets.Load(); n != 0 {
		t.Fatalf("expected no live GET, got %d", n)
	}
}

// TestLookup_DeletedNameIsReadLiveUntilItsTTL covers a Project deleted and
// created again while the reflector still shows the old one live:
// after the delete is recorded, Lookup reads storage and sees the new
// UID; 30 s later the reflector answers again.
func TestLookup_DeletedNameIsReadLiveUntilItsTTL(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	c, client := projectstatetest.NewCacheWithClock(t, clk, projectstatetest.NewProjectWithUID("p", "uid-1"))
	waitForProject(t, c, "p", projectstate.Project{UID: "uid-1"}, true)
	gets := projectstatetest.StorageAnswers(client, projectstatetest.NewProjectWithUID("p", "uid-2"), nil)

	c.ForceLiveLookup("p")
	got, err := c.Lookup(context.Background(), "p")
	if err != nil || got.UID != "uid-2" {
		t.Fatalf("expected storage's uid-2 after the delete, got %+v, %v", got, err)
	}

	clk.Step(31 * time.Second)
	got, err = c.Lookup(context.Background(), "p")
	if err != nil || got.UID != "uid-1" {
		t.Fatalf("expected the reflector's uid-1 after the TTL, got %+v, %v", got, err)
	}
	if n := gets.Load(); n != 1 {
		t.Fatalf("expected one live GET, got %d", n)
	}
}

// TestLookup_DeletedNameShownDeletingIsNotReadLive keeps the purge cheap:
// once the reflector shows the deletion, its answer stands, as upstream
// forces a live read only while its cache says Active.
func TestLookup_DeletedNameShownDeletingIsNotReadLive(t *testing.T) {
	deleting := projectstatetest.NewProjectWithUID("p", "uid-1")
	deleting.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	deleting.SetFinalizers([]string{"example.com/finalizer"})
	c, client := projectstatetest.NewCache(t, deleting)
	waitForProject(t, c, "p", projectstate.Project{UID: "uid-1", Deleting: true}, true)
	gets := projectstatetest.StorageAnswers(client, projectstatetest.NewProjectWithUID("p", "uid-2"), nil)

	c.ForceLiveLookup("p")
	got, err := c.Lookup(context.Background(), "p")
	if err != nil || !got.Deleting {
		t.Fatalf("expected the reflector's deleting Project, got %+v, %v", got, err)
	}
	if n := gets.Load(); n != 0 {
		t.Fatalf("expected no live GET while the reflector shows the deletion, got %d", n)
	}
}
