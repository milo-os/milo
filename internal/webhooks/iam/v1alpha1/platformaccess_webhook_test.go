package v1alpha1

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPlatformAccessMutator_Default(t *testing.T) {
	testUser := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user",
		},
		Spec: iamv1alpha1.UserSpec{
			Email: "test@example.com",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(testUser).Build()
	mutator := &PlatformAccessMutator{client: fakeClient, scheme: runtimeScheme}

	pa := &iamv1alpha1.PlatformAccess{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user-access",
		},
		Spec: iamv1alpha1.PlatformAccessSpec{
			UserRef: iamv1alpha1.UserReference{
				Name: "test-user",
			},
			State: iamv1alpha1.PlatformAccessStatePending,
		},
	}

	err := mutator.Default(context.Background(), pa)
	require.NoError(t, err)

	// Ensure owner reference is set
	if assert.Len(t, pa.OwnerReferences, 1, "expected a single owner reference to be set") {
		ref := pa.OwnerReferences[0]
		assert.Equal(t, "User", ref.Kind)
		assert.Equal(t, "test-user", ref.Name)
	}
}

func TestPlatformAccessValidator_ValidateCreate(t *testing.T) {
	validUser := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "valid-user",
		},
		Spec: iamv1alpha1.UserSpec{
			Email: "valid@example.com",
		},
	}

	tests := map[string]struct {
		pa           *iamv1alpha1.PlatformAccess
		preObjects   []client.Object
		expectError  bool
		errSubstring string
	}{
		"valid user reference": {
			pa: &iamv1alpha1.PlatformAccess{
				ObjectMeta: metav1.ObjectMeta{Name: "valid-pa"},
				Spec: iamv1alpha1.PlatformAccessSpec{
					UserRef: iamv1alpha1.UserReference{Name: "valid-user"},
					State:   iamv1alpha1.PlatformAccessStatePending,
				},
			},
			preObjects:  []client.Object{validUser},
			expectError: false,
		},
		"user not found": {
			pa: &iamv1alpha1.PlatformAccess{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-pa"},
				Spec: iamv1alpha1.PlatformAccessSpec{
					UserRef: iamv1alpha1.UserReference{Name: "non-existent-user"},
					State:   iamv1alpha1.PlatformAccessStatePending,
				},
			},
			expectError:  true,
			errSubstring: "not found",
		},
		"missing username": {
			pa: &iamv1alpha1.PlatformAccess{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-pa"},
				Spec: iamv1alpha1.PlatformAccessSpec{
					UserRef: iamv1alpha1.UserReference{Name: ""},
					State:   iamv1alpha1.PlatformAccessStatePending,
				},
			},
			expectError:  true,
			errSubstring: "userRef.name is required",
		},
		"duplicate platformaccess for same user": {
			pa: &iamv1alpha1.PlatformAccess{
				ObjectMeta: metav1.ObjectMeta{Name: "new-pa"},
				Spec: iamv1alpha1.PlatformAccessSpec{
					UserRef: iamv1alpha1.UserReference{Name: "valid-user"},
					State:   iamv1alpha1.PlatformAccessStatePending,
				},
			},
			preObjects: []client.Object{
				validUser,
				&iamv1alpha1.PlatformAccess{
					ObjectMeta: metav1.ObjectMeta{Name: "existing-pa"},
					Spec: iamv1alpha1.PlatformAccessSpec{
						UserRef: iamv1alpha1.UserReference{Name: "valid-user"},
						State:   iamv1alpha1.PlatformAccessStatePending,
					},
				},
			},
			expectError:  true,
			errSubstring: "already exists",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(runtimeScheme)
			if len(tc.preObjects) > 0 {
				builder = builder.WithObjects(tc.preObjects...)
			}
			builder = builder.WithIndex(&iamv1alpha1.PlatformAccess{}, platformAccessUserIndexKey, func(rawObj client.Object) []string {
				pa := rawObj.(*iamv1alpha1.PlatformAccess)
				return []string{pa.Spec.UserRef.Name}
			})
			cl := builder.Build()
			validator := &PlatformAccessValidator{client: cl}

			_, err := validator.ValidateCreate(context.Background(), tc.pa)
			if tc.expectError {
				require.Error(t, err)
				if tc.errSubstring != "" {
					assert.Contains(t, strings.ToLower(err.Error()), strings.ToLower(tc.errSubstring))
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPlatformAccessValidator_ValidateDelete(t *testing.T) {
	testUser := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user",
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(testUser).Build()
	validator := &PlatformAccessValidator{client: fakeClient}

	pa := &iamv1alpha1.PlatformAccess{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user-access",
		},
		Spec: iamv1alpha1.PlatformAccessSpec{
			UserRef: iamv1alpha1.UserReference{
				Name: "test-user",
			},
		},
	}

	_, err := validator.ValidateDelete(context.Background(), pa)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deletion of PlatformAccess resources is not allowed")
}
