package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"

	discoveryctx "go.miloapis.com/milo/pkg/server/discovery"
)

// TestStartServedTypes starts the controller manager's registry against a test API server holding Milo's
// DiscoveryContextPolicy CRD and its notes policy (milo#834): it syncs, reads a policy, a CRD annotation and a
// built-in type as the API server's registry does, and follows a CRD created after it synced.
func TestStartServedTypes(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set; run setup-envtest to provide an envtest API server")
	}
	root := filepath.Join("..", "..", "..", "config")
	env := &envtest.Environment{CRDInstallOptions: envtest.CRDInstallOptions{Paths: []string{
		filepath.Join(root, "crd", "bases", "discovery", "discovery.miloapis.com_discoverycontextpolicies.yaml"),
	}}}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	applyPolicy(t, ctx, dynamic.NewForConfigOrDie(cfg), filepath.Join(root, "discovery", "notes", "discovery-context-policy.yaml"))
	crds := apiextensionsclient.NewForConfigOrDie(cfg).ApiextensionsV1().CustomResourceDefinitions()
	createCRD(t, ctx, crds, "gadgets", "Platform")
	createCRD(t, ctx, crds, "doohickeys", "")

	registry, err := startServedTypes(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the registry syncs", registry.HasSynced)

	served := func(group, resource string) bool {
		return registry.IsVisible(schema.GroupResource{Group: group, Resource: resource}, discoveryctx.ContextProject)
	}
	for _, c := range []struct {
		group, resource string
		want            bool
	}{
		{"notes.miloapis.com", "notes", false},       // a policy rule
		{"example.com", "gadgets", false},            // a CRD annotation
		{"example.com", "doohickeys", true},          // an untagged CRD
		{"identity.miloapis.com", "sessions", false}, // a built-in type
	} {
		if got := served(c.group, c.resource); got != c.want {
			t.Errorf("served in a project %s.%s = %v, want %v", c.resource, c.group, got, c.want)
		}
	}

	createCRD(t, ctx, crds, "thingamajigs", "Platform")
	eventually(t, "a CRD created after the sync is read", func() bool { return !served("example.com", "thingamajigs") })
}

func applyPolicy(t *testing.T, ctx context.Context, client dynamic.Interface, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	policy := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(data, &policy.Object); err != nil {
		t.Fatal(err)
	}
	gvr := schema.GroupVersionResource{Group: "discovery.miloapis.com", Version: "v1alpha1", Resource: "discoverycontextpolicies"}
	if _, err := client.Resource(gvr).Create(ctx, policy, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
}

// createCRD creates example.com's resource with the parent-contexts annotation, or none when contexts is empty.
func createCRD(t *testing.T, ctx context.Context, crds interface {
	Create(context.Context, *apiextensionsv1.CustomResourceDefinition, metav1.CreateOptions) (*apiextensionsv1.CustomResourceDefinition, error)
}, plural, contexts string) {
	t.Helper()
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: plural + ".example.com"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: plural, Kind: plural + "Kind"},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{Name: "v1", Served: true, Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"}}}},
		},
	}
	if contexts != "" {
		crd.Annotations = map[string]string{discoveryctx.ParentContextsAnnotation: contexts}
	}
	if _, err := crds.Create(ctx, crd, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	if err := wait.PollUntilContextTimeout(context.Background(), 50*time.Millisecond, wait.ForeverTestTimeout, true,
		func(context.Context) (bool, error) { return condition(), nil }); err != nil {
		t.Fatalf("%s: not within %v", what, wait.ForeverTestTimeout)
	}
}
