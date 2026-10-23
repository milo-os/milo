package projectstate_test

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/component-base/metrics/legacyregistry"

	"go.miloapis.com/milo/internal/apiserver/projectstate"
	"go.miloapis.com/milo/internal/apiserver/projectstate/projectstatetest"
)

// TestLiveFromView_AnswersALiveUnforcedProject covers the read's view:
// a Project the reflector shows live is answered without storage, and a name
// whose DELETE was recorded is not, because only storage knows which Project
// it refers to now.
func TestLiveFromView_AnswersALiveUnforcedProject(t *testing.T) {
	c, client := projectstatetest.NewCache(t, projectstatetest.NewProjectWithUID("p", "uid-1"))
	waitForProject(t, c, "p", projectstate.Project{UID: "uid-1"}, true)
	gets := projectstatetest.StorageAnswers(client, projectstatetest.NewProjectWithUID("p", "uid-2"), nil)

	if got, ok := c.LiveFromView("p"); !ok || got.UID != "uid-1" {
		t.Fatalf("expected the reflector's live uid-1, got %+v, %v", got, ok)
	}
	c.ForceLiveLookup("p")
	if got, ok := c.LiveFromView("p"); ok {
		t.Fatalf("expected no answer for a name whose DELETE was recorded, got %+v", got)
	}
	if n := gets.Load(); n != 0 {
		t.Fatalf("expected the view to read no storage, got %d GETs", n)
	}
}

// TestLiveFromView_UnseenOrDeletingIsNotLive covers the two other answers the
// view cannot give: a name the reflector has not seen, and a Project it
// shows deleting.
func TestLiveFromView_UnseenOrDeletingIsNotLive(t *testing.T) {
	deleting := projectstatetest.NewProjectWithUID("d", "uid-1")
	deleting.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	deleting.SetFinalizers([]string{"example.com/finalizer"})
	c, _ := projectstatetest.NewCache(t, deleting)
	waitForProject(t, c, "d", projectstate.Project{UID: "uid-1", Deleting: true}, true)

	if got, ok := c.LiveFromView("unseen"); ok {
		t.Fatalf("expected no answer for an unseen name, got %+v", got)
	}
	if got, ok := c.LiveFromView("d"); ok {
		t.Fatalf("expected no answer for a deleting Project, got %+v", got)
	}
}

// TestLookup_CountsEachLiveReadByReasonFoundAndRequest keeps the live reads'
// cost visible in production: each storage read counts once, by why the
// view did not answer, whether storage held the Project, and whether the
// request reads or writes. An answer from the reflector counts nothing.
func TestLookup_CountsEachLiveReadByReasonFoundAndRequest(t *testing.T) {
	c, client := projectstatetest.NewCache(t, projectstatetest.NewProjectWithUID("seen", "uid-1"))
	waitForProject(t, c, "seen", projectstate.Project{UID: "uid-1"}, true)
	projectstatetest.StorageAnswers(client, projectstatetest.NewProjectWithUID("live", "uid-2"), nil)
	before := lookupCounts(t)

	_, _ = c.Lookup(withVerb("create"), "live")
	_, _ = c.Lookup(withVerb("get"), "live")
	c.ForceLiveLookup("seen")
	_, _ = c.Lookup(withVerb("patch"), "seen")
	c2 := projectstate.NewCache(projectstatetest.NewClient(t))
	_, _ = c2.Lookup(withVerb("update"), "gone")
	_, _ = projectstate.NewCache(projectstatetest.NewClient(t, projectstatetest.NewProjectWithUID("x", "u"))).
		Lookup(withVerb("watch"), "x")

	want := map[string]float64{"unseen/true/write": 1, "unseen/true/read": 2, "forced/true/write": 1,
		"unseen/false/write": 1}
	for key, n := range want {
		if got := lookupCounts(t)[key] - before[key]; got != n {
			t.Fatalf("expected %v live reads counted as %s, got %v", n, key, got)
		}
	}
}

// withVerb returns the context of a request with verb.
func withVerb(verb string) context.Context {
	return genericapirequest.WithRequestInfo(context.Background(),
		&genericapirequest.RequestInfo{IsResourceRequest: true, Verb: verb})
}

// lookupCounts reads projectstorage_project_lookups_total from the registry the
// server exposes, keyed reason/found/request.
func lookupCounts(t *testing.T) map[string]float64 {
	t.Helper()
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather the metrics: %v", err)
	}
	out := map[string]float64{}
	for _, f := range families {
		if f.GetName() != "projectstorage_project_lookups_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			out[labels["reason"]+"/"+labels["found"]+"/"+labels["request"]] = m.GetCounter().GetValue()
		}
	}
	return out
}
