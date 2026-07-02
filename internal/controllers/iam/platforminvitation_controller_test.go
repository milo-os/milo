package iam

import (
	"context"
	"strings"
	"testing"
	"time"

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

// getPlatformInvitationTestScheme returns a runtime.Scheme with IAM APIs registered.
func getPlatformInvitationTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = iamv1alpha1.AddToScheme(scheme)
	_ = notificationv1alpha1.AddToScheme(scheme)
	return scheme
}

func Test_getDeterministicPlatformInvitationResourceName(t *testing.T) {
	pi := iamv1alpha1.PlatformInvitation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pi",
			UID:  types.UID("pi-uid"),
		},
		Spec: iamv1alpha1.PlatformInvitationSpec{Email: "Test@Example.com"},
	}

	name := getDeterministicPlatformInvitationResourceName(pi)
	want := "pi-uid-pi"
	if name != want {
		t.Fatalf("unexpected deterministic name, got %s want %s", name, want)
	}
}


func Test_PlatformInvitationController_Reconcile_Scheduled(t *testing.T) {
	ctx := context.TODO()
	scheme := getPlatformInvitationTestScheme()

	future := metav1.NewTime(time.Now().Add(1 * time.Hour))
	pi := &iamv1alpha1.PlatformInvitation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pi-scheduled",
			UID:  types.UID("pi-uid"),
		},
		Spec: iamv1alpha1.PlatformInvitationSpec{
			Email:      "scheduled@example.com",
			ScheduleAt: &future,
		},
	}

	builder := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&iamv1alpha1.PlatformInvitation{}).
		WithObjects(pi.DeepCopy())

	// index for users by email (even though no users yet)
	builder = builder.WithIndex(&iamv1alpha1.User{}, platformInvitationUserEmailIndexKey, func(obj client.Object) []string {
		u := obj.(*iamv1alpha1.User)
		return []string{strings.ToLower(u.Spec.Email)}
	})

	c := builder.Build()
	pc := &PlatformInvitationController{Client: c}

	res, err := pc.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pi.Name}})
	if err != nil {
		t.Fatalf("reconcile returned error: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("expected positive RequeueAfter for scheduled invitation, got %v", res.RequeueAfter)
	}

	updated := &iamv1alpha1.PlatformInvitation{}
	if err := c.Get(ctx, types.NamespacedName{Name: pi.Name}, updated); err != nil {
		t.Fatalf("failed to get updated PlatformInvitation: %v", err)
	}

	cond := meta.FindStatusCondition(updated.Status.Conditions, iamv1alpha1.PlatformInvitationReadyCondition)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("scheduled condition missing or not true: %+v", updated.Status.Conditions)
	}


}

func Test_PlatformInvitationController_Reconcile_UserExistsSkipsPAA(t *testing.T) {
	ctx := context.TODO()
	scheme := getPlatformInvitationTestScheme()

	pi := &iamv1alpha1.PlatformInvitation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pi-user-exists",
			UID:  types.UID("pi-uid-exists"),
		},
		Spec: iamv1alpha1.PlatformInvitationSpec{Email: "Test@Example.com"},
	}

	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "existing-user"},
		Spec:       iamv1alpha1.UserSpec{Email: "test@example.com"}, // lowercased variant
	}

	builder := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&iamv1alpha1.PlatformInvitation{}).
		WithObjects(pi.DeepCopy(), user.DeepCopy())
	builder = builder.WithIndex(&iamv1alpha1.User{}, platformInvitationUserEmailIndexKey, func(obj client.Object) []string {
		u := obj.(*iamv1alpha1.User)
		return []string{strings.ToLower(u.Spec.Email)}
	})
	c := builder.Build()

	pc := &PlatformInvitationController{Client: c}

	if _, err := pc.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pi.Name}}); err != nil {
		t.Fatalf("reconcile returned error: %v", err)
	}



	// Ready condition should be true with appropriate message
	updated := &iamv1alpha1.PlatformInvitation{}
	_ = c.Get(ctx, types.NamespacedName{Name: pi.Name}, updated)
	ready := meta.FindStatusCondition(updated.Status.Conditions, iamv1alpha1.PlatformInvitationReadyCondition)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatalf("expected Ready condition true when user exists, got: %+v", updated.Status.Conditions)
	}
	if !strings.Contains(ready.Message, "not created as user already exists") {
		t.Fatalf("unexpected Ready message: %s", ready.Message)
	}
}

func Test_PlatformInvitationController_Reconcile_NoUserCreatesPAA(t *testing.T) {
	ctx := context.TODO()
	scheme := getPlatformInvitationTestScheme()

	pi := &iamv1alpha1.PlatformInvitation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pi-create-paa",
			UID:  types.UID("pi-uid-create"),
		},
		Spec: iamv1alpha1.PlatformInvitationSpec{Email: "newuser@example.com"},
	}

	builder := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&iamv1alpha1.PlatformInvitation{}).
		WithObjects(pi.DeepCopy())
	builder = builder.WithIndex(&iamv1alpha1.User{}, platformInvitationUserEmailIndexKey, func(obj client.Object) []string {
		u := obj.(*iamv1alpha1.User)
		return []string{strings.ToLower(u.Spec.Email)}
	})
	c := builder.Build()

	pc := &PlatformInvitationController{Client: c}

	res, err := pc.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pi.Name}})
	if err != nil {
		t.Fatalf("reconcile returned error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("did not expect requeue for immediate invitation, got %v", res.RequeueAfter)
	}



	// Ready condition should be true with created message
	updated := &iamv1alpha1.PlatformInvitation{}
	_ = c.Get(ctx, types.NamespacedName{Name: pi.Name}, updated)
	ready := meta.FindStatusCondition(updated.Status.Conditions, iamv1alpha1.PlatformInvitationReadyCondition)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatalf("expected Ready condition true, got: %+v", updated.Status.Conditions)
	}
	if !strings.Contains(ready.Message, "Email sent.") {
		t.Fatalf("unexpected Ready message: %s", ready.Message)
	}
}
