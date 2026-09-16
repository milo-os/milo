package policy

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

// A parent context the resolver cannot build a client for must surface as an
// error. Before, it silently handed back the local client, so the grant was
// written to the wrong control plane and the failure never reached an event.
func TestResolveClientUnsupportedParentContextIsAnError(t *testing.T) {
	resolver := NewParentContextResolver(&rest.Config{Host: "https://milo.example"}, testScheme(), ParentContextResolverOptions{})
	t.Cleanup(resolver.Close)

	trigger := &unstructured.Unstructured{}
	trigger.SetName("project-a")

	c, err := resolver.ResolveClient(context.Background(), &ParentContextSpec{
		APIGroup: "resourcemanager.miloapis.com",
		Kind:     "Organization",
		Name:     "org-a",
	}, trigger)
	if err == nil {
		t.Fatal("ResolveClient returned no error for an unsupported parent context")
	}
	if c != nil {
		t.Fatalf("ResolveClient returned a client %T alongside the error", c)
	}
	if !strings.Contains(err.Error(), "unsupported parent context type") {
		t.Fatalf("err = %v, want the validation cause preserved", err)
	}
}
