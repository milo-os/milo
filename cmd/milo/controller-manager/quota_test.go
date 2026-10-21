package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

func TestQuotaManagerSharesRefreshableRESTMapper(t *testing.T) {
	cfg := &rest.Config{Host: "https://example.invalid"}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sharedMapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))
	mgr, err := newQuotaManager(cfg, nil, logr.Discard(), sharedMapper)
	if err != nil {
		t.Fatal(err)
	}
	// Ownership takes its mapper from the local manager. Identity matters:
	// resetting the controller context's mapper must refresh this one too.
	if got := mgr.GetLocalManager().GetRESTMapper(); got != sharedMapper {
		t.Fatalf("quota manager mapper = %T, want the shared refreshable mapper", got)
	}
	if got := mgr.GetLocalManager().GetClient().RESTMapper(); got != sharedMapper {
		t.Fatalf("quota client mapper = %T, want the shared refreshable mapper", got)
	}
}

func TestQuotaManagerRediscoversAfterSharedMapperReset(t *testing.T) {
	var recovered atomic.Bool
	var discoveryRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json;g=apidiscovery.k8s.io;v=v2;as=APIGroupDiscoveryList")
		switch req.URL.Path {
		case "/api":
			fmt.Fprint(w, `{"kind":"APIGroupDiscoveryList","apiVersion":"apidiscovery.k8s.io/v2","items":[]}`)
		case "/apis":
			discoveryRequests.Add(1)
			freshness := "Stale"
			if recovered.Load() {
				freshness = "Current"
			}
			fmt.Fprintf(w, `{"kind":"APIGroupDiscoveryList","apiVersion":"apidiscovery.k8s.io/v2","items":[
				{"metadata":{"name":"quota.miloapis.com"},"versions":[{"version":"v1alpha1","freshness":"Current","resources":[{"resource":"resourceclaims","responseKind":{"kind":"ResourceClaim"},"scope":"Namespaced","verbs":["get"]}]}]},
				{"metadata":{"name":"ipam.miloapis.com"},"versions":[{"version":"v1alpha1","freshness":"%s","resources":[{"resource":"ipclaims","responseKind":{"kind":"IPClaim"},"scope":"Namespaced","verbs":["get"]}]}]}
			]}`, freshness)
		default:
			t.Errorf("unexpected discovery request: %s", req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)
	cfg := &rest.Config{Host: server.URL}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sharedMapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))
	mgr, err := newQuotaManager(cfg, nil, logr.Discard(), sharedMapper)
	if err != nil {
		t.Fatal(err)
	}
	managerMapper := mgr.GetLocalManager().GetRESTMapper()
	ownerKind := schema.GroupKind{Group: "ipam.miloapis.com", Kind: "IPClaim"}
	if _, err := managerMapper.RESTMapping(ownerKind); !meta.IsNoMatchError(err) {
		t.Fatalf("stale discovery: got %v, want NoMatch", err)
	}
	before := discoveryRequests.Load()
	recovered.Store(true)
	if _, err := managerMapper.RESTMapping(ownerKind); !meta.IsNoMatchError(err) {
		t.Fatalf("before refresh: got %v, want cached NoMatch", err)
	}
	if discoveryRequests.Load() != before {
		t.Fatal("API recovery alone should not invalidate cached discovery")
	}
	// Reset the controller context's mapper, as its periodic refresh loop does.
	// The manager must observe the refresh without being rebuilt or restarted.
	sharedMapper.Reset()
	mapping, err := managerMapper.RESTMapping(ownerKind)
	if err != nil {
		t.Fatalf("manager lookup after shared reset: %v", err)
	}
	wantResource := schema.GroupVersionResource{Group: "ipam.miloapis.com", Version: "v1alpha1", Resource: "ipclaims"}
	if mapping.Resource != wantResource || mapping.Scope.Name() != meta.RESTScopeNameNamespace {
		t.Fatalf("unexpected recovered mapping: %+v", mapping)
	}
	if discoveryRequests.Load() <= before {
		t.Fatal("expected manager lookup to fetch fresh discovery after shared reset")
	}
}
