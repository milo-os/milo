package app

import (
	"context"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/admission"
	metadatafake "k8s.io/client-go/metadata/fake"
	"k8s.io/client-go/rest"

	"go.miloapis.com/milo/internal/apiserver/admission/plugin/accessowner"
	"go.miloapis.com/milo/pkg/server/access"
)

// TestMiloInitializersSetAccessRules checks the initializers every server
// gets give AccessRuleOwnerEnforcement the access rules. Without them the
// plugin fails its initialization check and the server doesn't start.
func TestMiloInitializersSetAccessRules(t *testing.T) {
	plugin := accessowner.NewPlugin()
	admission.PluginInitializers(miloInitializers(&rest.Config{}, nil, access.NewRules())).Initialize(plugin)
	if err := plugin.ValidateInitialization(); err != nil {
		t.Errorf("AccessRuleOwnerEnforcement after Milo's initializers: %v", err)
	}
}

func TestCRDVersions(t *testing.T) {
	crd := func(name, rv string) *metav1.PartialObjectMetadata {
		return &metav1.PartialObjectMetadata{
			TypeMeta:   metav1.TypeMeta{APIVersion: apiextensionsv1.SchemeGroupVersion.String(), Kind: "CustomResourceDefinition"},
			ObjectMeta: metav1.ObjectMeta{Name: name, ResourceVersion: rv},
		}
	}
	scheme := metadatafake.NewTestScheme()
	if err := metav1.AddMetaToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	client := metadatafake.NewSimpleMetadataClient(scheme, crd("votes.example.com", "7"), crd("posts.example.com", "9"))
	got, err := crdVersions(client)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["votes.example.com"] != "7" || got["posts.example.com"] != "9" {
		t.Errorf("versions = %v", got)
	}
}
