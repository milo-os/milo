package app

import (
	"testing"

	"github.com/go-logr/logr"
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
