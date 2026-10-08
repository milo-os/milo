// SPDX-License-Identifier: AGPL-3.0-only
package v1alpha1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidateAssignableRole(t *testing.T) {
	const assignableNS = "datum-cloud"

	existing := assignableRole() // Role "viewer" in datum-cloud
	cl := fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(existing).Build()

	refPath := field.NewPath("spec", "roleRef")

	tests := map[string]struct {
		name           string
		namespace      string
		expectError    bool
		field          string
		expectedBadVal string
	}{
		"an existing role in the assignable namespace is valid": {
			name:        "viewer",
			namespace:   assignableNS,
			expectError: false,
		},
		"a role in a non-assignable namespace is rejected": {
			name:           "viewer",
			namespace:      "organization-acme",
			expectError:    true,
			field:          "spec.roleRef.namespace",
			expectedBadVal: "organization-acme",
		},
		"an empty namespace is rejected (must name the assignable namespace)": {
			name:           "viewer",
			namespace:      "",
			expectError:    true,
			field:          "spec.roleRef.namespace",
			expectedBadVal: "",
		},
		"a non-existent role in the assignable namespace is rejected": {
			name:           "does-not-exist",
			namespace:      assignableNS,
			expectError:    true,
			field:          "spec.roleRef.name",
			expectedBadVal: "datum-cloud/does-not-exist",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			errs := validateAssignableRole(context.Background(), cl, tc.name, tc.namespace, assignableNS, refPath)

			if !tc.expectError {
				assert.Empty(t, errs)
				return
			}

			require.NotEmpty(t, errs)
			assert.Equal(t, tc.field, errs[0].Field)
			assert.Equal(t, tc.expectedBadVal, errs[0].BadValue)
		})
	}
}
