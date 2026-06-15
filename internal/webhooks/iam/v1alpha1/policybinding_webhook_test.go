package v1alpha1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func policyBinding(subjects ...iamv1alpha1.Subject) *iamv1alpha1.PolicyBinding {
	return &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: "organization-acme"},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef:  iamv1alpha1.RoleReference{Name: "viewer"},
			Subjects: subjects,
			ResourceSelector: iamv1alpha1.ResourceSelector{
				ResourceKind: &iamv1alpha1.ResourceKind{APIGroup: "resourcemanager.miloapis.com", Kind: "Organization"},
			},
		},
	}
}

func TestPolicyBindingMutator_Default(t *testing.T) {
	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "alice", UID: types.UID("user-uid-1")},
	}
	group := &iamv1alpha1.Group{
		ObjectMeta: metav1.ObjectMeta{Name: "loaders", Namespace: "organization-acme", UID: types.UID("group-uid-1")},
	}
	sa := &iamv1alpha1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "robot", UID: types.UID("sa-uid-1")},
	}

	tests := map[string]struct {
		preObjects  []client.Object
		subjects    []iamv1alpha1.Subject
		expectError bool
		contains    string
		assertUID   func(t *testing.T, subjects []iamv1alpha1.Subject)
	}{
		"resolves user uid from name": {
			preObjects: []client.Object{user},
			subjects:   []iamv1alpha1.Subject{{Kind: "User", Name: "alice"}},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Equal(t, "user-uid-1", s[0].UID)
			},
		},
		"resolves group uid from name and namespace": {
			preObjects: []client.Object{group},
			subjects:   []iamv1alpha1.Subject{{Kind: "Group", Name: "loaders", Namespace: "organization-acme"}},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Equal(t, "group-uid-1", s[0].UID)
			},
		},
		"resolves serviceaccount uid from name": {
			preObjects: []client.Object{sa},
			subjects:   []iamv1alpha1.Subject{{Kind: "ServiceAccount", Name: "robot"}},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Equal(t, "sa-uid-1", s[0].UID)
			},
		},
		"leaves an already-set uid untouched": {
			preObjects: []client.Object{user},
			subjects:   []iamv1alpha1.Subject{{Kind: "User", Name: "alice", UID: "preset-uid"}},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Equal(t, "preset-uid", s[0].UID)
			},
		},
		"skips system groups": {
			subjects: []iamv1alpha1.Subject{{Kind: "Group", Name: "system:authenticated-users"}},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Empty(t, s[0].UID)
			},
		},
		"resolves multiple subjects": {
			preObjects: []client.Object{user, group},
			subjects: []iamv1alpha1.Subject{
				{Kind: "User", Name: "alice"},
				{Kind: "Group", Name: "loaders", Namespace: "organization-acme"},
			},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Equal(t, "user-uid-1", s[0].UID)
				assert.Equal(t, "group-uid-1", s[1].UID)
			},
		},
		"errors when the named user does not exist": {
			subjects:    []iamv1alpha1.Subject{{Kind: "User", Name: "ghost"}},
			expectError: true,
			contains:    `User "ghost" not found`,
		},
		"errors when a group subject omits the namespace": {
			subjects:    []iamv1alpha1.Subject{{Kind: "Group", Name: "loaders"}},
			expectError: true,
			contains:    "namespace is required",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(tc.preObjects...).Build()
			mutator := &PolicyBindingMutator{client: cl}

			pb := policyBinding(tc.subjects...)
			err := mutator.Default(context.Background(), pb)

			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.contains)
				return
			}

			require.NoError(t, err)
			if tc.assertUID != nil {
				tc.assertUID(t, pb.Spec.Subjects)
			}
		})
	}
}
