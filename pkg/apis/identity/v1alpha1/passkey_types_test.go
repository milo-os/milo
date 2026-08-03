package v1alpha1_test

import (
	"testing"

	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	_ runtime.Object = &identityv1alpha1.Passkey{}
	_ runtime.Object = &identityv1alpha1.PasskeyList{}
)

func TestPasskeyDeepCopyIsIndependent(t *testing.T) {
	original := &identityv1alpha1.Passkey{
		Status: identityv1alpha1.PasskeyStatus{
			UserUID:     "user-1",
			DisplayName: "MacBook Touch ID",
			State:       identityv1alpha1.PasskeyStateActive,
		},
	}
	original.Name = "passkey-123"

	dup := original.DeepCopy()
	dup.Status.DisplayName = "Renamed"

	if original.Status.DisplayName == dup.Status.DisplayName {
		t.Fatal("DeepCopy did not produce an independent copy")
	}
	if dup.Name != "passkey-123" {
		t.Fatalf("Name = %q, want %q", dup.Name, "passkey-123")
	}
}
