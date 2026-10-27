package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

// TestUnservedRequestCounterReadsTheShippedPolicy runs the registry's two informers against a test API server, as the
// API server runs them, holding Milo's core DiscoveryContextPolicy: the counter counts a project's roles, which the
// policy keeps at the platform, and not its leases, which the policy widens to projects; then a CRD tagged
// Organization, once its informer sees it.
func TestUnservedRequestCounterReadsTheShippedPolicy(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("KUBEBUILDER_ASSETS must be set in CI; this test needs an envtest API server")
		}
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
	applyPolicyFile(t, ctx, dynamic.NewForConfigOrDie(cfg), filepath.Join(root, "discovery", "core", "discovery-context-policy.yaml"))

	registry := NewRegistry()
	factory := apiextensionsinformers.NewSharedInformerFactory(apiextensionsclient.NewForConfigOrDie(cfg), 0)
	factory.Apiextensions().V1().CustomResourceDefinitions().Informer()
	go func() { _ = registry.Run(ctx, factory) }()
	factory.Start(ctx.Done())
	go func() { _ = registry.RunPolicyInformer(ctx, cfg) }()
	waitFor(t, "the registry syncs", registry.HasSynced)

	unservedRequests.Reset()
	chain := NewFilters(registry, false)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	create := func(gr schema.GroupResource) {
		req := httptest.NewRequest(http.MethodPost, apiPath(gr, "/namespaces/default/"+gr.Resource), nil)
		info := &genericapirequest.RequestInfo{IsResourceRequest: true, Verb: "create",
			APIGroup: gr.Group, APIVersion: "v1", Namespace: "default", Resource: gr.Resource}
		chain.ServeHTTP(httptest.NewRecorder(), req.WithContext(genericapirequest.WithRequestInfo(inProject(req.Context()), info)))
	}
	create(rolesGR)
	create(leasesGR)
	create(schema.GroupResource{Resource: "configmaps"})
	wantCounted(t, map[string]float64{"create rbac.authorization.k8s.io roles": 1})

	createOrganizationCRD(t, ctx, apiextensionsclient.NewForConfigOrDie(cfg), widgetGR)
	waitFor(t, "the registry reads the CRD", func() bool { return !registry.IsVisible(widgetGR, ContextProject) })
	create(widgetGR)
	wantCounted(t, map[string]float64{"create rbac.authorization.k8s.io roles": 1, "create example.com widgets": 1})
}

func applyPolicyFile(t *testing.T, ctx context.Context, client dynamic.Interface, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var policy unstructured.Unstructured
	if err := yaml.Unmarshal(raw, &policy.Object); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Resource(discoveryContextPolicyGVR).Create(ctx, &policy, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
}

func createOrganizationCRD(t *testing.T, ctx context.Context, client apiextensionsclient.Interface, gr schema.GroupResource) {
	t.Helper()
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:        gr.Resource + "." + gr.Group,
			Annotations: map[string]string{ParentContextsAnnotation: string(ContextOrganization)},
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: gr.Group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: gr.Resource, Kind: "Widget"},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v1", Served: true, Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object", XPreserveUnknownFields: ptr.To(true),
				}},
			}},
		},
	}
	if _, err := client.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create CRD %s: %v", crd.Name, err)
	}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	if err := wait.PollUntilContextTimeout(context.Background(), 50*time.Millisecond, 30*time.Second, true,
		func(context.Context) (bool, error) { return condition(), nil }); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}
