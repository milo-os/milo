package iam

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

const groupMembershipTestNamespace = "organization-acme"

func groupMembershipTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = iamv1alpha1.AddToScheme(scheme)
	return scheme
}

func groupMembershipTestUser(name, email string) *iamv1alpha1.User {
	return &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-uid")},
		Spec:       iamv1alpha1.UserSpec{Email: email},
	}
}

func groupMembershipTestMembership(annotations map[string]string) *iamv1alpha1.GroupMembership {
	return &iamv1alpha1.GroupMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "acme-membership",
			Namespace:   groupMembershipTestNamespace,
			Annotations: annotations,
		},
		Spec: iamv1alpha1.GroupMembershipSpec{
			UserRef:  iamv1alpha1.UserReference{Name: "member-user"},
			GroupRef: iamv1alpha1.GroupReference{Name: "team", Namespace: groupMembershipTestNamespace},
		},
	}
}

func newGroupMembershipController(objs ...client.Object) (*GroupMembershipController, client.Client) {
	c := fake.NewClientBuilder().WithScheme(groupMembershipTestScheme()).WithObjects(objs...).Build()
	return &GroupMembershipController{Client: c}, c
}

func reconcileGroupMembership(t *testing.T, r *GroupMembershipController) {
	t.Helper()
	if _, err := r.Reconcile(context.TODO(), ctrl.Request{NamespacedName: types.NamespacedName{
		Name:      "acme-membership",
		Namespace: groupMembershipTestNamespace,
	}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func getGroupMembership(t *testing.T, c client.Client) *iamv1alpha1.GroupMembership {
	t.Helper()
	gm := &iamv1alpha1.GroupMembership{}
	if err := c.Get(context.TODO(), types.NamespacedName{
		Name:      "acme-membership",
		Namespace: groupMembershipTestNamespace,
	}, gm); err != nil {
		t.Fatalf("get group membership: %v", err)
	}
	return gm
}

func TestGroupMembershipControllerWritesMissingEmailAnnotation(t *testing.T) {
	r, c := newGroupMembershipController(
		groupMembershipTestUser("member-user", "member@example.com"),
		groupMembershipTestMembership(nil),
	)

	reconcileGroupMembership(t, r)

	if got := getGroupMembership(t, c).Annotations[iamv1alpha1.UserEmailAnnotation]; got != "member@example.com" {
		t.Fatalf("expected annotation %q, got %q", "member@example.com", got)
	}
}

func TestGroupMembershipControllerRefreshesStaleEmailAnnotation(t *testing.T) {
	r, c := newGroupMembershipController(
		groupMembershipTestUser("member-user", "new@example.com"),
		groupMembershipTestMembership(map[string]string{
			iamv1alpha1.UserEmailAnnotation: "old@example.com",
			"example.com/keep-me":           "yes",
		}),
	)

	reconcileGroupMembership(t, r)

	gm := getGroupMembership(t, c)
	if got := gm.Annotations[iamv1alpha1.UserEmailAnnotation]; got != "new@example.com" {
		t.Fatalf("expected annotation %q, got %q", "new@example.com", got)
	}
	if got := gm.Annotations["example.com/keep-me"]; got != "yes" {
		t.Fatalf("expected unrelated annotations to survive the patch, got %q", got)
	}
}

func TestGroupMembershipControllerIgnoresMissingUser(t *testing.T) {
	r, c := newGroupMembershipController(groupMembershipTestMembership(nil))

	before := getGroupMembership(t, c).ResourceVersion
	reconcileGroupMembership(t, r)

	gm := getGroupMembership(t, c)
	if _, ok := gm.Annotations[iamv1alpha1.UserEmailAnnotation]; ok {
		t.Fatalf("expected no email annotation when the referenced user is missing, got %q",
			gm.Annotations[iamv1alpha1.UserEmailAnnotation])
	}
	if gm.ResourceVersion != before {
		t.Fatalf("expected group membership to be untouched, resourceVersion went %q -> %q", before, gm.ResourceVersion)
	}
}

func TestGroupMembershipControllerSkipsWriteWhenEmailMatches(t *testing.T) {
	r, c := newGroupMembershipController(
		groupMembershipTestUser("member-user", "member@example.com"),
		groupMembershipTestMembership(map[string]string{
			iamv1alpha1.UserEmailAnnotation: "member@example.com",
		}),
	)

	before := getGroupMembership(t, c).ResourceVersion
	reconcileGroupMembership(t, r)

	gm := getGroupMembership(t, c)
	if gm.ResourceVersion != before {
		t.Fatalf("expected no write when the annotation already matches, resourceVersion went %q -> %q",
			before, gm.ResourceVersion)
	}
	if got := gm.Annotations[iamv1alpha1.UserEmailAnnotation]; got != "member@example.com" {
		t.Fatalf("expected annotation %q, got %q", "member@example.com", got)
	}
}
