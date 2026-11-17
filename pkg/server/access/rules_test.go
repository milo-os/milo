package access

import (
	"context"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
)

func crd(plural string, annotations map[string]string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: plural + "." + group, Annotations: annotations},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: plural},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v1alpha1", Served: true,
				SelectableFields: []apiextensionsv1.SelectableField{{JSONPath: ".spec.userRef.name"}},
			}},
		},
	}
}

// startRules runs rules from a fake API server holding crds, and returns
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
	go func() { _ = rules.Run(ctx, factory) }()
	// Run registers its handler before the factory starts it, as the server
	// does by starting the shared factory after post-start hooks run.
	waitFor(t, "informer registered", func() bool {
		factory.Start(ctx.Done())
		return rules.HasSynced()
	})
	return rules, client
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
	if r, ok := rules.Get(votesGR); !ok || r.OwnerField != "spec.userRef.name" {
		t.Fatalf("votes rule after sync = %+v, %v", r, ok)
	}
	if _, ok := rules.Get(releasesGR); ok {
		t.Fatal("unannotated CRD has a rule")
	}

	ctx := context.Background()
	crds := client.ApiextensionsV1().CustomResourceDefinitions()

	// Annotation added.
	rel := crd("releases", map[string]string{HiddenFieldAnnotation: "spec.hidden"})
	if _, err := crds.Update(ctx, rel, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "releases rule", func() bool {
		r, ok := rules.Get(releasesGR)
		return ok && r.HiddenField == "spec.hidden" && r.HiddenSubresource == DefaultHiddenSubresource
	})

	// Annotation changed.
	if _, err := crds.Update(ctx, crd("votes", map[string]string{OwnerFieldAnnotation: "spec.owner"}), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "votes rule change", func() bool { r, _ := rules.Get(votesGR); return r.OwnerField == "spec.owner" })

	// Annotation removed.
	if _, err := crds.Update(ctx, crd("releases", nil), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "releases rule removed", func() bool { _, ok := rules.Get(releasesGR); return !ok })

	// CRD deleted.
	if err := crds.Delete(ctx, "votes."+group, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "votes rule removed", func() bool { _, ok := rules.Get(votesGR); return !ok })
}

func TestRulesAreNotSyncedBeforeTheInformer(t *testing.T) {
	if NewRules().HasSynced() {
		t.Error("new rules report synced")
	}
}

func TestDeletedTombstoneRemovesTheRule(t *testing.T) {
	rules := NewRules()
	c := crd("votes", map[string]string{OwnerFieldAnnotation: "spec.userRef.name"})
	rules.upsert(c)
	rules.remove(cache.DeletedFinalStateUnknown{Key: c.Name, Obj: c})
	if _, ok := rules.Get(schema.GroupResource{Group: group, Resource: "votes"}); ok {
		t.Error("rule kept after a tombstone delete")
	}
}
