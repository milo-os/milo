package resourcemanager

import (
	"context"
	"fmt"
	"strings"
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

func TestProjectSuspensionController_Reconcile(t *testing.T) {
	scheme := getTestScheme()

	// Enable feature gate for reconcile tests by default
	err := utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=true", features.ProjectSuspension))
	if err != nil {
		t.Fatalf("failed to enable feature gate: %v", err)
	}

	t.Run("feature gate disabled", func(t *testing.T) {
		err := utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=false", features.ProjectSuspension))
		if err != nil {
			t.Fatalf("failed to disable feature gate: %v", err)
		}
		defer utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=true", features.ProjectSuspension))

		ctx := context.Background()
		project := &resourcemanagerv1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-project",
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(project).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).
			Build()

		r := &ProjectSuspensionPropagatorController{Client: c, EventEmitter: newFakeEventEmitter(100)}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: "test-project",
			},
		}

		_, err = r.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var updatedProject resourcemanagerv1alpha1.Project
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &updatedProject); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}
		// Status condition should not be set (should be empty since propagator was inert)
		cond := apimeta.FindStatusCondition(updatedProject.Status.Conditions, resourcemanagerv1alpha1.ProjectSuspended)
		if cond != nil {
			t.Errorf("expected no Suspended condition when feature gate is disabled, got %v", cond)
		}
	})

	t.Run("no suspensions", func(t *testing.T) {
		ctx := context.Background()
		project := &resourcemanagerv1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-project",
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(project).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).
			WithIndex(&resourcemanagerv1alpha1.ProjectSuspension{}, projectRefNameIndex, func(rawObj client.Object) []string {
				obj := rawObj.(*resourcemanagerv1alpha1.ProjectSuspension)
				return []string{obj.Spec.ProjectRef.Name}
			}).
			Build()

		fakeRecorder := newFakeEventEmitter(100)
		r := &ProjectSuspensionPropagatorController{Client: c, EventEmitter: fakeRecorder}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: "test-project",
			},
		}

		_, err := r.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var updatedProject resourcemanagerv1alpha1.Project
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &updatedProject); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}

		if len(updatedProject.Status.Suspensions) != 0 {
			t.Errorf("expected 0 suspensions, got %v", updatedProject.Status.Suspensions)
		}

		cond := apimeta.FindStatusCondition(updatedProject.Status.Conditions, resourcemanagerv1alpha1.ProjectSuspended)
		if cond == nil {
			t.Fatal("expected Suspended condition, got nil")
		}
		if cond.Status != metav1.ConditionFalse {
			t.Errorf("expected Suspended condition status False, got %v", cond.Status)
		}
		if cond.Reason != resourcemanagerv1alpha1.ProjectActiveReason {
			t.Errorf("expected Suspended condition reason %v, got %v", resourcemanagerv1alpha1.ProjectActiveReason, cond.Reason)
		}
	})

	t.Run("with active suspensions", func(t *testing.T) {
		ctx := context.Background()
		project := &resourcemanagerv1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-project",
			},
		}
		suspension1 := &resourcemanagerv1alpha1.ProjectSuspension{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "suspension-b",
				CreationTimestamp: metav1.Now(),
			},
			Spec: resourcemanagerv1alpha1.ProjectSuspensionSpec{
				ProjectRef: resourcemanagerv1alpha1.ProjectReference{
					Name: "test-project",
				},
				Reason:             resourcemanagerv1alpha1.ReasonAbuse,
				ReinstateAuthority: resourcemanagerv1alpha1.AuthorityOperator,
			},
			Status: resourcemanagerv1alpha1.ProjectSuspensionStatus{
				Phase: resourcemanagerv1alpha1.PhaseActive,
			},
		}
		suspension2 := &resourcemanagerv1alpha1.ProjectSuspension{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "suspension-a",
				CreationTimestamp: metav1.Now(),
			},
			Spec: resourcemanagerv1alpha1.ProjectSuspensionSpec{
				ProjectRef: resourcemanagerv1alpha1.ProjectReference{
					Name: "test-project",
				},
				Reason:             resourcemanagerv1alpha1.ReasonBilling,
				ReinstateAuthority: resourcemanagerv1alpha1.AuthorityOperator,
			},
			// Status.Phase empty defaults to Active
		}
		suspensionOther := &resourcemanagerv1alpha1.ProjectSuspension{
			ObjectMeta: metav1.ObjectMeta{
				Name: "suspension-other",
			},
			Spec: resourcemanagerv1alpha1.ProjectSuspensionSpec{
				ProjectRef: resourcemanagerv1alpha1.ProjectReference{
					Name: "other-project",
				},
				Reason:             resourcemanagerv1alpha1.ReasonBilling,
				ReinstateAuthority: resourcemanagerv1alpha1.AuthorityOperator,
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(project, suspension1, suspension2, suspensionOther).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).
			WithIndex(&resourcemanagerv1alpha1.ProjectSuspension{}, projectRefNameIndex, func(rawObj client.Object) []string {
				obj := rawObj.(*resourcemanagerv1alpha1.ProjectSuspension)
				return []string{obj.Spec.ProjectRef.Name}
			}).
			Build()

		fakeRecorder := newFakeEventEmitter(100)
		r := &ProjectSuspensionPropagatorController{Client: c, EventEmitter: fakeRecorder}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: "test-project",
			},
		}

		_, err := r.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var updatedProject resourcemanagerv1alpha1.Project
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &updatedProject); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}

		if len(updatedProject.Status.Suspensions) != 2 ||
			updatedProject.Status.Suspensions[0].Reason != resourcemanagerv1alpha1.ReasonAbuse ||
			updatedProject.Status.Suspensions[0].ReinstateAuthority != resourcemanagerv1alpha1.AuthorityOperator ||
			updatedProject.Status.Suspensions[0].SuspendedAt.IsZero() ||
			updatedProject.Status.Suspensions[1].Reason != resourcemanagerv1alpha1.ReasonBilling ||
			updatedProject.Status.Suspensions[1].ReinstateAuthority != resourcemanagerv1alpha1.AuthorityOperator ||
			updatedProject.Status.Suspensions[1].SuspendedAt.IsZero() {
			t.Errorf("unexpected suspensions: %+v", updatedProject.Status.Suspensions)
		}

		cond := apimeta.FindStatusCondition(updatedProject.Status.Conditions, resourcemanagerv1alpha1.ProjectSuspended)
		if cond == nil {
			t.Fatal("expected Suspended condition, got nil")
		}
		if cond.Status != metav1.ConditionTrue {
			t.Errorf("expected Suspended condition status True, got %v", cond.Status)
		}
		if cond.Reason != resourcemanagerv1alpha1.ProjectSuspendedReason {
			t.Errorf("expected Suspended condition reason %v, got %v", resourcemanagerv1alpha1.ProjectSuspendedReason, cond.Reason)
		}

		// Verify event was emitted, carrying suspension categories (Abuse,
		// Billing) but never the underlying ProjectSuspension resource
		// names ("suspension-a", "suspension-b") — those are internal admin
		// detail that must not reach consumer-facing activity feeds.
		select {
		case ev := <-fakeRecorder.Events:
			if !strings.Contains(ev, "Suspended") {
				t.Errorf("expected Suspended event, got: %s", ev)
			}
			if !strings.Contains(ev, "Abuse") || !strings.Contains(ev, "Billing") {
				t.Errorf("expected event to carry both suspension categories, got: %s", ev)
			}
			if strings.Contains(ev, "suspension-a") || strings.Contains(ev, "suspension-b") {
				t.Errorf("event must never contain ProjectSuspension resource names, got: %s", ev)
			}
		default:
			t.Error("expected event to be emitted, but got none")
		}
	})

	t.Run("with only lifted suspensions", func(t *testing.T) {
		ctx := context.Background()
		// Start project as already suspended so transitioning to False (active/reinstated) emits an event
		project := &resourcemanagerv1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-project",
			},
			Status: resourcemanagerv1alpha1.ProjectStatus{
				Conditions: []metav1.Condition{
					{
						Type:   resourcemanagerv1alpha1.ProjectSuspended,
						Status: metav1.ConditionTrue,
						Reason: resourcemanagerv1alpha1.ProjectSuspendedReason,
					},
				},
			},
		}
		suspension1 := &resourcemanagerv1alpha1.ProjectSuspension{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "suspension-lifted",
				CreationTimestamp: metav1.Now(),
			},
			Spec: resourcemanagerv1alpha1.ProjectSuspensionSpec{
				ProjectRef: resourcemanagerv1alpha1.ProjectReference{
					Name: "test-project",
				},
				Reason:             resourcemanagerv1alpha1.ReasonAbuse,
				ReinstateAuthority: resourcemanagerv1alpha1.AuthorityOperator,
			},
			Status: resourcemanagerv1alpha1.ProjectSuspensionStatus{
				Phase: resourcemanagerv1alpha1.PhaseLifted,
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(project, suspension1).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).
			WithIndex(&resourcemanagerv1alpha1.ProjectSuspension{}, projectRefNameIndex, func(rawObj client.Object) []string {
				obj := rawObj.(*resourcemanagerv1alpha1.ProjectSuspension)
				return []string{obj.Spec.ProjectRef.Name}
			}).
			Build()

		fakeRecorder := newFakeEventEmitter(100)
		r := &ProjectSuspensionPropagatorController{Client: c, EventEmitter: fakeRecorder}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: "test-project",
			},
		}

		_, err := r.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var updatedProject resourcemanagerv1alpha1.Project
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &updatedProject); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}

		// status.Suspensions only ever holds active suspensions (see
		// ProjectSuspensionInfo's doc comment) — a Lifted ProjectSuspension
		// must not appear here even though it still exists as a resource.
		if len(updatedProject.Status.Suspensions) != 0 {
			t.Errorf("expected 0 suspensions (Lifted suspensions are excluded), got %+v", updatedProject.Status.Suspensions)
		}

		cond := apimeta.FindStatusCondition(updatedProject.Status.Conditions, resourcemanagerv1alpha1.ProjectSuspended)
		if cond == nil {
			t.Fatal("expected Suspended condition, got nil")
		}
		if cond.Status != metav1.ConditionFalse {
			t.Errorf("expected Suspended condition status False, got %v", cond.Status)
		}
		if cond.Reason != resourcemanagerv1alpha1.ProjectActiveReason {
			t.Errorf("expected Suspended condition reason %v, got %v", resourcemanagerv1alpha1.ProjectActiveReason, cond.Reason)
		}

		// Verify Reinstated event was emitted
		select {
		case ev := <-fakeRecorder.Events:
			if !strings.Contains(ev, "Reinstated") {
				t.Errorf("expected Reinstated event, got: %s", ev)
			}
		default:
			t.Error("expected event to be emitted, but got none")
		}
	})

	t.Run("mix of active and lifted suspensions", func(t *testing.T) {
		ctx := context.Background()
		project := &resourcemanagerv1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-project",
			},
		}
		activeSuspension := &resourcemanagerv1alpha1.ProjectSuspension{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "suspension-active",
				CreationTimestamp: metav1.Now(),
			},
			Spec: resourcemanagerv1alpha1.ProjectSuspensionSpec{
				ProjectRef: resourcemanagerv1alpha1.ProjectReference{
					Name: "test-project",
				},
				Reason:             resourcemanagerv1alpha1.ReasonAbuse,
				ReinstateAuthority: resourcemanagerv1alpha1.AuthorityOperator,
			},
			Status: resourcemanagerv1alpha1.ProjectSuspensionStatus{
				Phase: resourcemanagerv1alpha1.PhaseActive,
			},
		}
		liftedSuspension := &resourcemanagerv1alpha1.ProjectSuspension{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "suspension-lifted",
				CreationTimestamp: metav1.Now(),
			},
			Spec: resourcemanagerv1alpha1.ProjectSuspensionSpec{
				ProjectRef: resourcemanagerv1alpha1.ProjectReference{
					Name: "test-project",
				},
				Reason:             resourcemanagerv1alpha1.ReasonBilling,
				ReinstateAuthority: resourcemanagerv1alpha1.AuthorityOperator,
			},
			Status: resourcemanagerv1alpha1.ProjectSuspensionStatus{
				Phase: resourcemanagerv1alpha1.PhaseLifted,
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(project, activeSuspension, liftedSuspension).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).
			WithIndex(&resourcemanagerv1alpha1.ProjectSuspension{}, projectRefNameIndex, func(rawObj client.Object) []string {
				obj := rawObj.(*resourcemanagerv1alpha1.ProjectSuspension)
				return []string{obj.Spec.ProjectRef.Name}
			}).
			Build()

		fakeRecorder := newFakeEventEmitter(100)
		r := &ProjectSuspensionPropagatorController{Client: c, EventEmitter: fakeRecorder}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: "test-project",
			},
		}

		_, err := r.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var updatedProject resourcemanagerv1alpha1.Project
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &updatedProject); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}

		// Only the active suspension should appear in status.Suspensions;
		// the lifted one must be excluded.
		if len(updatedProject.Status.Suspensions) != 1 ||
			updatedProject.Status.Suspensions[0].Reason != resourcemanagerv1alpha1.ReasonAbuse {
			t.Errorf("expected only the active (Abuse) suspension, got %+v", updatedProject.Status.Suspensions)
		}

		// Verify Suspended event was emitted, carrying only the active
		// suspension's category and never the ProjectSuspension resource
		// names ("suspension-active", "suspension-lifted").
		select {
		case ev := <-fakeRecorder.Events:
			if !strings.Contains(ev, "Suspended") {
				t.Errorf("expected Suspended event, got: %s", ev)
			}
			if !strings.Contains(ev, "Abuse") {
				t.Errorf("expected event to carry the suspension category, got: %s", ev)
			}
			if strings.Contains(ev, "Billing") {
				t.Errorf("expected the lifted suspension's category to be excluded, got: %s", ev)
			}
			if strings.Contains(ev, "suspension-active") || strings.Contains(ev, "suspension-lifted") {
				t.Errorf("event must never contain ProjectSuspension resource names, got: %s", ev)
			}
		default:
			t.Error("expected event to be emitted, but got none")
		}
	})
}

