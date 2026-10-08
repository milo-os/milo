package v1alpha1_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	sigsyaml "sigs.k8s.io/yaml"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

// zitadel-provider writes this field by value and milo controllers read it by value.
// Renaming either value silently would split the two; this pins the wire values.
func TestEmailVerificationStateWireValues(t *testing.T) {
	if got := string(iamv1alpha1.EmailVerificationStateVerified); got != "Verified" {
		t.Errorf("EmailVerificationStateVerified = %q, want %q", got, "Verified")
	}
	if got := string(iamv1alpha1.EmailVerificationStateUnverified); got != "Unverified" {
		t.Errorf("EmailVerificationStateUnverified = %q, want %q", got, "Unverified")
	}
}

// The JSON tag is the wire contract for the status field and for the
// .status.emailVerification selectable field; omitempty is what lets an
// unsynced User omit the field rather than send an empty string.
func TestUserStatusEmailVerificationJSONTag(t *testing.T) {
	field, ok := reflect.TypeOf(iamv1alpha1.UserStatus{}).FieldByName("EmailVerification")
	if !ok {
		t.Fatal("UserStatus has no EmailVerification field")
	}
	if got, want := field.Tag.Get("json"), "emailVerification,omitempty"; got != want {
		t.Errorf("EmailVerification json tag = %q, want %q", got, want)
	}
	if got := field.Type; got != reflect.TypeOf(iamv1alpha1.EmailVerificationState("")) {
		t.Errorf("EmailVerification type = %s, want EmailVerificationState", got)
	}
}

// The field is optional: a User whose email verification state zitadel-provider has
// not synced yet must still validate, so the schema constrains the value but never
// requires it.
func TestUserStatusEmailVerificationSchema(t *testing.T) {
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
	version := crd.Spec.Versions[0]

	statusProp, ok := version.Schema.OpenAPIV3Schema.Properties["status"]
	if !ok {
		t.Fatal("schema missing status property")
	}
	emailVerificationProp, ok := statusProp.Properties["emailVerification"]
	if !ok {
		t.Fatal("status schema missing emailVerification property")
	}
	if emailVerificationProp.Type != "string" {
		t.Errorf("emailVerification type = %q, want %q", emailVerificationProp.Type, "string")
	}

	want := []string{"Verified", "Unverified"}
	if got := enumValues(t, emailVerificationProp.Enum); !reflect.DeepEqual(got, want) {
		t.Errorf("emailVerification enum = %v, want %v", got, want)
	}

	for _, required := range statusProp.Required {
		if required == "emailVerification" {
			t.Error("emailVerification is required; an unsynced User must be able to omit it")
		}
	}

	found := false
	for _, selectable := range version.SelectableFields {
		if selectable.JSONPath == ".status.emailVerification" {
			found = true
		}
	}
	if !found {
		t.Error("CRD does not expose .status.emailVerification as a selectable field")
	}
}
