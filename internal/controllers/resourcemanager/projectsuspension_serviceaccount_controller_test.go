package resourcemanager

import (
	"context"
	"fmt"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

func suspensionTestProject(name string, ready bool, suspended *bool, reasons ...resourcemanagerv1alpha1.ProjectSuspensionReason) *resourcemanagerv1alpha1.Project {
	p := &resourcemanagerv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: name}}
	readyStatus := metav1.ConditionFalse
	if ready {
		readyStatus = metav1.ConditionTrue
	}
	apimeta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{Type: resourcemanagerv1alpha1.ProjectReady, Status: readyStatus, Reason: "Test"})
	if suspended != nil {
		status := metav1.ConditionFalse
		if *suspended {
			status = metav1.ConditionTrue
		}
		apimeta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{Type: resourcemanagerv1alpha1.ProjectSuspended, Status: status, Reason: "Test"})
	}
	for _, r := range reasons {
		p.Status.Suspensions = append(p.Status.Suspensions, resourcemanagerv1alpha1.ProjectSuspensionInfo{Reason: r})
	}
	return p
}

func suspensionTestServiceAccount(name string, conditions ...metav1.Condition) *iamv1alpha1.ServiceAccount {
	return &iamv1alpha1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       iamv1alpha1.ServiceAccountSpec{State: "Active"},
		Status: iamv1alpha1.ServiceAccountStatus{
			Conditions: conditions,
		},
	}
}

var readyServiceAccountCondition = metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Reconciled", LastTransitionTime: metav1.Now()}

func newServiceAccountSuspensionTest(t *testing.T, project *resourcemanagerv1alpha1.Project, serviceAccounts ...client.Object) (*ProjectServiceAccountSuspensionController, client.Client, *[]string) {
	t.Helper()
	if err := utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=true", features.ProjectSuspension)); err != nil {
		t.Fatalf("enable feature gate: %v", err)
	}
	scheme := getTestScheme()
	core := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project).WithStatusSubresource(project).Build()
	projectCP := fake.NewClientBuilder().WithScheme(scheme).WithObjects(serviceAccounts...).
		WithStatusSubresource(&iamv1alpha1.ServiceAccount{}).Build()

	var reached []string
	r := &ProjectServiceAccountSuspensionController{
		Client: core,
		ProjectClient: func(name string) (client.Client, error) {
			reached = append(reached, name)
			return projectCP, nil
		},
	}
	return r, projectCP, &reached
}

func reconcileProject(t *testing.T, r *ProjectServiceAccountSuspensionController, name string) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	return res
}

func suspendedCondition(t *testing.T, c client.Client, name string) *metav1.Condition {
	t.Helper()
	var sa iamv1alpha1.ServiceAccount
	if err := c.Get(context.Background(), types.NamespacedName{Name: name}, &sa); err != nil {
		t.Fatalf("get ServiceAccount %q: %v", name, err)
	}
	return apimeta.FindStatusCondition(sa.Status.Conditions, iamv1alpha1.ServiceAccountSuspended)
}

func TestProjectServiceAccountSuspension_SuspendedProjectMarksServiceAccounts(t *testing.T) {
	suspended := true
	project := suspensionTestProject("acme", true, &suspended, resourcemanagerv1alpha1.ReasonFraud, resourcemanagerv1alpha1.ReasonBilling)
	r, pc, _ := newServiceAccountSuspensionTest(t, project,
		suspensionTestServiceAccount("robot-a", readyServiceAccountCondition),
		suspensionTestServiceAccount("robot-b"),
	)

	if res := reconcileProject(t, r, "acme"); res.RequeueAfter != defaultSuspendedProjectRecheck {
		t.Errorf("RequeueAfter = %v, want %v so a missed or erased mark is repaired", res.RequeueAfter, defaultSuspendedProjectRecheck)
	}

	for _, name := range []string{"robot-a", "robot-b"} {
		cond := suspendedCondition(t, pc, name)
		if cond == nil || cond.Status != metav1.ConditionTrue {
			t.Fatalf("%s: Suspended condition = %+v, want True", name, cond)
		}
		if cond.Reason != iamv1alpha1.ServiceAccountProjectSuspendedReason {
			t.Errorf("%s: reason = %q, want %q", name, cond.Reason, iamv1alpha1.ServiceAccountProjectSuspendedReason)
		}
		if want := `Project "acme" is suspended (Fraud, Billing).`; cond.Message != want {
			t.Errorf("%s: message = %q, want %q", name, cond.Message, want)
		}
	}

	// The auth provider's Ready condition must survive.
	var sa iamv1alpha1.ServiceAccount
	if err := pc.Get(context.Background(), types.NamespacedName{Name: "robot-a"}, &sa); err != nil {
		t.Fatal(err)
	}
	if apimeta.FindStatusCondition(sa.Status.Conditions, "Ready") == nil {
		t.Errorf("Ready condition was dropped: %+v", sa.Status.Conditions)
	}
	if sa.Spec.State != "Active" {
		t.Errorf("spec.state = %q, want it untouched", sa.Spec.State)
	}
}

