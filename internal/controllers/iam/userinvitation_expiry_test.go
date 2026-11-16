package iam

import (
	"context"
	"strings"
	"testing"
	"time"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newExpiryTestController builds a controller over one pending invitation that
// expires at expiresAt, with no invitee User yet (the usual state while a new
// invitee is still signing up).
func newExpiryTestController(t *testing.T, expiresAt time.Time) (*UserInvitationController, client.Client, *record.FakeRecorder, ctrl.Request) {
	t.Helper()
	inviter := &iamv1alpha1.User{ObjectMeta: metav1.ObjectMeta{Name: "inviter", UID: types.UID("inviter-uid")}, Spec: iamv1alpha1.UserSpec{Email: "inviter@example.com"}}
	ui := &iamv1alpha1.UserInvitation{
		ObjectMeta: metav1.ObjectMeta{Name: "inv", Namespace: "organization-org", UID: types.UID("ui-uid")},
		Spec: iamv1alpha1.UserInvitationSpec{
			Email:           "new@example.com",
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{Name: "org"},
			State:           iamv1alpha1.UserInvitationStatePending,
			Roles:           []iamv1alpha1.RoleReference{{Name: "org-viewer", Namespace: "milo-system"}},
			InvitedBy:       iamv1alpha1.UserReference{Name: inviter.Name},
			ExpirationDate:  &metav1.Time{Time: expiresAt},
		},
	}
	org := &resourcemanagerv1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{Name: "org", UID: types.UID("org-uid")}}

	c := fake.NewClientBuilder().WithScheme(getTestScheme()).
		WithStatusSubresource(&iamv1alpha1.UserInvitation{}).
		WithObjects(ui, org, inviter).
		WithIndex(&iamv1alpha1.User{}, userEmailIndexKey, func(obj client.Object) []string {
			return []string{strings.ToLower(obj.(*iamv1alpha1.User).Spec.Email)}
		}).
		WithIndex(&iamv1alpha1.UserInvitation{}, userEmailIndexKey, func(obj client.Object) []string {
			return []string{strings.ToLower(obj.(*iamv1alpha1.UserInvitation).Spec.Email)}
		}).
		Build()

	recorder := record.NewFakeRecorder(10)
	uic := &UserInvitationController{Client: c, SystemNamespace: "milo-system", EventRecorder: recorder}
	initFinalizer(t, uic)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: ui.Name, Namespace: ui.Namespace}}
	// The first reconcile only registers the finalizer.
	if _, err := uic.Reconcile(context.TODO(), req); err != nil {
		t.Fatalf("finalizer reconcile: %v", err)
	}
	return uic, c, recorder, req
}

func TestUserInvitationController_Reconcile_RequeuesAtExpiry(t *testing.T) {
	expiresAt := time.Now().Add(2 * time.Hour)
	uic, _, recorder, req := newExpiryTestController(t, expiresAt)

	res, err := uic.Reconcile(context.TODO(), req)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter < 2*time.Hour-time.Minute || res.RequeueAfter > 2*time.Hour+time.Minute {
		t.Fatalf("expected a requeue at expiry (~2h), got %v", res.RequeueAfter)
	}
	if len(recorder.Events) != 0 {
		t.Fatalf("expected no events for a live invitation, got %v", <-recorder.Events)
	}
}

func TestUserInvitationController_Reconcile_RecordsExpiry(t *testing.T) {
	uic, c, recorder, req := newExpiryTestController(t, time.Now().Add(-time.Minute))

	res, err := uic.Reconcile(context.TODO(), req)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("expected no requeue once expired, got %v", res.RequeueAfter)
	}

	got := &iamv1alpha1.UserInvitation{}
	if err := c.Get(context.TODO(), req.NamespacedName, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if !meta.IsStatusConditionTrue(got.Status.Conditions, string(iamv1alpha1.UserInvitationExpiredCondition)) {
		t.Fatalf("expected Expired condition, got %+v", got.Status.Conditions)
	}

	select {
	case e := <-recorder.Events:
		want := "Normal InvitationExpired The invitation for new@example.com to org expired"
		if e != want {
			t.Fatalf("event = %q, want %q", e, want)
		}
	default:
		t.Fatal("expected an InvitationExpired event")
	}

	// A later reconcile must not record the expiry again.
	if _, err := uic.Reconcile(context.TODO(), req); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(recorder.Events) != 0 {
		t.Fatalf("expected one expiry event, got another: %v", <-recorder.Events)
	}
}

func TestUserInvitationController_Reconcile_LongExpiredRecordsNoEvent(t *testing.T) {
	uic, c, recorder, req := newExpiryTestController(t, time.Now().Add(-72*time.Hour))

	if _, err := uic.Reconcile(context.TODO(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := &iamv1alpha1.UserInvitation{}
	if err := c.Get(context.TODO(), req.NamespacedName, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if !meta.IsStatusConditionTrue(got.Status.Conditions, string(iamv1alpha1.UserInvitationExpiredCondition)) {
		t.Fatalf("expected Expired condition, got %+v", got.Status.Conditions)
	}
	if len(recorder.Events) != 0 {
		t.Fatalf("expected no event for an invitation that expired days ago, got %v", <-recorder.Events)
	}
}
