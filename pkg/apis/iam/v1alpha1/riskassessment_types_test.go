package v1alpha1_test

import (
	"os"
	"path/filepath"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

func loadIAMCRDVersion(t *testing.T, file string) apiextensionsv1.CustomResourceDefinitionVersion {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "config", "crd", "bases", "iam", file))
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
	return crd.Spec.Versions[0]
}

// assertRiskAssessmentSchema checks the riskAssessment property under parent.
// The fraud service writes these field names and the staff portal reads them,
// so the wire names are a contract; every field must stay optional so that
// existing objects without an assessment keep validating.
func assertRiskAssessmentSchema(t *testing.T, parent apiextensionsv1.JSONSchemaProps, parentName string) {
	t.Helper()
	ra, ok := parent.Properties["riskAssessment"]
	if !ok {
		t.Fatalf("%s schema missing riskAssessment property", parentName)
	}
	for _, r := range parent.Required {
		if r == "riskAssessment" {
			t.Errorf("%s.riskAssessment is required; it must be optional", parentName)
		}
	}
	if ra.Type != "object" {
		t.Errorf("%s.riskAssessment type = %q, want object", parentName, ra.Type)
	}
	want := map[string]string{
		"score":          "string",
		"decision":       "string",
		"evaluatedAt":    "string",
		"evaluationName": "string",
	}
	for name, typ := range want {
		prop, ok := ra.Properties[name]
		if !ok {
			t.Errorf("%s.riskAssessment missing %s", parentName, name)
			continue
		}
		if prop.Type != typ {
			t.Errorf("%s.riskAssessment.%s type = %q, want %q", parentName, name, prop.Type, typ)
		}
	}
	if len(ra.Required) != 0 {
		t.Errorf("%s.riskAssessment required = %v, want none", parentName, ra.Required)
	}
}

func TestPlatformAccessSpecRiskAssessmentSchema(t *testing.T) {
	version := loadIAMCRDVersion(t, "iam.miloapis.com_platformaccesses.yaml")
	spec, ok := version.Schema.OpenAPIV3Schema.Properties["spec"]
	if !ok {
		t.Fatal("schema missing spec property")
	}
	assertRiskAssessmentSchema(t, spec, "spec")
}

func TestUserStatusRiskAssessmentSchema(t *testing.T) {
	version := loadIAMCRDVersion(t, "iam.miloapis.com_users.yaml")
	status, ok := version.Schema.OpenAPIV3Schema.Properties["status"]
	if !ok {
		t.Fatal("schema missing status property")
	}
	assertRiskAssessmentSchema(t, status, "status")

	found := false
	for _, selectable := range version.SelectableFields {
		if selectable.JSONPath == ".status.riskAssessment.decision" {
			found = true
		}
	}
	if !found {
		t.Error("CRD does not expose .status.riskAssessment.decision as a selectable field")
	}
}
