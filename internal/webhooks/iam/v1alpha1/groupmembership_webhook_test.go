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

func newGroupMembership(name, namespace, user, group, groupNamespace string) *iamv1alpha1.GroupMembership {
	return &iamv1alpha1.GroupMembership{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: iamv1alpha1.GroupMembershipSpec{
			UserRef: iamv1alpha1.UserReference{Name: user},
			GroupRef: iamv1alpha1.GroupReference{
				Name:      group,
				Namespace: groupNamespace,
			},
		},
	}
}

func newUser(name string) *iamv1alpha1.User {
	return &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

func newGroup(name, namespace string) *iamv1alpha1.Group {
	return &iamv1alpha1.Group{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
}

func newTestValidator(objects ...client.Object) *GroupMembershipValidator {
	builder := fake.NewClientBuilder().
		WithScheme(runtimeScheme).
		WithIndex(&iamv1alpha1.GroupMembership{}, groupMembershipCompositeKey, func(rawObj client.Object) []string {
			gm := rawObj.(*iamv1alpha1.GroupMembership)
			return []string{buildGroupMembershipCompositeKey(gm.Spec.UserRef, gm.Spec.GroupRef)}
		})
	if len(objects) > 0 {
		builder = builder.WithObjects(objects...)
	}
	return &GroupMembershipValidator{
		client: builder.Build(),
	}
}

func TestGroupMembership_Create_Valid(t *testing.T) {
	v := newTestValidator(
		newUser("alice"),
		newGroup("eng", "ns1"),
	)

	membership := newGroupMembership("gm-1", "ns1", "alice", "eng", "ns1")
	_, err := v.ValidateCreate(context.Background(), membership)
	require.NoError(t, err)
}

func TestGroupMembership_Create_DuplicateRejected(t *testing.T) {
	existing := newGroupMembership("gm-1", "ns1", "alice", "eng", "ns1")
	v := newTestValidator(
		newUser("alice"),
		newGroup("eng", "ns1"),
		existing,
	)

	membership := newGroupMembership("gm-2", "ns1", "alice", "eng", "ns1")
	_, err := v.ValidateCreate(context.Background(), membership)
	require.Error(t, err, "duplicate user/group pair must be rejected")
	assert.Contains(t, err.Error(), "alice")
	assert.Contains(t, err.Error(), "eng")
	assert.Contains(t, strings.ToLower(err.Error()), "already a member")
}

func TestGroupMembership_Create_DuplicateInDifferentGroupAllowed(t *testing.T) {
	existing := newGroupMembership("gm-1", "ns1", "alice", "eng", "ns1")
	v := newTestValidator(
		newUser("alice"),
		newGroup("eng", "ns1"),
		newGroup("design", "ns1"),
		existing,
	)

	membership := newGroupMembership("gm-2", "ns1", "alice", "design", "ns1")
	_, err := v.ValidateCreate(context.Background(), membership)
	require.NoError(t, err, "same user in a different group is allowed")
}

func TestGroupMembership_Create_DuplicateDifferentUserAllowed(t *testing.T) {
	existing := newGroupMembership("gm-1", "ns1", "alice", "eng", "ns1")
	v := newTestValidator(
		newUser("alice"),
		newUser("bob"),
		newGroup("eng", "ns1"),
		existing,
	)

	membership := newGroupMembership("gm-2", "ns1", "bob", "eng", "ns1")
	_, err := v.ValidateCreate(context.Background(), membership)
	require.NoError(t, err, "a different user in the same group is allowed")
}

func TestGroupMembership_Create_DuplicateDifferentNamespaceAllowed(t *testing.T) {
	existing := newGroupMembership("gm-1", "ns1", "alice", "eng", "ns1")
	v := newTestValidator(
		newUser("alice"),
		newGroup("eng", "ns1"),
		newGroup("eng", "ns2"),
		existing,
	)

	membership := newGroupMembership("gm-2", "ns2", "alice", "eng", "ns2")
	_, err := v.ValidateCreate(context.Background(), membership)
	require.NoError(t, err, "memberships in different namespaces are independent")
}

func TestGroupMembership_Create_UserDoesNotExistRejected(t *testing.T) {
	v := newTestValidator(
		newGroup("eng", "ns1"),
	)

	membership := newGroupMembership("gm-1", "ns1", "ghost", "eng", "ns1")
	_, err := v.ValidateCreate(context.Background(), membership)
	require.Error(t, err, "a create referencing a missing user must be rejected")
	assert.Contains(t, err.Error(), `"ghost"`)
}

func TestGroupMembership_Create_GroupDoesNotExistRejected(t *testing.T) {
	v := newTestValidator(
		newUser("alice"),
	)

	membership := newGroupMembership("gm-1", "ns1", "alice", "ghost-group", "ns1")
	_, err := v.ValidateCreate(context.Background(), membership)
	require.Error(t, err, "a create referencing a missing group must be rejected")
	assert.Contains(t, err.Error(), `"ghost-group"`)
}

func TestGroupMembership_Update_MetadataOnlyAllowed(t *testing.T) {
	oldMembership := newGroupMembership("gm-1", "ns1", "alice", "eng", "ns1")
	newMembership := newGroupMembership("gm-1", "ns1", "alice", "eng", "ns1")
	newMembership.Finalizers = []string{"iam.miloapis.com/openfga"}
	newMembership.Labels = map[string]string{"iam.miloapis.com/group-name": "eng"}
	v := newTestValidator()

	_, err := v.ValidateUpdate(context.Background(), oldMembership, newMembership)
	require.NoError(t, err, "metadata-only updates such as the OpenFGA controller adding its finalizer must be allowed")
}

func TestGroupMembership_Update_ChangeGroupRejected(t *testing.T) {
	oldMembership := newGroupMembership("gm-1", "ns1", "alice", "eng", "ns1")
	updated := newGroupMembership("gm-1", "ns1", "alice", "design", "ns1")
	v := newTestValidator()

	_, err := v.ValidateUpdate(context.Background(), oldMembership, updated)
	require.Error(t, err, "changing the group pointer must be rejected")
	assert.Contains(t, err.Error(), "immutable")
}

func TestGroupMembership_Update_ChangeUserRejected(t *testing.T) {
	oldMembership := newGroupMembership("gm-1", "ns1", "alice", "eng", "ns1")
	updated := newGroupMembership("gm-1", "ns1", "bob", "eng", "ns1")
	v := newTestValidator()

	_, err := v.ValidateUpdate(context.Background(), oldMembership, updated)
	require.Error(t, err, "changing the user pointer must be rejected")
	assert.Contains(t, err.Error(), "immutable")
}

func TestGroupMembership_Update_ChangeGroupNamespaceRejected(t *testing.T) {
	oldMembership := newGroupMembership("gm-1", "ns1", "alice", "eng", "ns1")
	updated := newGroupMembership("gm-1", "ns1", "alice", "eng", "ns2")
	v := newTestValidator()

	_, err := v.ValidateUpdate(context.Background(), oldMembership, updated)
	require.Error(t, err, "changing the referenced group namespace must be rejected")
	assert.Contains(t, err.Error(), "immutable")
}

func TestGroupMembership_Delete_Allowed(t *testing.T) {
	membership := newGroupMembership("gm-1", "ns1", "alice", "eng", "ns1")
	v := newTestValidator()

	_, err := v.ValidateDelete(context.Background(), membership)
	require.NoError(t, err, "deletion must be allowed")
}

func newAnnotatedGroupMembership(annotations map[string]string) *iamv1alpha1.GroupMembership {
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

			gm := newAnnotatedGroupMembership(tc.annotations)
			require.NoError(t, mutator.Default(context.Background(), gm))

			assert.Equal(t, tc.expectedAnnotations, gm.Annotations)
			assert.Equal(t, "member-user", gm.Spec.UserRef.Name)
		})
	}
}
