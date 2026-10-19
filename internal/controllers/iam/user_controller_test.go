package iam

import (
	"context"
	"testing"
	"time"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const userControllerTestUser = "user-1"

func userControllerTestRiskAssessment() *iamv1alpha1.RiskAssessment {
	return &iamv1alpha1.RiskAssessment{
		Score:          "0.12",
		Decision:       "ACCEPTED",
		EvaluatedAt:    &metav1.Time{Time: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)},
		EvaluationName: "user-1-eval-1",
	}
}

func userControllerTestPlatformAccess(state iamv1alpha1.PlatformAccessState, ra *iamv1alpha1.RiskAssessment) *iamv1alpha1.PlatformAccess {
	return &iamv1alpha1.PlatformAccess{
		ObjectMeta: metav1.ObjectMeta{Name: userControllerTestUser},
		Spec: iamv1alpha1.PlatformAccessSpec{
			UserRef:        iamv1alpha1.UserReference{Name: userControllerTestUser},
			State:          state,
			RiskAssessment: ra,
		},
	}
}

// newUserControllerForTest returns a UserController backed by a fake client
// and a pointer to a counter of User status writes, so tests can assert that
// an unchanged projection does not write.
func newUserControllerForTest(objs ...client.Object) (*UserController, client.Client, *int) {
	statusUpdates := 0
	c := fake.NewClientBuilder().WithScheme(waitlistTestScheme()).
		WithStatusSubresource(&iamv1alpha1.User{}, &iamv1alpha1.PlatformAccess{}).
		WithObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if _, ok := obj.(*iamv1alpha1.User); ok && subResourceName == "status" {
					statusUpdates++
				}
				return c.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()
	return &UserController{Client: c}, c, &statusUpdates
}

