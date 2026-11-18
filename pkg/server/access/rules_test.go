package access

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/component-base/metrics/testutil"
)

func crd(plural string, annotations map[string]string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: plural + "." + group, Annotations: annotations, ResourceVersion: "1"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: plural},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v1alpha1", Served: true,
				SelectableFields: []apiextensionsv1.SelectableField{{JSONPath: ".spec.userRef.name"}, {JSONPath: ".spec.hidden"}},
			}},
		},
	}
}

// staticRules returns synced rules over a fixed set of CRDs, and the store
// holding them.
func staticRules(crds ...*apiextensionsv1.CustomResourceDefinition) (*Rules, cache.Store) {
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for _, c := range crds {
		_ = store.Add(c)
	}
	r := NewRules()
	r.crds = store
	r.synced = func() bool { return true }
	return r, store
}

// startRules attaches rules to a fake API server holding crds, and returns
// once they have synced, along with the fake client to change CRDs.
func startRules(t *testing.T, crds ...*apiextensionsv1.CustomResourceDefinition) (*Rules, *apiextensionsfake.Clientset) {
	t.Helper()
	client := apiextensionsfake.NewClientset()
	for _, c := range crds {
		if _, err := client.ApiextensionsV1().CustomResourceDefinitions().Create(context.Background(), c, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	factory := apiextensionsinformers.NewSharedInformerFactory(client, 0)
	rules := NewRules()
	if err := rules.Attach(factory); err != nil {
		t.Fatal(err)
	}
	factory.Start(ctx.Done())
	waitFor(t, "rules synced", rules.HasSynced)
	return rules, client
}

// invalidSeries counts the milo_access_rule_invalid series set to 1 that
// match labels.
func invalidSeries(t *testing.T, labels map[string]string) int {
	t.Helper()
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, mf := range families {
		if mf.GetName() != "milo_access_rule_invalid" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if testutil.LabelsMatch(m, labels) && m.GetGauge().GetValue() == 1 {
				n++
			}
		}
	}
	return n
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	if err := wait.PollUntilContextTimeout(context.Background(), 10*time.Millisecond, 5*time.Second, true,
		func(context.Context) (bool, error) { return cond(), nil }); err != nil {
		t.Fatalf("waiting for %s: %v", what, err)
	}
}

func TestRulesFollowCRDs(t *testing.T) {
	votesGR := schema.GroupResource{Group: group, Resource: "votes"}
	releasesGR := schema.GroupResource{Group: group, Resource: "releases"}

	rules, client := startRules(t,
		crd("votes", map[string]string{OwnerFieldAnnotation: "spec.userRef.name"}),
		crd("releases", nil),
	)
	if l := rules.Lookup(votesGR); l.State != Enforced || l.Rule.OwnerField != "spec.userRef.name" {
		t.Fatalf("votes after sync = %+v", l)
	}
	if l := rules.Lookup(releasesGR); l.State != Unrestricted {
		t.Fatalf("unannotated CRD = %+v", l)
	}

	ctx := context.Background()
	crds := client.ApiextensionsV1().CustomResourceDefinitions()
	update := func(c *apiextensionsv1.CustomResourceDefinition) {
		t.Helper()
		if _, err := crds.Update(ctx, c, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	// Annotation added.
	update(crd("releases", map[string]string{HiddenFieldAnnotation: "spec.hidden"}))
	waitFor(t, "releases rule", func() bool {
		l := rules.Lookup(releasesGR)
		return l.State == Enforced && l.Rule.HiddenField == "spec.hidden" && l.Rule.HiddenSubresource == DefaultHiddenSubresource
	})

	// Annotation misspelt: refused, and reported.
	update(crd("releases", map[string]string{"iam.miloapis.com/hiden-field": "spec.hidden"}))
	waitFor(t, "releases refused", func() bool { return rules.Lookup(releasesGR).State == Refused })
	waitFor(t, "invalid metric", func() bool {
		rules.mu.RLock()
		defer rules.mu.RUnlock()
		return len(rules.invalid[releasesGR]) == 1
	})
	if got := invalidSeries(t, map[string]string{"group": group, "resource": "releases", "reason": ReasonUnknownAnnotation}); got != 1 {
		t.Errorf("invalid series = %d, want 1", got)
	}

	// Annotation removed: no rule, metric cleared.
	update(crd("releases", nil))
	waitFor(t, "releases rule removed", func() bool { return rules.Lookup(releasesGR).State == Unrestricted })
	waitFor(t, "invalid metric cleared", func() bool {
		rules.mu.RLock()
		defer rules.mu.RUnlock()
		return len(rules.invalid[releasesGR]) == 0
	})
	if got := invalidSeries(t, map[string]string{"resource": "releases"}); got != 0 {
		t.Errorf("invalid series = %d, want 0", got)
	}

	// CRD deleted.
	if err := crds.Delete(ctx, "votes."+group, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "votes rule removed", func() bool { return rules.Lookup(votesGR).State == Unrestricted })
}

// TestRuleIsKnownAsSoonAsTheCRDIsServable covers the window the reviewer
// found: a rule must apply as soon as the CRD handler can serve the
// resource. Both read the same cache, so a CRD that is in it has its rule,
// with no event handler in between.
func TestRuleIsKnownAsSoonAsTheCRDIsServable(t *testing.T) {
	rules, store := staticRules()
	gr := schema.GroupResource{Group: group, Resource: "votes"}
	if l := rules.Lookup(gr); l.State != Unrestricted {
		t.Fatalf("before the CRD = %+v", l)
	}
	_ = store.Add(crd("votes", map[string]string{OwnerFieldAnnotation: "spec.userRef.name"}))
	if l := rules.Lookup(gr); l.State != Enforced {
		t.Errorf("once the CRD is cached = %+v, want enforced", l)
	}
	c := crd("votes", map[string]string{OwnerFieldAnnotation: " "})
	c.ResourceVersion = "2"
	_ = store.Update(c)
	if l := rules.Lookup(gr); l.State != Refused {
		t.Errorf("once the cached CRD is invalid = %+v, want refused", l)
	}
}

func TestNotSyncedRefusesEverything(t *testing.T) {
	r := NewRules()
	if r.HasSynced() || r.Check() == nil {
		t.Error("new rules report synced")
	}
	if l := r.Lookup(schema.GroupResource{Resource: "pods"}); l.State != Refused || l.Err.Status().Code != 503 {
		t.Errorf("lookup before sync = %+v", l)
	}
}

func TestDeletedTombstoneClearsTheMetric(t *testing.T) {
	rules := NewRules()
	c := crd("tombstones", map[string]string{OwnerFieldAnnotation: ""})
	rules.observe(c)
	gr := schema.GroupResource{Group: group, Resource: "tombstones"}
	if len(rules.invalid[gr]) != 1 {
		t.Fatalf("invalid = %v", rules.invalid[gr])
	}
	rules.forget(cache.DeletedFinalStateUnknown{Key: c.Name, Obj: c})
	if _, ok := rules.invalid[gr]; ok {
		t.Error("invalid declaration kept after a tombstone delete")
	}
	if _, ok := rules.decls[c.Name]; ok {
		t.Error("declaration cached after a tombstone delete")
	}
}

// TestStaleCRDIsRefused checks a CRD whose cached copy stops following
// storage is refused, while other CRDs stay served and the readiness check
// keeps passing: a lag every replica shares mustn't take them all out of
// rotation.
func TestStaleCRDIsRefused(t *testing.T) {
	votesGR := schema.GroupResource{Group: group, Resource: "votes"}
	notesGR := schema.GroupResource{Group: group, Resource: "notes"}
	owned := map[string]string{OwnerFieldAnnotation: "spec.userRef.name"}
	rules, store := staticRules(crd("votes", owned), crd("notes", owned))
	live := map[string]string{"votes." + group: "1", "notes." + group: "1"}
	list := func(context.Context) (map[string]string, error) { return live, nil }
	ctx := context.Background()
	served := func(gr schema.GroupResource) bool { return rules.Lookup(gr).State == Enforced }

	lagging := rules.checkStaleness(ctx, list, nil)
	if len(lagging) != 0 || len(rules.stale) != 0 {
		t.Fatalf("in step: lagging %v, stale %v", lagging, rules.stale)
	}

	// Storage moves on; the first check only notes it.
	live = map[string]string{"votes." + group: "2", "posts." + group: "5", "notes." + group: "1"}
	lagging = rules.checkStaleness(ctx, list, lagging)
	if len(lagging) != 2 || len(rules.stale) != 0 || !served(votesGR) {
		t.Fatalf("first lag: lagging %v, stale %v", lagging, rules.stale)
	}

	// A grace period later votes hasn't moved: it's stale, and only it.
	lagging = rules.checkStaleness(ctx, list, lagging)
	if l := rules.Lookup(votesGR); l.State != Refused || !strings.Contains(l.Err.Error(), "out of date") ||
		!strings.Contains(l.Err.Error(), "votes."+group) {
		t.Errorf("stale CRD = %+v", l)
	}
	if !served(notesGR) {
		t.Errorf("CRD in step while another is stale = %+v", rules.Lookup(notesGR))
	}
	if l := rules.Lookup(schema.GroupResource{Group: "iam.miloapis.com", Resource: "users-not-a-crd"}); l.State != Unrestricted {
		t.Errorf("resource without a CRD while a CRD is stale = %+v", l)
	}
	if err := rules.Check(); err != nil {
		t.Errorf("readiness while a CRD is stale = %v, want ok", err)
	}

	// A storage read that fails keeps the verdict.
	failing := func(context.Context) (map[string]string, error) { return nil, fmt.Errorf("boom") }
	lagging = rules.checkStaleness(ctx, failing, lagging)
	if served(votesGR) {
		t.Error("a failed check cleared staleness")
	}

	// The cache catches up.
	c := crd("votes", owned)
	c.ResourceVersion = "2"
	_ = store.Update(c)
	p := crd("posts", nil)
	p.ResourceVersion = "5"
	_ = store.Add(p)
	if lagging = rules.checkStaleness(ctx, list, lagging); len(lagging) != 0 || len(rules.stale) != 0 {
		t.Errorf("caught up: lagging %v, stale %v", lagging, rules.stale)
	}
	if !served(votesGR) {
		t.Errorf("after catching up = %+v", rules.Lookup(votesGR))
	}
}

func TestMovingCacheIsNotStale(t *testing.T) {
	rules, store := staticRules(crd("votes", nil))
	live := map[string]string{"votes." + group: "3"}
	list := func(context.Context) (map[string]string, error) { return live, nil }
	lagging := rules.checkStaleness(context.Background(), list, nil)
	// The cache moved, though not all the way, before the next check.
	c := crd("votes", nil)
	c.ResourceVersion = "2"
	_ = store.Update(c)
	rules.checkStaleness(context.Background(), list, lagging)
	if len(rules.stale) != 0 {
		t.Errorf("moving cache is stale: %v", rules.stale)
	}
}

func TestDeclare(t *testing.T) {
	withScale := func(c *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinition {
		c.Spec.Versions[0].Subresources = &apiextensionsv1.CustomResourceSubresources{Scale: &apiextensionsv1.CustomResourceSubresourceScale{
			SpecReplicasPath: ".spec.userRef", StatusReplicasPath: ".status.replicas",
		}}
		return c
	}
	withStatusOwner := func(c *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinition {
		c.Spec.Versions[0].SelectableFields = append(c.Spec.Versions[0].SelectableFields, apiextensionsv1.SelectableField{JSONPath: ".status.owner"})
		return c
	}
	for _, tc := range []struct {
		name        string
		crd         *apiextensionsv1.CustomResourceDefinition
		wantReasons []string
		wantRule    Rule
	}{
		{"no annotations", crd("a", nil), nil, Rule{}},
		{"owner and hidden", crd("a", map[string]string{OwnerFieldAnnotation: ".spec.userRef.name", HiddenFieldAnnotation: "spec.hidden", "example.com/x": "y"}), nil,
			Rule{OwnerField: "spec.userRef.name", HiddenField: "spec.hidden", HiddenSubresource: DefaultHiddenSubresource}},
		{"named hidden subresource", crd("a", map[string]string{HiddenFieldAnnotation: "spec.hidden", HiddenSubresourceAnnotation: "staff"}), nil,
			Rule{HiddenField: "spec.hidden", HiddenSubresource: "staff"}},
		{"blank owner", crd("a", map[string]string{OwnerFieldAnnotation: " "}), []string{ReasonBlank}, Rule{}},
		{"blank hidden subresource", crd("a", map[string]string{HiddenFieldAnnotation: "spec.hidden", HiddenSubresourceAnnotation: ""}), []string{ReasonBlank},
			Rule{HiddenField: "spec.hidden", HiddenSubresource: DefaultHiddenSubresource}},
		{"misspelt", crd("a", map[string]string{"iam.miloapis.com/owner-feild": "spec.userRef.name"}), []string{ReasonUnknownAnnotation}, Rule{}},
		{"not selectable", crd("a", map[string]string{OwnerFieldAnnotation: "spec.owner"}), []string{ReasonNotSelectable}, Rule{OwnerField: "spec.owner"}},
		{"bracketed path", crd("a", map[string]string{HiddenFieldAnnotation: "spec['hidden']"}), []string{ReasonInvalidPath}, Rule{}},
		{"subresource without field", crd("a", map[string]string{HiddenSubresourceAnnotation: "staff"}), []string{ReasonSubresourceNoField}, Rule{}},
		{"owner under status", withStatusOwner(crd("a", map[string]string{OwnerFieldAnnotation: "status.owner"})), []string{ReasonOwnerUnderStatus},
			Rule{OwnerField: "status.owner"}},
		{"owner under a scale path", withScale(crd("a", map[string]string{OwnerFieldAnnotation: "spec.userRef.name"})), []string{ReasonOwnerInScalePath},
			Rule{OwnerField: "spec.userRef.name"}},
	} {
		d := Declare(tc.crd)
		var reasons []string
		for _, p := range d.Problems {
			reasons = append(reasons, p.Reason)
			if p.Annotation == "" || p.Message == "" {
				t.Errorf("%s: problem without annotation or message: %+v", tc.name, p)
			}
		}
		if !equalStrings(reasons, tc.wantReasons) {
			t.Errorf("%s: reasons = %v, want %v (%s)", tc.name, reasons, tc.wantReasons, d.Error())
		}
		if d.Rule != tc.wantRule {
			t.Errorf("%s: rule = %+v, want %+v", tc.name, d.Rule, tc.wantRule)
		}
	}
	if !selectable(crd("a", nil), "spec.userRef.name") || selectable(crd("a", nil), "spec.owner") {
		t.Error("selectable reports the wrong fields")
	}
}
