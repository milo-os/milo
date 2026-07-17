package v1alpha1_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod not found)")
		}
		dir = parent
	}
}

func enumValues(t *testing.T, enum []apiextensionsv1.JSON) []string {
	t.Helper()
	out := make([]string, 0, len(enum))
	for _, e := range enum {
		var s string
		if err := json.Unmarshal(e.Raw, &s); err != nil {
			t.Fatalf("enum value not a string: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func TestUserStatusLastLoginProviderEnum(t *testing.T) {
	crdPath := filepath.Join(repoRoot(t), "config", "crd", "bases", "iam", "iam.miloapis.com_users.yaml")
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
	statusProp, ok := schema.Properties["status"]
	if !ok {
		t.Fatal("schema missing status property")
	}
	lastLoginProp, ok := statusProp.Properties["lastLoginProvider"]
	if !ok {
		t.Fatal("status schema missing lastLoginProvider property")
	}
	if len(lastLoginProp.AllOf) == 0 {
		t.Fatal("lastLoginProvider has no allOf enum constraints")
	}

	want := map[string]bool{"github": true, "google": true, "passkey": true, "email": true}
	for i, constraint := range lastLoginProp.AllOf {
		got := enumValues(t, constraint.Enum)
		if len(got) != len(want) {
			t.Errorf("allOf[%d] enum = %v, want 4 values matching %v", i, got, want)
			continue
		}
		for _, v := range got {
			if !want[v] {
				t.Errorf("allOf[%d] enum contains unexpected value %q", i, v)
			}
		}
	}
}
