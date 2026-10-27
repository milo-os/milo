package discovery

import (
	"cmp"
	"maps"
	"os"
	"slices"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

var passkeys = schema.GroupResource{Group: "identity.miloapis.com", Resource: "passkeys"}

// inlineAPIServerRegistry builds the registry the way the API server built it
// before this package held the built-in types: four static registrations made
// inline at startup. It is the oracle for "every caller of NewRegistry gets
// what the API server had".
func inlineAPIServerRegistry() *Registry {
	r := newRegistry()
	for _, resource := range []string{"sessions", "useridentities", "passkeys", "passkeyregistrationlinks"} {
		r.RegisterStatic(schema.GroupResource{Group: "identity.miloapis.com", Resource: resource}, ContextUser)
	}
	return r
}

// feed gives a registry the same declarations through every input: Milo's own
// core policy, a policy and a CRD annotation that each name a built-in type (so
// a change of precedence shows), and CRDs with and without the annotation.
func feed(t *testing.T, r *Registry) {
	t.Helper()
	r.upsertPolicyFromUnstructured(shippedPolicy(t, "core"))
	r.upsertFromPolicy("fixture", policySpec{rules: []policyRule{
		{group: "identity.miloapis.com", resources: []string{"sessions"}, contexts: []string{"Organization"}},
	}})
	for _, crd := range []*apiextensionsv1.CustomResourceDefinition{
		annotatedCRD(passkeys.Group, passkeys.Resource, "Organization"),
		annotatedCRD("example.com", "widgets", "Project"),
		annotatedCRD("example.com", "gadgets", ""),
		annotatedCRD("example.com", "doodads", AllContextsWildcard),
	} {
		r.upsertFromObj(crd)
	}
}

// shippedPolicy reads one of the DiscoveryContextPolicy objects Milo ships in config/discovery/, by its directory.
func shippedPolicy(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile("../../../config/discovery/" + name + "/discovery-context-policy.yaml")
	if err != nil {
		t.Fatalf("read the %s policy: %v", name, err)
	}
	u := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(raw, &u.Object); err != nil {
		t.Fatalf("decode the %s policy: %v", name, err)
	}
	return u
}

func annotatedCRD(group, plural, contexts string) *apiextensionsv1.CustomResourceDefinition {
	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: plural},
		},
	}
	if contexts != "" {
		crd.ObjectMeta = metav1.ObjectMeta{Annotations: map[string]string{ParentContextsAnnotation: contexts}}
	}
	return crd
}

// declaredTypes returns every type any input of the registries names, and two
// types no input names, sorted.
func declaredTypes(registries ...*Registry) []schema.GroupResource {
	set := map[schema.GroupResource]bool{
		{Group: "", Resource: "pods"}:                      true,
		{Group: "unknown.example.com", Resource: "things"}: true,
	}
	for _, r := range registries {
		addKeys(set, r.static)
		addKeys(set, r.crd)
		addKeys(set, r.policy)
	}
	return slices.SortedFunc(maps.Keys(set), func(a, b schema.GroupResource) int {
		return cmp.Compare(a.String(), b.String())
	})
}

func addKeys[V any](set map[schema.GroupResource]bool, inputs map[schema.GroupResource]V) {
	for gr := range inputs {
		set[gr] = true
	}
}

// TestNewRegistryDeclaresWhatTheAPIServerDeclared compares NewRegistry with the
// API server's inline registry twice: alone, so every built-in type's contexts
// show, and under every input, so a change of precedence shows.
func TestNewRegistryDeclaresWhatTheAPIServerDeclared(t *testing.T) {
	for _, tc := range []struct {
		name string
		feed bool
	}{{"built-ins alone", false}, {"under every input", true}} {
		t.Run(tc.name, func(t *testing.T) {
			want, got := inlineAPIServerRegistry(), NewRegistry()
			if tc.feed {
				feed(t, want)
				feed(t, got)
				requirePrecedenceFixture(t, want)
			}
			sameContexts(t, want, got)
		})
	}
}

// requirePrecedenceFixture fails the test if the fixture stops putting a CRD
// annotation above a built-in type, which would leave precedence unchecked.
func requirePrecedenceFixture(t *testing.T, r *Registry) {
	t.Helper()
	if contexts := r.AllowedContexts(passkeys); !slices.Equal(contexts, []ParentContext{ContextOrganization}) {
		t.Fatalf("the fixture no longer puts a CRD annotation above a built-in type: %v", contexts)
	}
}

func sameContexts(t *testing.T, want, got *Registry) {
	t.Helper()
	types := declaredTypes(want, got)
	for _, gr := range types {
		if w, g := want.AllowedContexts(gr), got.AllowedContexts(gr); !slices.Equal(g, w) {
			t.Errorf("%s: NewRegistry gives contexts %v, the API server's inline registry %v", gr, g, w)
		}
	}
	t.Logf("compared %d types", len(types))
}
