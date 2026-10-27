package discovery

import (
	"os"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

var (
	notes        = schema.GroupResource{Group: "notes.miloapis.com", Resource: "notes"}
	clusterNotes = schema.GroupResource{Group: "notes.miloapis.com", Resource: "clusternotes"}
)

// shippedCRD reads one of the CRDs Milo ships in config/crd/bases/.
func shippedCRD(t *testing.T, path string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	raw, err := os.ReadFile("../../../config/crd/bases/" + path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal(raw, crd); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return crd
}

// TestShippedNoteDeclarations reads the notes CRDs and the notes policy as Milo ships them. A Note lives in a
// project: the portal creates it there, Milo's note webhook makes its subject its owner, and the project's quota
// counts it. So a project serves notes, and the platform keeps them. A ClusterNote stays a platform type. The policy
// overrides the CRD annotation, so each must say it alone.
func TestShippedNoteDeclarations(t *testing.T) {
	for name, withPolicy := range map[string]bool{"the CRD annotations alone": false, "the notes policy over them": true} {
		t.Run(name, func(t *testing.T) {
			r := newRegistry()
			r.upsertFromObj(shippedCRD(t, "notes/notes.miloapis.com_notes.yaml"))
			r.upsertFromObj(shippedCRD(t, "notes/notes.miloapis.com_clusternotes.yaml"))
			if withPolicy {
				r.upsertPolicyFromUnstructured(shippedPolicy(t, "notes"))
			}
			for _, c := range []struct {
				resource schema.GroupResource
				context  ParentContext
				want     bool
			}{
				{notes, ContextProject, true},
				{notes, ContextPlatform, true},
				{notes, ContextOrganization, false},
				{clusterNotes, ContextProject, false},
				{clusterNotes, ContextPlatform, true},
			} {
				if got := r.IsVisible(c.resource, c.context); got != c.want {
					t.Errorf("%s visible in %s = %v, want %v", c.resource, c.context, got, c.want)
				}
			}
		})
	}
}
