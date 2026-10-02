package v1alpha1_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/listtype"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	sigsyaml "sigs.k8s.io/yaml"
)

// Exercise the generated schema using the API server's validators so malformed
// or ambiguous permission declarations cannot reach the authorization provider.
func TestProtectedResourceSubresourceValidation(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "config", "crd", "bases", "iam", "iam.miloapis.com_protectedresources.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := sigsyaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	var internalSchema apiextensions.JSONSchemaProps
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(crd.Spec.Versions[0].Schema.OpenAPIV3Schema, &internalSchema, nil); err != nil {
		t.Fatal(err)
	}
	validator, _, err := validation.NewSchemaValidator(&internalSchema)
	if err != nil {
		t.Fatal(err)
	}
	structural, err := schema.NewStructural(&internalSchema)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name         string
		subresources string
		wantError    bool
	}{
		{name: "existing registration"},
		{name: "empty optional list", subresources: `[]`},
		{name: "explicit verbs", subresources: `[{"name":"status","permissions":["patch","update"]},{"name":"scale","permissions":["get"]}]`},
		{name: "missing name", subresources: `[{"permissions":["patch"]}]`, wantError: true},
		{name: "empty name", subresources: `[{"name":"","permissions":["patch"]}]`, wantError: true},
		{name: "nested subresource", subresources: `[{"name":"status/nested","permissions":["patch"]}]`, wantError: true},
		{name: "dot in name", subresources: `[{"name":"status.patch","permissions":["patch"]}]`, wantError: true},
		{name: "uppercase name", subresources: `[{"name":"Status","permissions":["patch"]}]`, wantError: true},
		{name: "long name", subresources: `[{"name":"` + strings.Repeat("s", 64) + `","permissions":["patch"]}]`, wantError: true},
		{name: "duplicate names", subresources: `[{"name":"status","permissions":["patch"]},{"name":"status","permissions":["update"]}]`, wantError: true},
		{name: "missing permissions", subresources: `[{"name":"status"}]`, wantError: true},
		{name: "empty permissions", subresources: `[{"name":"status","permissions":[]}]`, wantError: true},
		{name: "duplicate verbs", subresources: `[{"name":"status","permissions":["patch","patch"]}]`, wantError: true},
		{name: "empty verb", subresources: `[{"name":"status","permissions":[""]}]`, wantError: true},
		{name: "dot in verb", subresources: `[{"name":"status","permissions":["status.patch"]}]`, wantError: true},
		{name: "slash in verb", subresources: `[{"name":"status","permissions":["status/patch"]}]`, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := map[string]interface{}{
				"serviceRef": map[string]interface{}{"name": "example.com"},
				"kind":       "Widget", "singular": "widget", "plural": "widgets",
				"permissions": []interface{}{"get", "patch", "update"},
			}
			if tc.subresources != "" {
				var subresources interface{}
				if err := json.Unmarshal([]byte(tc.subresources), &subresources); err != nil {
					t.Fatal(err)
				}
				spec["subresources"] = subresources
			}
			obj := map[string]interface{}{"spec": spec}
			errs := validation.ValidateCustomResource(nil, obj, validator)
			errs = append(errs, listtype.ValidateListSetsAndMaps(nil, structural, obj)...)
			if (len(errs) > 0) != tc.wantError {
				t.Fatalf("validation errors = %v, want error: %v", errs, tc.wantError)
			}
		})
	}
}
