package discovery

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func makeRegistry() *Registry {
	return NewRegistry()
}

func TestPolicyPrecedenceOverCRD(t *testing.T) {
	r := makeRegistry()
	gr := schema.GroupResource{Group: "example.com", Resource: "widgets"}

	r.crd[gr] = []ParentContext{ContextPlatform}
	r.upsertFromPolicy("test-policy", policySpec{
		rules: []policyRule{
			{group: "example.com", resources: []string{"widgets"}, contexts: []string{"Project"}},
		},
	})

	got := r.AllowedContexts(gr)
	if len(got) != 1 || got[0] != ContextProject {
		t.Errorf("AllowedContexts = %v, want [Project]", got)
	}
}

func TestPolicyPrecedenceOverStatic(t *testing.T) {
	r := makeRegistry()
	gr := schema.GroupResource{Group: "example.com", Resource: "gadgets"}

	r.RegisterStatic(gr, ContextOrganization)
	r.upsertFromPolicy("test-policy", policySpec{
		rules: []policyRule{
			{group: "example.com", resources: []string{"gadgets"}, contexts: []string{"Project"}},
		},
	})

	got := r.AllowedContexts(gr)
	if len(got) != 1 || got[0] != ContextProject {
		t.Errorf("AllowedContexts = %v, want [Project]", got)
	}
}

func TestWildcardGroupAll(t *testing.T) {
	r := makeRegistry()
	r.upsertFromPolicy("wild-policy", policySpec{
		rules: []policyRule{
			{group: "*", resources: []string{"pods"}, contexts: []string{"Project"}},
		},
	})

	got := r.AllowedContexts(schema.GroupResource{Group: "anything", Resource: "pods"})
	if len(got) != 1 || got[0] != ContextProject {
		t.Errorf("AllowedContexts(wildcard group) = %v, want [Project]", got)
	}
}

func TestWildcardResourceAll(t *testing.T) {
	r := makeRegistry()
	r.upsertFromPolicy("wild-policy", policySpec{
		rules: []policyRule{
			{group: "apps", resources: []string{"*"}, contexts: []string{"Project"}},
		},
	})

	for _, resource := range []string{"deployments", "statefulsets", "replicasets"} {
		got := r.AllowedContexts(schema.GroupResource{Group: "apps", Resource: resource})
		if len(got) != 1 || got[0] != ContextProject {
			t.Errorf("AllowedContexts(apps/%s) = %v, want [Project]", resource, got)
		}
	}

	// Different group should not match.
	got := r.AllowedContexts(schema.GroupResource{Group: "batch", Resource: "jobs"})
	if got != nil {
		t.Errorf("AllowedContexts(batch/jobs) = %v, want nil (no match)", got)
	}
}

func TestExactBeatsWildcard(t *testing.T) {
	r := makeRegistry()
	gr := schema.GroupResource{Group: "apps", Resource: "deployments"}

	r.upsertFromPolicy("wild-policy", policySpec{
		rules: []policyRule{
			{group: "apps", resources: []string{"*"}, contexts: []string{"Organization"}},
		},
	})
	r.upsertFromPolicy("exact-policy", policySpec{
		rules: []policyRule{
			{group: "apps", resources: []string{"deployments"}, contexts: []string{"Project"}},
		},
	})

	got := r.AllowedContexts(gr)
	if len(got) != 1 || got[0] != ContextProject {
		t.Errorf("AllowedContexts = %v, want [Project] (exact beats wildcard)", got)
	}
}

func TestMultiPolicyConflict(t *testing.T) {
	r := makeRegistry()
	gr := schema.GroupResource{Group: "batch", Resource: "jobs"}

	// "alpha-policy" comes alphabetically before "zeta-policy".
	r.upsertFromPolicy("zeta-policy", policySpec{
		rules: []policyRule{
			{group: "batch", resources: []string{"*"}, contexts: []string{"Organization"}},
		},
	})
	r.upsertFromPolicy("alpha-policy", policySpec{
		rules: []policyRule{
			{group: "batch", resources: []string{"*"}, contexts: []string{"Project"}},
		},
	})

	got := r.AllowedContexts(gr)
	if len(got) != 1 || got[0] != ContextProject {
		t.Errorf("AllowedContexts = %v, want [Project] (alpha-policy wins)", got)
	}
}

func TestDeletePolicyRemovesEntries(t *testing.T) {
	r := makeRegistry()
	gr := schema.GroupResource{Group: "example.com", Resource: "things"}

	r.crd[gr] = []ParentContext{ContextOrganization}
	r.upsertFromPolicy("removable-policy", policySpec{
		rules: []policyRule{
			{group: "example.com", resources: []string{"things"}, contexts: []string{"Project"}},
		},
	})

	// Policy should take precedence.
	got := r.AllowedContexts(gr)
	if len(got) != 1 || got[0] != ContextProject {
		t.Fatalf("before delete: AllowedContexts = %v, want [Project]", got)
	}

	r.deleteFromPolicy("removable-policy")

	// After deletion, should fall back to CRD value.
	got = r.AllowedContexts(gr)
	if len(got) != 1 || got[0] != ContextOrganization {
		t.Errorf("after delete: AllowedContexts = %v, want [Organization]", got)
	}
}

func TestDeletePolicyRemovesWildcardEntries(t *testing.T) {
	r := makeRegistry()
	gr := schema.GroupResource{Group: "apps", Resource: "deployments"}

	r.crd[gr] = []ParentContext{ContextOrganization}
	r.upsertFromPolicy("removable-policy", policySpec{
		rules: []policyRule{
			{group: "apps", resources: []string{"*"}, contexts: []string{"Project"}},
		},
	})

	got := r.AllowedContexts(gr)
	if len(got) != 1 || got[0] != ContextProject {
		t.Fatalf("before delete: AllowedContexts = %v, want [Project]", got)
	}

	r.deleteFromPolicy("removable-policy")

	got = r.AllowedContexts(gr)
	if len(got) != 1 || got[0] != ContextOrganization {
		t.Errorf("after delete: AllowedContexts = %v, want [Organization] (wildcard entry not cleaned up)", got)
	}
}

func TestExactMatchConflictBetweenPolicies(t *testing.T) {
	r := makeRegistry()
	gr := schema.GroupResource{Group: "example.com", Resource: "widgets"}

	// "alpha-policy" wins alphabetically over "zeta-policy".
	r.upsertFromPolicy("zeta-policy", policySpec{
		rules: []policyRule{
			{group: "example.com", resources: []string{"widgets"}, contexts: []string{"Organization"}},
		},
	})
	r.upsertFromPolicy("alpha-policy", policySpec{
		rules: []policyRule{
			{group: "example.com", resources: []string{"widgets"}, contexts: []string{"Project"}},
		},
	})

	got := r.AllowedContexts(gr)
	if len(got) != 1 || got[0] != ContextProject {
		t.Errorf("AllowedContexts = %v, want [Project] (alpha-policy wins exact conflict)", got)
	}
}

func TestHasSyncedRequiresBothInformers(t *testing.T) {
	r := makeRegistry()

	if r.HasSynced() {
		t.Error("HasSynced() = true before any informer synced")
	}

	r.hasInit = true
	if r.HasSynced() {
		t.Error("HasSynced() = true with only CRD informer synced")
	}

	r.hasInit = false
	r.hasPolicyInit = true
	if r.HasSynced() {
		t.Error("HasSynced() = true with only policy informer synced")
	}

	r.hasInit = true
	if !r.HasSynced() {
		t.Error("HasSynced() = false with both informers synced")
	}
}