func TestProjectServiceAccountSuspension_LiftedProjectRemovesCondition(t *testing.T) {
	lifted := false
	project := suspensionTestProject("acme", true, &lifted)
	stale := metav1.Condition{
		Type: iamv1alpha1.ServiceAccountSuspended, Status: metav1.ConditionTrue,
		Reason: iamv1alpha1.ServiceAccountProjectSuspendedReason, Message: "old", LastTransitionTime: metav1.Now(),
	}
	r, pc, _ := newServiceAccountSuspensionTest(t, project,
		suspensionTestServiceAccount("robot-a", readyServiceAccountCondition, stale),
		suspensionTestServiceAccount("robot-b"),
	)

	if res := reconcileProject(t, r, "acme"); res.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0: only suspended projects are rechecked", res.RequeueAfter)
	}
	for _, name := range []string{"robot-a", "robot-b"} {
		if cond := suspendedCondition(t, pc, name); cond != nil {
			t.Errorf("%s: Suspended condition = %+v, want it removed", name, cond)
		}
	}
}

func TestProjectServiceAccountSuspension_UpToDateServiceAccountIsNotWritten(t *testing.T) {
	suspended := true
	project := suspensionTestProject("acme", true, &suspended, resourcemanagerv1alpha1.ReasonAbuse)
	current := metav1.Condition{
		Type: iamv1alpha1.ServiceAccountSuspended, Status: metav1.ConditionTrue,
		Reason: iamv1alpha1.ServiceAccountProjectSuspendedReason, Message: `Project "acme" is suspended (Abuse).`,
		LastTransitionTime: metav1.Now(),
	}
	r, pc, _ := newServiceAccountSuspensionTest(t, project, suspensionTestServiceAccount("robot-a", current))

	var before iamv1alpha1.ServiceAccount
	if err := pc.Get(context.Background(), types.NamespacedName{Name: "robot-a"}, &before); err != nil {
		t.Fatal(err)
	}
	reconcileProject(t, r, "acme")
	var after iamv1alpha1.ServiceAccount
	if err := pc.Get(context.Background(), types.NamespacedName{Name: "robot-a"}, &after); err != nil {
		t.Fatal(err)
	}
	if before.ResourceVersion != after.ResourceVersion {
		t.Errorf("resourceVersion changed from %s to %s; an up-to-date ServiceAccount must not be written", before.ResourceVersion, after.ResourceVersion)
	}
}

func TestProjectServiceAccountSuspension_ProjectNotReady(t *testing.T) {
	suspended := true
	r, _, reached := newServiceAccountSuspensionTest(t, suspensionTestProject("acme", false, &suspended))

	if res := reconcileProject(t, r, "acme"); res.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0: becoming Ready reconciles the project again", res.RequeueAfter)
	}
	if len(*reached) != 0 {
		t.Errorf("reached the control plane of a project that is not ready: %v", *reached)
	}
}

func TestProjectServiceAccountSuspension_FeatureGateOff(t *testing.T) {
	suspended := true
	r, pc, reached := newServiceAccountSuspensionTest(t, suspensionTestProject("acme", true, &suspended), suspensionTestServiceAccount("robot-a"))
	if err := utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=false", features.ProjectSuspension)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=true", features.ProjectSuspension))
	}()

	reconcileProject(t, r, "acme")
	if len(*reached) != 0 {
		t.Errorf("reached a project control plane with the feature gate off: %v", *reached)
	}
	if cond := suspendedCondition(t, pc, "robot-a"); cond != nil {
		t.Errorf("Suspended condition = %+v, want none with the feature gate off", cond)
	}
}

func TestProjectServiceAccountSuspension_MissingProjectIsIgnored(t *testing.T) {
	r, _, reached := newServiceAccountSuspensionTest(t, suspensionTestProject("other", true, nil))
	reconcileProject(t, r, "gone")
	if len(*reached) != 0 {
		t.Errorf("reached a control plane for a missing project: %v", *reached)
	}
}

