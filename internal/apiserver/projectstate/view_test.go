package projectstate_test

import (
	"context"
	"testing"

	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/component-base/metrics/legacyregistry"

	"go.miloapis.com/milo/internal/apiserver/projectstate"
	"go.miloapis.com/milo/internal/apiserver/projectstate/projectstatetest"
)

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