// fakeEventEmitter records emitted Project lifecycle events for assertions.
// Events are rendered "<reason> <note>" so tests can assert on either part
// with a single substring check.
type fakeEventEmitter struct {
	Events chan string
	err    error
	calls  int
}

func newFakeEventEmitter(buffer int) *fakeEventEmitter {
	return &fakeEventEmitter{Events: make(chan string, buffer)}
}

func (f *fakeEventEmitter) Emit(_ context.Context, project *resourcemanagerv1alpha1.Project, reason, note string) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.Events <- fmt.Sprintf("%s %s", reason, note)
	return nil
}

// Ensure the fake satisfies the interface the controller depends on.
var _ projectEventEmitter = (*fakeEventEmitter)(nil)

func TestRecordTransition_ContinuesWhenEmitFails(t *testing.T) {
	// A failed event must not fail the reconcile. By the time recordTransition
	// runs, the status patch has already succeeded, so returning an error here
	// would only cause a pointless re-reconcile — the emit is logged instead.
	emitter := newFakeEventEmitter(1)
	emitter.err = fmt.Errorf("activity backend unavailable")

	project := &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "project-123"},
	}

	recordTransition(context.Background(), emitter, project, false, true, []string{"Abuse"}, metav1.Time{})

	if emitter.calls != 1 {
		t.Errorf("expected one emit attempt, got %d", emitter.calls)
	}
	if len(emitter.Events) != 0 {
		t.Error("failed emit must not enqueue an event")
	}
}
