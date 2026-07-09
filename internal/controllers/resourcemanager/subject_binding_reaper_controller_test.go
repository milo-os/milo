package resourcemanager

import (
	"context"
	"testing"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newSubjectBinding(name, userName, userUID string) *iamv1alpha1.PolicyBinding {
	return &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "organization-test-org",
			Labels: map[string]string{
				resourcemanagerv1alpha1.SubjectUserNameLabel: userName,
			},
		},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef: iamv1alpha1.RoleReference{
				Name: "owner",
			},
			Subjects: []iamv1alpha1.Subject{
				{Kind: "User", Name: userName, UID: userUID},
			},
			ResourceSelector: iamv1alpha1.ResourceSelector{
				ResourceRef: &iamv1alpha1.ResourceReference{
					APIGroup: "resourcemanager.miloapis.com",
					Kind:     "Project",
					Name:     "some-project",
					UID:      "project-uid",
				},
			},
		},
	}
}

// TestSubjectBindingReaperController_Reconcile_UserDeleted_ReapsOnlyItsBindings verifies that
// when a User no longer exists, bindings labeled for that user are deleted, while a binding
// labeled for a different, still-existing user is left alone.
func TestSubjectBindingReaperController_Reconcile_UserDeleted_ReapsOnlyItsBindings(t *testing.T) {
	ctx := context.TODO()
	scheme := getTestScheme()

	// alice has no User object, simulating deletion.
	aliceBinding := newSubjectBinding("project-foo-owner-abc", "alice", "alice-uid")

	bob := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "bob", UID: types.UID("bob-uid")},
		Spec:       iamv1alpha1.UserSpec{Email: "bob@example.com"},
	}
	bobBinding := newSubjectBinding("project-bar-owner-def", "bob", "bob-uid")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(bob, aliceBinding, bobBinding).
		Build()

	controller := &SubjectBindingReaperController{Client: c}

	// Simulate the reconcile triggered by alice's deletion.
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "alice"}}
	if _, err := controller.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if err := c.Get(ctx, types.NamespacedName{Name: aliceBinding.Name, Namespace: aliceBinding.Namespace}, &iamv1alpha1.PolicyBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected alice's binding to be deleted, got err=%v", err)
	}

	if err := c.Get(ctx, types.NamespacedName{Name: bobBinding.Name, Namespace: bobBinding.Namespace}, &iamv1alpha1.PolicyBinding{}); err != nil {
		t.Fatalf("expected bob's binding to be untouched (bob still exists), got err=%v", err)
	}
}

// TestSubjectBindingReaperController_Reconcile_UserStillExists_LeavesBindingAlone verifies
// that a reconcile for a User that still exists does not touch any bindings labeled for them.
func TestSubjectBindingReaperController_Reconcile_UserStillExists_LeavesBindingAlone(t *testing.T) {
	ctx := context.TODO()
	scheme := getTestScheme()

	alice := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "alice", UID: types.UID("alice-uid")},
		Spec:       iamv1alpha1.UserSpec{Email: "alice@example.com"},
	}
	aliceBinding := newSubjectBinding("project-foo-owner-abc", "alice", "alice-uid")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(alice, aliceBinding).
		Build()

	controller := &SubjectBindingReaperController{Client: c}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "alice"}}
	if _, err := controller.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if err := c.Get(ctx, types.NamespacedName{Name: aliceBinding.Name, Namespace: aliceBinding.Namespace}, &iamv1alpha1.PolicyBinding{}); err != nil {
		t.Fatalf("expected alice's binding to still exist while alice exists, got err=%v", err)
	}
}

// TestSubjectBindingReaperController_Reconcile_NoBindingsLabeled_NoOp verifies a reconcile
// for a deleted user with no labeled bindings at all is a no-op, not an error.
func TestSubjectBindingReaperController_Reconcile_NoBindingsLabeled_NoOp(t *testing.T) {
	ctx := context.TODO()
	scheme := getTestScheme()

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	controller := &SubjectBindingReaperController{Client: c}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "nobody"}}
	if _, err := controller.Reconcile(ctx, req); err != nil {
		t.Fatalf("expected no-op reconcile to succeed, got err=%v", err)
	}
}
