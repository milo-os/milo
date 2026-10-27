package iam

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

// TestEnsureOwnerReferencesLeavesBindingsToTheAdopter: the User controller makes the User the owner of its
// UserPreference and writes no owner on the User's self-manage bindings. The PolicyBinding adopter is their one writer,
// so a binding's owner does not depend on which controller runs first.
func TestEnsureOwnerReferencesLeavesBindingsToTheAdopter(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, iamv1alpha1.AddToScheme(scheme))
	user := &iamv1alpha1.User{ObjectMeta: metav1.ObjectMeta{Name: "u", UID: "user-uid"}}
	pref := &iamv1alpha1.UserPreference{ObjectMeta: metav1.ObjectMeta{Name: "userpreference-u"}}
	selfManage := selfManageBinding("user-self-manage-u")
	prefSelfManage := selfManageBinding("userpreference-self-manage-u")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(user, pref, selfManage, prefSelfManage).Build()

	require.NoError(t, (&UserController{Client: c}).ensureOwnerReferences(context.Background(), user))

	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pref), pref))
	require.Equal(t, []types.UID{user.UID}, preferenceOwners(pref), "the User owns its UserPreference")
	for _, pb := range []*iamv1alpha1.PolicyBinding{selfManage, prefSelfManage} {
		require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pb), pb))
		require.Empty(t, pb.OwnerReferences, "%s's owner is the adopter's to write", pb.Name)
	}
}

func selfManageBinding(name string) *iamv1alpha1.PolicyBinding {
	return &iamv1alpha1.PolicyBinding{ObjectMeta: metav1.ObjectMeta{Namespace: "milo-system", Name: name}}
}

func preferenceOwners(pref *iamv1alpha1.UserPreference) []types.UID {
	var uids []types.UID
	for _, o := range pref.OwnerReferences {
		uids = append(uids, o.UID)
	}
	return uids
}
