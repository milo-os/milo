package v1alpha1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newGroupMembership(annotations map[string]string) *iamv1alpha1.GroupMembership {
	return &iamv1alpha1.GroupMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "membership",
			Namespace:   "org-ns",
			Annotations: annotations,
		},
		Spec: iamv1alpha1.GroupMembershipSpec{
			UserRef:  iamv1alpha1.UserReference{Name: "member-user"},
			GroupRef: iamv1alpha1.GroupReference{Name: "team", Namespace: "org-ns"},
		},
	}
}

// TestGroupMembershipMutator_Default verifies the member's email is stamped
// as an annotation when the referenced User exists, and that the object is
// left untouched when it does not.
func TestGroupMembershipMutator_Default(t *testing.T) {
	memberUser := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "member-user"},
		Spec:       iamv1alpha1.UserSpec{Email: "member@example.com"},
	}
	noEmailUser := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "member-user"},
	}

	tests := map[string]struct {
		preObjects          []client.Object
		annotations         map[string]string
		expectedAnnotations map[string]string
	}{
		"email stamped when user exists": {
			preObjects: []client.Object{memberUser},
			expectedAnnotations: map[string]string{
				iamv1alpha1.UserEmailAnnotation: "member@example.com",
			},
		},
		"no annotation and no error when user is missing": {
			expectedAnnotations: nil,
		},
		"existing annotations preserved": {
			preObjects:  []client.Object{memberUser},
			annotations: map[string]string{"example.com/keep": "yes"},
			expectedAnnotations: map[string]string{
				"example.com/keep":              "yes",
				iamv1alpha1.UserEmailAnnotation: "member@example.com",
			},
		},
		"stale email annotation refreshed from the user": {
			preObjects:  []client.Object{memberUser},
			annotations: map[string]string{iamv1alpha1.UserEmailAnnotation: "old@example.com"},
			expectedAnnotations: map[string]string{
				iamv1alpha1.UserEmailAnnotation: "member@example.com",
			},
		},
		"no annotation when user has no email": {
			preObjects:          []client.Object{noEmailUser},
			expectedAnnotations: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(tc.preObjects...).Build()
			mutator := &GroupMembershipMutator{client: fakeClient}

			gm := newGroupMembership(tc.annotations)
			require.NoError(t, mutator.Default(context.Background(), gm))

			assert.Equal(t, tc.expectedAnnotations, gm.Annotations)
			assert.Equal(t, "member-user", gm.Spec.UserRef.Name)
		})
	}
}