func reconcileUserController(t *testing.T, r *UserController) {
	t.Helper()
	if _, err := r.Reconcile(context.TODO(), ctrl.Request{NamespacedName: types.NamespacedName{Name: userControllerTestUser}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func TestUserController_ProjectsPlatformAccess(t *testing.T) {
	tests := []struct {
		name               string
		platformAccess     *iamv1alpha1.PlatformAccess
		wantPlatformAccess iamv1alpha1.PlatformAccessState
		wantRiskAssessment *iamv1alpha1.RiskAssessment
	}{
		{
			name:               "platform access with risk assessment is copied",
			platformAccess:     userControllerTestPlatformAccess(iamv1alpha1.PlatformAccessStateApproved, userControllerTestRiskAssessment()),
			wantPlatformAccess: iamv1alpha1.PlatformAccessStateApproved,
			wantRiskAssessment: userControllerTestRiskAssessment(),
		},
		{
			name:               "platform access without risk assessment leaves it nil",
			platformAccess:     userControllerTestPlatformAccess(iamv1alpha1.PlatformAccessStateRejected, nil),
			wantPlatformAccess: iamv1alpha1.PlatformAccessStateRejected,
			wantRiskAssessment: nil,
		},
		{
			name:               "no platform access is pending with no risk assessment",
			platformAccess:     nil,
			wantPlatformAccess: iamv1alpha1.PlatformAccessStatePending,
			wantRiskAssessment: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{waitlistTestUser(userControllerTestUser, "", nil)}
			if tt.platformAccess != nil {
				objs = append(objs, tt.platformAccess)
			}
			r, c, statusUpdates := newUserControllerForTest(objs...)

			reconcileUserController(t, r)

			got := &iamv1alpha1.User{}
			if err := c.Get(context.TODO(), types.NamespacedName{Name: userControllerTestUser}, got); err != nil {
				t.Fatalf("get user: %v", err)
			}
			if got.Status.PlatformAccess != tt.wantPlatformAccess {
				t.Errorf("status.platformAccess = %q, want %q", got.Status.PlatformAccess, tt.wantPlatformAccess)
			}
			if !equality.Semantic.DeepEqual(got.Status.RiskAssessment, tt.wantRiskAssessment) {
				t.Errorf("status.riskAssessment = %+v, want %+v", got.Status.RiskAssessment, tt.wantRiskAssessment)
			}
			if *statusUpdates != 1 {
				t.Errorf("status updates on first reconcile = %d, want 1", *statusUpdates)
			}

			// A second reconcile with nothing changed must not write status again.
			reconcileUserController(t, r)
			if *statusUpdates != 1 {
				t.Errorf("status updates after unchanged reconcile = %d, want 1", *statusUpdates)
			}
		})
	}
}

// The projected value must be a copy: mutating the User's status must not
// alias the PlatformAccess the controller read it from.
func TestUserController_RiskAssessmentIsDeepCopied(t *testing.T) {
	pa := userControllerTestPlatformAccess(iamv1alpha1.PlatformAccessStateApproved, userControllerTestRiskAssessment())
	user := waitlistTestUser(userControllerTestUser, "", nil)
	r, _, _ := newUserControllerForTest(user, pa)

	ctx := context.TODO()
	gotPA, err := r.getPlatformAccess(ctx, user)
	if err != nil {
		t.Fatalf("getPlatformAccess: %v", err)
	}
	state, ra := platformAccessProjection(gotPA)
	if state != iamv1alpha1.PlatformAccessStateApproved {
		t.Fatalf("state = %q, want Approved", state)
	}
	ra.Decision = "MUTATED"
	if gotPA.Spec.RiskAssessment.Decision != "ACCEPTED" {
		t.Errorf("mutating projected risk assessment changed the source: %q", gotPA.Spec.RiskAssessment.Decision)
	}
}

// When the fraud service records a newer evaluation without changing state,
// the User picks it up; when the assessment is cleared, the User's copy is too.
func TestUserController_RiskAssessmentFollowsPlatformAccess(t *testing.T) {
	pa := userControllerTestPlatformAccess(iamv1alpha1.PlatformAccessStateApproved, userControllerTestRiskAssessment())
	r, c, statusUpdates := newUserControllerForTest(waitlistTestUser(userControllerTestUser, "", nil), pa)
	ctx := context.TODO()

	reconcileUserController(t, r)

	current := &iamv1alpha1.PlatformAccess{}
	if err := c.Get(ctx, types.NamespacedName{Name: userControllerTestUser}, current); err != nil {
		t.Fatalf("get platform access: %v", err)
	}
	current.Spec.RiskAssessment.Score = "0.87"
	current.Spec.RiskAssessment.Decision = "REVIEW"
	current.Spec.RiskAssessment.EvaluationName = "user-1-eval-2"
	if err := c.Update(ctx, current); err != nil {
		t.Fatalf("update platform access: %v", err)
	}

	reconcileUserController(t, r)

	got := &iamv1alpha1.User{}
	if err := c.Get(ctx, types.NamespacedName{Name: userControllerTestUser}, got); err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.Status.RiskAssessment == nil || got.Status.RiskAssessment.Decision != "REVIEW" || got.Status.RiskAssessment.Score != "0.87" {
		t.Fatalf("status.riskAssessment = %+v, want REVIEW/0.87", got.Status.RiskAssessment)
	}
	if *statusUpdates != 2 {
		t.Errorf("status updates = %d, want 2", *statusUpdates)
	}

	if err := c.Get(ctx, types.NamespacedName{Name: userControllerTestUser}, current); err != nil {
		t.Fatalf("get platform access: %v", err)
	}
	current.Spec.RiskAssessment = nil
	if err := c.Update(ctx, current); err != nil {
		t.Fatalf("update platform access: %v", err)
	}

	reconcileUserController(t, r)

	if err := c.Get(ctx, types.NamespacedName{Name: userControllerTestUser}, got); err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.Status.RiskAssessment != nil {
		t.Errorf("status.riskAssessment = %+v, want nil after it was cleared on the PlatformAccess", got.Status.RiskAssessment)
	}
}