func TestProjectServiceAccountSuspension_Predicates(t *testing.T) {
	never := suspensionTestProject("acme", true, nil)
	on, off := true, false
	suspended := suspensionTestProject("acme", true, &on, resourcemanagerv1alpha1.ReasonFraud)
	lifted := suspensionTestProject("acme", true, &off)
	moreReasons := suspensionTestProject("acme", true, &on, resourcemanagerv1alpha1.ReasonFraud, resourcemanagerv1alpha1.ReasonAbuse)

	if everSuspended(never) {
		t.Error("a never-suspended project must be skipped")
	}
	for name, tc := range map[string]struct {
		oldP, newP *resourcemanagerv1alpha1.Project
		want       bool
	}{
		"suspended":            {never, suspended, true},
		"lifted":               {suspended, lifted, true},
		"new reason":           {suspended, moreReasons, true},
		"unrelated change":     {suspended, suspended.DeepCopy(), false},
		"never suspended edit": {never, never.DeepCopy(), false},
	} {
		t.Run(name, func(t *testing.T) {
			got := (everSuspended(tc.oldP) || everSuspended(tc.newP)) && suspensionChanged(tc.oldP, tc.newP)
			if got != tc.want {
				t.Errorf("update passes = %v, want %v", got, tc.want)
			}
		})
	}
}

// The recheck repairs what Project changes alone miss: a ServiceAccount
// created during the suspension, and a mark another writer erased.
func TestProjectServiceAccountSuspension_RecheckRepairsMissedMarks(t *testing.T) {
	suspended := true
	project := suspensionTestProject("acme", true, &suspended, resourcemanagerv1alpha1.ReasonAbuse)
	r, pc, _ := newServiceAccountSuspensionTest(t, project, suspensionTestServiceAccount("robot-a"))
	ctx := context.Background()

	reconcileProject(t, r, "acme")

	// A ServiceAccount created during the suspension, for example by
	// system:masters, which the admission plugin exempts.
	if err := pc.Create(ctx, suspensionTestServiceAccount("robot-late")); err != nil {
		t.Fatal(err)
	}
	// A writer that replaces status.conditions and drops the mark.
	var erased iamv1alpha1.ServiceAccount
	if err := pc.Get(ctx, types.NamespacedName{Name: "robot-a"}, &erased); err != nil {
		t.Fatal(err)
	}
	erased.Status.Conditions = []metav1.Condition{readyServiceAccountCondition}
	if err := pc.Status().Update(ctx, &erased); err != nil {
		t.Fatal(err)
	}

	// The requeue the first reconcile scheduled.
	reconcileProject(t, r, "acme")

	for _, name := range []string{"robot-a", "robot-late"} {
		if cond := suspendedCondition(t, pc, name); cond == nil || cond.Status != metav1.ConditionTrue {
			t.Errorf("%s: Suspended condition = %+v, want True after the recheck", name, cond)
		}
	}
}

// A conflicting write from another writer, such as the auth provider, fails
// the reconcile instead of overwriting; the retry reads fresh data and keeps
// that writer's conditions.
func TestProjectServiceAccountSuspension_ConflictRetryKeepsOtherWritersConditions(t *testing.T) {
	if err := utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=true", features.ProjectSuspension)); err != nil {
		t.Fatal(err)
	}
	suspended := true
	project := suspensionTestProject("acme", true, &suspended, resourcemanagerv1alpha1.ReasonAbuse)
	scheme := getTestScheme()
	core := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project).WithStatusSubresource(project).Build()

	otherWriter := metav1.Condition{Type: "ProviderSynced", Status: metav1.ConditionTrue, Reason: "Synced", LastTransitionTime: metav1.Now()}
	raced := false
	pc := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(suspensionTestServiceAccount("robot-a", readyServiceAccountCondition)).
		WithStatusSubresource(&iamv1alpha1.ServiceAccount{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if !raced {
					// The other writer lands between this controller's list
					// and its patch.
					raced = true
					var sa iamv1alpha1.ServiceAccount
					if err := c.Get(ctx, client.ObjectKeyFromObject(obj), &sa); err != nil {
						return err
					}
					apimeta.SetStatusCondition(&sa.Status.Conditions, otherWriter)
					if err := c.Status().Update(ctx, &sa); err != nil {
						return err
					}
				}
				return c.SubResource(subResource).Patch(ctx, obj, patch, opts...)
			},
		}).Build()

	r := &ProjectServiceAccountSuspensionController{
		Client:        core,
		ProjectClient: func(string) (client.Client, error) { return pc, nil },
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "acme"}}

	_, err := r.Reconcile(context.Background(), req)
	if !apierrors.IsConflict(err) {
		t.Fatalf("first Reconcile() error = %v, want a conflict", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("retry Reconcile() error = %v", err)
	}

	var sa iamv1alpha1.ServiceAccount
	if err := pc.Get(context.Background(), types.NamespacedName{Name: "robot-a"}, &sa); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Ready", "ProviderSynced", iamv1alpha1.ServiceAccountSuspended} {
		if apimeta.FindStatusCondition(sa.Status.Conditions, want) == nil {
			t.Errorf("condition %q missing after the retry: %+v", want, sa.Status.Conditions)
		}
	}
}
