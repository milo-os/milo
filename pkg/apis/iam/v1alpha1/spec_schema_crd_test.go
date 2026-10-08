package v1alpha1_test

import (
	"os"
	"path/filepath"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// CRD defaulting fills a nested default only when the parent object is
// present. Each kind below therefore either defaults spec to {} or requires
// spec outright, and the nested defaults must survive regeneration.
func TestSpecSchemaHandlesAbsentSpec(t *testing.T) {
	cases := []struct {
		kind           string
		crdFile        string
		wantSpecReq    bool
		wantSpecDef    string
		nestedDefaults map[string]string
	}{
		{
			kind:        "ServiceAccount",
			crdFile:     "iam.miloapis.com_serviceaccounts.yaml",
			wantSpecDef: "{}",
			nestedDefaults: map[string]string{
				"state": `"Active"`,
			},
		},
		{
			kind:        "PlatformAccess",
			crdFile:     "iam.miloapis.com_platformaccesses.yaml",
			wantSpecReq: true,
			nestedDefaults: map[string]string{
				"state": `"Pending"`,
			},
		},
		{
			kind:        "UserPreference",
			crdFile:     "iam.miloapis.com_userpreferences.yaml",
			wantSpecReq: true,
			nestedDefaults: map[string]string{
				"theme": `"system"`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			crdPath := filepath.Join(repoRoot(t), "config", "crd", "bases", "iam", tc.crdFile)
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

			schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
			specProp, ok := schema.Properties["spec"]
			if !ok {
				t.Fatal("schema missing spec property")
			}

			specRequired := false
			for _, req := range schema.Required {
				if req == "spec" {
					specRequired = true
				}
			}
			if specRequired != tc.wantSpecReq {
				t.Errorf("spec required = %v, want %v (schema.required = %v)", specRequired, tc.wantSpecReq, schema.Required)
			}

			gotSpecDef := ""
			if specProp.Default != nil {
				gotSpecDef = string(specProp.Default.Raw)
			}
			if gotSpecDef != tc.wantSpecDef {
				t.Errorf("spec default = %q, want %q", gotSpecDef, tc.wantSpecDef)
			}

			if !specRequired && gotSpecDef == "" {
				t.Errorf("spec is neither required nor defaulted; an absent spec would never apply nested defaults")
			}

			for field, want := range tc.nestedDefaults {
				prop, ok := specProp.Properties[field]
				if !ok {
					t.Errorf("spec schema missing %s property", field)
					continue
				}
				if prop.Default == nil {
					t.Errorf("spec.%s has no default, want %s", field, want)
					continue
				}
				if got := string(prop.Default.Raw); got != want {
					t.Errorf("spec.%s default = %s, want %s", field, got, want)
				}
			}
		})
	}
}
