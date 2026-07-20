package identity_test

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	identityapi "go.miloapis.com/milo/pkg/apis/identity"
	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"
)

func TestInstall_PasskeyFieldSelectorParity(t *testing.T) {
	scheme := runtime.NewScheme()
	identityapi.Install(scheme)

	gvk := schema.GroupVersionKind{
		Group:   identityv1alpha1.SchemeGroupVersion.Group,
		Version: identityv1alpha1.SchemeGroupVersion.Version,
		Kind:    "Passkey",
	}

	label, value, err := scheme.ConvertFieldLabel(gvk, "status.userUID", "user-123")
	if err != nil {
		t.Fatalf("status.userUID should be an accepted Passkey selector: %v", err)
	}
	if label != "status.userUID" || value != "user-123" {
		t.Fatalf("got (%q, %q), want (\"status.userUID\", \"user-123\")", label, value)
	}

	// A label the conversion func doesn't recognize is passed through as
	// ("", "", nil) by the shared userScopedSelector closure — mirrors the
	// exact behavior already relied on for Session/UserIdentity.
	label, value, err = scheme.ConvertFieldLabel(gvk, "status.displayName", "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if label != "" || value != "" {
		t.Fatalf("status.displayName should not be recognized as a Passkey selector, got (%q, %q)", label, value)
	}
}
