package lifecycle

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

type ownershipDiscoveryTransport struct {
	t          *testing.T
	fresh      bool
	apiReads   int
	ownerReads int
}

func (f *ownershipDiscoveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Helper()
	contentType := "application/json;g=apidiscovery.k8s.io;v=v2;as=APIGroupDiscoveryList"
	var body string
	switch req.URL.Path {
	case "/api":
		body = `{"kind":"APIGroupDiscoveryList","apiVersion":"apidiscovery.k8s.io/v2","items":[]}`
	case "/apis":
		f.apiReads++
		freshness := "Stale"
		if f.fresh {
			freshness = "Current"
		}
		body = `{"kind":"APIGroupDiscoveryList","apiVersion":"apidiscovery.k8s.io/v2","items":[
			{"metadata":{"name":"quota.miloapis.com"},"versions":[{"version":"v1alpha1","freshness":"Current","resources":[{"resource":"resourceclaims","responseKind":{"kind":"ResourceClaim"},"scope":"Namespaced","verbs":["get","list","watch"]}]}]},
			{"metadata":{"name":"ipam.miloapis.com"},"versions":[{"version":"v1alpha1","freshness":"` + freshness + `","resources":[{"resource":"ipclaims","responseKind":{"kind":"IPClaim"},"scope":"Namespaced","verbs":["get"]}]}]}
		]}`
	case "/apis/ipam.miloapis.com/v1alpha1/namespaces/default/ipclaims/test-ip":
		f.ownerReads++
		contentType = "application/json"
		body = `{"apiVersion":"ipam.miloapis.com/v1alpha1","kind":"IPClaim","metadata":{"name":"test-ip","namespace":"default","uid":"ip-owner-uid"}}`
	default:
		f.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

type ownershipTestCluster struct {
	*testCluster
	config *rest.Config
}

func (c *ownershipTestCluster) GetConfig() *rest.Config { return c.config }

func TestOwnershipRecoversAfterStaleDiscoveryRefresh(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)
	claim := autoClaim("ip-quota", "default", "True", quotav1alpha1.ResourceClaimGrantedReason, time.Now().Add(-time.Hour))
	claim.Spec.ResourceRef = &quotav1alpha1.UnversionedObjectReference{
		APIGroup: "ipam.miloapis.com", Kind: "IPClaim", Namespace: "default", Name: "test-ip",
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(claim).Build()
	transport := &ownershipDiscoveryTransport{t: t}
	cfg := &rest.Config{Host: "https://example.invalid", Transport: transport}
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))
	// Startup discovers quota successfully while IPAM's only version is stale.
	if _, err := mapper.RESTMapping(schema.GroupKind{Group: "quota.miloapis.com", Kind: "ResourceClaim"}); err != nil {
		t.Fatal(err)
	}
	r := &ResourceClaimOwnershipController{
		Scheme: scheme,
		Manager: &testManager{cluster: &ownershipTestCluster{
			testCluster: &testCluster{client: cli}, config: cfg,
		}},
		restMapper: mapper,
	}
	req := mcreconcile.Request{Request: ctrl.Request{NamespacedName: types.NamespacedName{Name: claim.Name, Namespace: claim.Namespace}}}
	if _, err := r.Reconcile(ctx, req); !meta.IsNoMatchError(err) {
		t.Fatalf("stale discovery: got %v, want NoMatch", err)
	}
	var got quotav1alpha1.ResourceClaim
	if err := cli.Get(ctx, client.ObjectKeyFromObject(claim), &got); err != nil {
		t.Fatalf("discovery failure must preserve an old claim: %v", err)
	}
	if len(got.OwnerReferences) != 0 || transport.ownerReads != 0 {
		t.Fatal("stale discovery should not resolve or attach an owner")
	}
	before := transport.apiReads
	transport.fresh = true
	if _, err := r.Reconcile(ctx, req); !meta.IsNoMatchError(err) {
		t.Fatalf("cached stale discovery: got %v, want NoMatch until refresh", err)
	}
	if transport.apiReads != before || transport.ownerReads != 0 {
		t.Fatal("cached stale discovery should not make new discovery or owner requests")
	}
	mapper.Reset() // The existing controller-context refresh loop does this.
	result, err := r.Reconcile(ctx, req)
	if err != nil || result != (ctrl.Result{}) {
		t.Fatalf("after discovery recovery: result=%+v, err=%v", result, err)
	}
	if transport.apiReads <= before || transport.ownerReads != 1 {
		t.Fatalf("expected fresh discovery and one owner GET: discovery=%d (before %d), owner=%d", transport.apiReads, before, transport.ownerReads)
	}
	if err := cli.Get(ctx, client.ObjectKeyFromObject(claim), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.OwnerReferences) != 1 {
		t.Fatalf("expected one owner reference, got %+v", got.OwnerReferences)
	}
	want := metav1.OwnerReference{APIVersion: "ipam.miloapis.com/v1alpha1", Kind: "IPClaim", Name: "test-ip", UID: "ip-owner-uid"}
	owner := got.OwnerReferences[0]
	if owner.APIVersion != want.APIVersion || owner.Kind != want.Kind || owner.Name != want.Name || owner.UID != want.UID {
		t.Fatalf("owner = %+v, want %+v", owner, want)
	}
}
