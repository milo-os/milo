package app

import (
	"strings"
	"testing"

	identityopenapi "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// passthroughNamer mimics DefinitionNamer.GetDefinitionName as of Kubernetes
// 1.35, which returns the generated name verbatim.
func passthroughNamer(name string) (string, spec.Extensions) { return name, nil }

func TestRestFriendlyDefinitionNames(t *testing.T) {
	namer := restFriendlyDefinitionNames(passthroughNamer)

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "go import path is converted",
			in:   "go.miloapis.com/milo/pkg/apis/identity/v1alpha1.Passkey",
			want: "com.miloapis.go.milo.pkg.apis.identity.v1alpha1.Passkey",
		},
		{
			// ToRESTFriendlyName is not idempotent, so an already-friendly name
			// must be left untouched rather than reversed a second time.
			name: "already friendly name is untouched",
			in:   "io.k8s.api.core.v1.Pod",
			want: "io.k8s.api.core.v1.Pod",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := namer(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A definition name containing a slash is JSON-Pointer escaped to "~1" inside
// every $ref while the definitions key keeps the literal slash, which leaves the
// reference dangling and breaks client-side validation for every group this
// apiserver serves.
func TestIdentityDefinitionNamesCarryNoSlashes(t *testing.T) {
	namer := restFriendlyDefinitionNames(passthroughNamer)

	for name := range identityopenapi.GetOpenAPIDefinitions(spec.MustCreateRef) {
		served, _ := namer(name)
		if strings.Contains(served, "/") {
			t.Errorf("definition %q is served as %q, which contains a slash", name, served)
		}
	}
}

// Every type an identity definition depends on must itself be published, or the
// $ref pointing at it resolves to nothing.
func TestIdentityOpenAPIDefinitionsHaveNoDanglingDependencies(t *testing.T) {
	definitions := identityopenapi.GetOpenAPIDefinitions(spec.MustCreateRef)

	for name, definition := range definitions {
		for _, dependency := range definition.Dependencies {
			if !strings.HasPrefix(dependency, "go.miloapis.com/") {
				continue // published by the upstream Kubernetes definitions
			}
			if _, ok := definitions[dependency]; !ok {
				t.Errorf("definition %q depends on %q, which is not published", name, dependency)
			}
		}
	}
}
