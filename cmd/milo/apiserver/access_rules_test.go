package app

import (
	"context"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metadatafake "k8s.io/client-go/metadata/fake"
)

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
