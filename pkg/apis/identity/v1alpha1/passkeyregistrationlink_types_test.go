package v1alpha1_test

import (
	"testing"

	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	_ runtime.Object = &identityv1alpha1.PasskeyRegistrationLink{}
	_ runtime.Object = &identityv1alpha1.PasskeyRegistrationLinkList{}
)

func TestPasskeyRegistrationLinkDeepCopyIsIndependent(t *testing.T) {
	original := &identityv1alpha1.PasskeyRegistrationLink{
		Spec: identityv1alpha1.PasskeyRegistrationLinkSpec{
			UserRef:     identityv1alpha1.PasskeyRegistrationLinkUserReference{Name: "349828036672626689"},
			RequestedBy: "staff-1",
			Reason:      "ticket 1234",
		},
	}
	original.Name = "passkey-recovery-abc"
	dup := original.DeepCopy()
	dup.Spec.Reason = "changed"
	if original.Spec.Reason == dup.Spec.Reason {
		t.Fatal("DeepCopy did not produce an independent copy")
	}
}
