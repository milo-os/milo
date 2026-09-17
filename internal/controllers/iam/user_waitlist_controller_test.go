package iam

import (
	"context"
	"testing"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const waitlistTestNamespace = "milo-system"

func waitlistTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = iamv1alpha1.AddToScheme(scheme)
	_ = notificationv1alpha1.AddToScheme(scheme)
	return scheme
}

// waitlistTestUser builds a User in the given access state. verified == nil leaves the
// EmailVerification field empty (a User zitadel-provider has not synced yet); otherwise it is
// set Unverified/Verified.
func waitlistTestUser(name string, access iamv1alpha1.PlatformAccessState, verified *bool) *iamv1alpha1.User {
	u := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-uid")},
		Spec:       iamv1alpha1.UserSpec{Email: name + "@example.com", GivenName: "Ada"},
		Status:     iamv1alpha1.UserStatus{PlatformAccess: access},
	}
	if verified != nil {
		u.Status.EmailVerification = iamv1alpha1.EmailVerificationStateUnverified
		if *verified {
			u.Status.EmailVerification = iamv1alpha1.EmailVerificationStateVerified
		}
	}
	return u
}

func newWaitlistController(objs ...client.Object) (*UserWaitlistController, client.Client) {
	c := fake.NewClientBuilder().WithScheme(waitlistTestScheme()).
		WithStatusSubresource(&iamv1alpha1.User{}).
		WithObjects(objs...).Build()
	return &UserWaitlistController{
		Client:                    c,
		SystemNamespace:           waitlistTestNamespace,
		PendingEmailTemplateName:  "tmpl-pending",
		ApprovedEmailTemplateName: "tmpl-approved",
		RejectedEmailTemplateName: "tmpl-rejected",
	}, c
}

func countEmails(t *testing.T, c client.Client) int {
	t.Helper()
	list := &notificationv1alpha1.EmailList{}
	if err := c.List(context.TODO(), list, client.InNamespace(waitlistTestNamespace)); err != nil {
		t.Fatalf("list emails: %v", err)
	}
	return len(list.Items)
}

func reconcileUser(t *testing.T, r *UserWaitlistController, name string) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.TODO(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return res
}

func boolPtr(b bool) *bool { return &b }

func Test_UserWaitlist_ApprovedButUnverified_SendsNothing(t *testing.T) {
	// The C11 gate: approval alone no longer mails an address nobody proved.
	for _, verified := range []*bool{nil, boolPtr(false)} {
		u := waitlistTestUser("u1", iamv1alpha1.PlatformAccessStateApproved, verified)
		r, c := newWaitlistController(u)

		res := reconcileUser(t, r, "u1")

		if res.RequeueAfter != 0 || res.Requeue {
			t.Fatalf("expected no requeue (the User watch re-triggers on the condition flip), got %+v", res)
		}
		if n := countEmails(t, c); n != 0 {
			t.Fatalf("expected 0 emails, got %d", n)
		}
		got := &iamv1alpha1.User{}
		_ = c.Get(context.TODO(), types.NamespacedName{Name: "u1"}, got)
		if meta.IsStatusConditionTrue(got.Status.Conditions, string(iamv1alpha1.UserWaitlistApprovedEmailSentCondition)) {
			t.Fatalf("sent-condition must not be set while unverified")
		}
	}
}

func Test_UserWaitlist_ApprovedAndVerified_SendsExactlyOnce(t *testing.T) {
	u := waitlistTestUser("u2", iamv1alpha1.PlatformAccessStateApproved, boolPtr(true))
	r, c := newWaitlistController(u)

	reconcileUser(t, r, "u2")
	reconcileUser(t, r, "u2") // idempotent: the sent-condition guard holds

	if n := countEmails(t, c); n != 1 {
		t.Fatalf("expected exactly 1 email, got %d", n)
	}
	list := &notificationv1alpha1.EmailList{}
	_ = c.List(context.TODO(), list, client.InNamespace(waitlistTestNamespace))
	if got := list.Items[0].Spec.TemplateRef.Name; got != "tmpl-approved" {
		t.Fatalf("template = %q, want tmpl-approved", got)
	}
}

func Test_UserWaitlist_VerificationArrivesAfterApproval_SendsOnce(t *testing.T) {
	u := waitlistTestUser("u3", iamv1alpha1.PlatformAccessStateApproved, boolPtr(false))
	r, c := newWaitlistController(u)

	reconcileUser(t, r, "u3")
	if n := countEmails(t, c); n != 0 {
		t.Fatalf("expected 0 emails before verification, got %d", n)
	}

	// zitadel-provider flips the field (simulated with the same write pattern it uses).
	got := &iamv1alpha1.User{}
	_ = c.Get(context.TODO(), types.NamespacedName{Name: "u3"}, got)
	got.Status.EmailVerification = iamv1alpha1.EmailVerificationStateVerified
	if err := c.Status().Update(context.TODO(), got); err != nil {
		t.Fatalf("status update: %v", err)
	}

	reconcileUser(t, r, "u3")
	if n := countEmails(t, c); n != 1 {
		t.Fatalf("expected 1 email after verification, got %d", n)
	}
}

func Test_UserWaitlist_Rejected_FollowsTheSameRule(t *testing.T) {
	unverified := waitlistTestUser("u4", iamv1alpha1.PlatformAccessStateRejected, boolPtr(false))
	r, c := newWaitlistController(unverified)
	reconcileUser(t, r, "u4")
	if n := countEmails(t, c); n != 0 {
		t.Fatalf("rejected+unverified: expected 0 emails, got %d", n)
	}

	verified := waitlistTestUser("u5", iamv1alpha1.PlatformAccessStateRejected, boolPtr(true))
	r, c = newWaitlistController(verified)
	reconcileUser(t, r, "u5")
	if n := countEmails(t, c); n != 1 {
		t.Fatalf("rejected+verified: expected 1 email, got %d", n)
	}
	list := &notificationv1alpha1.EmailList{}
	_ = c.List(context.TODO(), list, client.InNamespace(waitlistTestNamespace))
	if got := list.Items[0].Spec.TemplateRef.Name; got != "tmpl-rejected" {
		t.Fatalf("template = %q, want tmpl-rejected", got)
	}
}
