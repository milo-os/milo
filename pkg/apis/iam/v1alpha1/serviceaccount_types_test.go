package v1alpha1_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	sigsyaml "sigs.k8s.io/yaml"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

func TestServiceAccountStatusClientIDJSON(t *testing.T) {
	account := iamv1alpha1.ServiceAccount{
		Status: iamv1alpha1.ServiceAccountStatus{
			ClientID: "349624629",
		},
	}

	encoded, err := json.Marshal(account)
	if err != nil {
		t.Fatalf("marshal ServiceAccount: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal ServiceAccount JSON: %v", err)
	}
	status, ok := payload["status"].(map[string]any)
	if !ok {
		t.Fatalf("status is missing or not an object: %s", encoded)
	}
	if got, want := status["clientID"], "349624629"; got != want {
		t.Fatalf("clientID = %v, want %q", got, want)
	}
}

func TestServiceAccountStatusClientIDSchema(t *testing.T) {
	crdPath := filepath.Join(repoRoot(t), "config", "crd", "bases", "iam", "iam.miloapis.com_serviceaccounts.yaml")
	raw, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("reading CRD: %v", err)
	}

	var crd apiextensionsv1.CustomResourceDefinition
	if err := sigsyaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("unmarshaling CRD: %v", err)
	}
	if len(crd.Spec.Versions) == 0 {
		t.Fatal("CRD has no versions")
	}

	status, ok := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"]
	if !ok {
		t.Fatal("schema missing status property")
	}
	clientID, ok := status.Properties["clientID"]
	if !ok {
		t.Fatal("status schema missing clientID property")
	}
	if clientID.Type != "string" {
		t.Errorf("clientID type = %q, want %q", clientID.Type, "string")
	}
	for _, required := range status.Required {
		if required == "clientID" {
			t.Error("clientID must remain optional while providers roll out support")
		}
	}
}
