package features_test

import (
	"testing"

	utilfeature "k8s.io/apiserver/pkg/util/feature"

	"go.miloapis.com/milo/pkg/features"
)

func TestPasskeysFeatureGate_DefaultsToDisabled(t *testing.T) {
	if utilfeature.DefaultFeatureGate.Enabled(features.Passkeys) {
		t.Fatal("Passkeys feature gate must default to disabled (Alpha)")
	}
}
