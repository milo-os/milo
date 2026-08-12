package resourcemanager

import (
	"context"
	"fmt"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

const testEmailNamespace = "milo-system"

func newSuspendedProject(name, orgName string, suspendedSince time.Time) *resourcemanagerv1alpha1.Project {
	return &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  types.UID(name + "-uid"),
		},
		Spec: resourcemanagerv1alpha1.ProjectSpec{
			OwnerRef: resourcemanagerv1alpha1.OwnerReference{
				Kind: "Organization",
				Name: orgName,
			},
		},
		Status: resourcemanagerv1alpha1.ProjectStatus{
			Conditions: []metav1.Condition{
				{
					Type:               resourcemanagerv1alpha1.ProjectSuspended,
					Status:             metav1.ConditionTrue,
					Reason:             resourcemanagerv1alpha1.ProjectSuspendedReason,
					LastTransitionTime: metav1.NewTime(suspendedSince),
				},
			},
		},
	}
}

func newTestOrganization(name string) *resourcemanagerv1alpha1.Organization {
	return &resourcemanagerv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resourcemanagerv1alpha1.OrganizationSpec{
			ContactInfo: &resourcemanagerv1alpha1.OrganizationContactInfo{
				Email: "owner@example.com",
				Name:  "Owner",
			},
		},
	}
}

// buildExcludedReasonsSet mirrors the set the controller builds in
// SetupWithManager, so tests can construct a controller with a fully initialised
// warningExcludedReasonsSet without spinning up a manager.
func buildExcludedReasonsSet(reasons []resourcemanagerv1alpha1.ProjectSuspensionReason) map[resourcemanagerv1alpha1.ProjectSuspensionReason]struct{} {
	set := make(map[resourcemanagerv1alpha1.ProjectSuspensionReason]struct{}, len(reasons))
	for _, reason := range reasons {
		set[reason] = struct{}{}
	}
	return set
}

func TestProjectSuspensionEscalationController_Reconcile(t *testing.T) {
	scheme := getTestScheme()

	if err := utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=true", features.ProjectSuspension)); err != nil {
		t.Fatalf("failed to enable feature gate: %v", err)
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-project"}}

	t.Run("feature gate disabled", func(t *testing.T) {
		if err := utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=false", features.ProjectSuspension)); err != nil {
			t.Fatalf("failed to disable feature gate: %v", err)
		}
		defer utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=true", features.ProjectSuspension))

		project := newSuspendedProject("test-project", "test-org", time.Now())
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).Build()

		r := &ProjectSuspensionEscalationController{
			Client:                  c,
			EventRecorder:           record.NewFakeRecorder(100),
			RetentionWindowDays:     30,
			notificationCheckpoints: computeNotificationCheckpoints(30, nil),
		}
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var got resourcemanagerv1alpha1.Project
		if err := c.Get(context.Background(), client.ObjectKey{Name: "test-project"}, &got); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}
		if got.Status.SuspensionEscalation != nil {
			t.Errorf("expected no escalation status when feature gate is disabled, got %+v", got.Status.SuspensionEscalation)
		}
	})

	t.Run("schedules escalation and sends immediate notice on first suspension", func(t *testing.T) {
		ctx := context.Background()
		suspendedSince := time.Now().Add(-1 * time.Hour)
		project := newSuspendedProject("test-project", "test-org", suspendedSince)
		org := newTestOrganization("test-org")

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, org).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).Build()

		fakeRecorder := record.NewFakeRecorder(100)
		r := &ProjectSuspensionEscalationController{
			Client:                    c,
			EventRecorder:             fakeRecorder,
			RetentionWindowDays:       30,
			NotificationDaysRemaining: []int{7, 3, 1},
			EmailTemplateName:         "deletion-warning",
			EmailNamespace:            testEmailNamespace,
			notificationCheckpoints:   computeNotificationCheckpoints(30, []int{7, 3, 1}),
		}

		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var got resourcemanagerv1alpha1.Project
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &got); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}

		if got.Status.SuspensionEscalation == nil {
			t.Fatal("expected SuspensionEscalation status to be set")
		}
		wantDeletionAt := suspendedSince.Add(30 * 24 * time.Hour)
		if got.Status.SuspensionEscalation.DeletionAt.Time.Sub(wantDeletionAt).Abs() > time.Second {
			t.Errorf("expected deletionAt around %v, got %v", wantDeletionAt, got.Status.SuspensionEscalation.DeletionAt.Time)
		}
		if len(got.Status.SuspensionEscalation.NotifiedDaysRemaining) != 1 || got.Status.SuspensionEscalation.NotifiedDaysRemaining[0] != 30 {
			t.Errorf("expected immediate 30-day notice recorded, got %v", got.Status.SuspensionEscalation.NotifiedDaysRemaining)
		}

		cond := apimeta.FindStatusCondition(got.Status.Conditions, resourcemanagerv1alpha1.ProjectPendingDeletion)
		if cond == nil || cond.Status != metav1.ConditionTrue {
			t.Errorf("expected PendingDeletion condition True, got %+v", cond)
		}

		var emails notificationv1alpha1.EmailList
		if err := c.List(ctx, &emails, client.InNamespace(testEmailNamespace)); err != nil {
			t.Fatalf("failed to list emails: %v", err)
		}
		if len(emails.Items) != 1 {
			t.Fatalf("expected 1 warning email, got %d", len(emails.Items))
		}
	})

	t.Run("re-reconcile does not duplicate the same checkpoint", func(t *testing.T) {
		ctx := context.Background()
		suspendedSince := time.Now().Add(-1 * time.Hour)
		project := newSuspendedProject("test-project", "test-org", suspendedSince)
		org := newTestOrganization("test-org")

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, org).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).Build()

		r := &ProjectSuspensionEscalationController{
			Client:                    c,
			EventRecorder:             record.NewFakeRecorder(100),
			RetentionWindowDays:       30,
			NotificationDaysRemaining: []int{7, 3, 1},
			EmailTemplateName:         "deletion-warning",
			EmailNamespace:            testEmailNamespace,
			notificationCheckpoints:   computeNotificationCheckpoints(30, []int{7, 3, 1}),
		}

		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error on first reconcile: %v", err)
		}
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error on second reconcile: %v", err)
		}

		var emails notificationv1alpha1.EmailList
		if err := c.List(ctx, &emails, client.InNamespace(testEmailNamespace)); err != nil {
			t.Fatalf("failed to list emails: %v", err)
		}
		if len(emails.Items) != 1 {
			t.Fatalf("expected exactly 1 warning email after re-reconcile, got %d", len(emails.Items))
		}
	})

	t.Run("sends reminder when a lower checkpoint is crossed", func(t *testing.T) {
		ctx := context.Background()
		suspendedSince := time.Now().Add(-27 * 24 * time.Hour) // 3 days remaining of a 30-day window
		project := newSuspendedProject("test-project", "test-org", suspendedSince)
		project.Status.SuspensionEscalation = &resourcemanagerv1alpha1.ProjectSuspensionEscalationStatus{
			DeletionAt:            metav1.NewTime(suspendedSince.Add(30 * 24 * time.Hour)),
			NotifiedDaysRemaining: []int32{30, 7},
		}
		org := newTestOrganization("test-org")

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, org).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).Build()

		r := &ProjectSuspensionEscalationController{
			Client:                    c,
			EventRecorder:             record.NewFakeRecorder(100),
			RetentionWindowDays:       30,
			NotificationDaysRemaining: []int{7, 3, 1},
			EmailTemplateName:         "deletion-warning",
			EmailNamespace:            testEmailNamespace,
			notificationCheckpoints:   computeNotificationCheckpoints(30, []int{7, 3, 1}),
		}

		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var got resourcemanagerv1alpha1.Project
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &got); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}
		if !containsInt32(got.Status.SuspensionEscalation.NotifiedDaysRemaining, 3) {
			t.Errorf("expected 3-day checkpoint recorded, got %v", got.Status.SuspensionEscalation.NotifiedDaysRemaining)
		}

		var emails notificationv1alpha1.EmailList
		if err := c.List(ctx, &emails, client.InNamespace(testEmailNamespace)); err != nil {
			t.Fatalf("failed to list emails: %v", err)
		}
		if len(emails.Items) != 1 {
			t.Fatalf("expected 1 new warning email for the crossed checkpoint, got %d", len(emails.Items))
		}
	})

	t.Run("deletes the project once the retention window elapses", func(t *testing.T) {
		ctx := context.Background()
		suspendedSince := time.Now().Add(-31 * 24 * time.Hour)
		project := newSuspendedProject("test-project", "test-org", suspendedSince)
		project.Status.SuspensionEscalation = &resourcemanagerv1alpha1.ProjectSuspensionEscalationStatus{
			DeletionAt:            metav1.NewTime(suspendedSince.Add(30 * 24 * time.Hour)),
			NotifiedDaysRemaining: []int32{30, 7, 3, 1},
		}
		org := newTestOrganization("test-org")

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, org).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).Build()

		r := &ProjectSuspensionEscalationController{
			Client:                    c,
			EventRecorder:             record.NewFakeRecorder(100),
			RetentionWindowDays:       30,
			NotificationDaysRemaining: []int{7, 3, 1},
			EmailTemplateName:         "deletion-warning",
			EmailNamespace:            testEmailNamespace,
			notificationCheckpoints:   computeNotificationCheckpoints(30, []int{7, 3, 1}),
		}

		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var got resourcemanagerv1alpha1.Project
		err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &got)
		if !apierrors.IsNotFound(err) {
			t.Fatalf("expected project to be deleted, got err=%v obj=%+v", err, got)
		}
	})

	t.Run("reinstatement clears escalation state", func(t *testing.T) {
		ctx := context.Background()
		project := &resourcemanagerv1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{Name: "test-project", UID: types.UID("test-project-uid")},
			Spec: resourcemanagerv1alpha1.ProjectSpec{
				OwnerRef: resourcemanagerv1alpha1.OwnerReference{Kind: "Organization", Name: "test-org"},
			},
			Status: resourcemanagerv1alpha1.ProjectStatus{
				Conditions: []metav1.Condition{
					{
						Type:   resourcemanagerv1alpha1.ProjectSuspended,
						Status: metav1.ConditionFalse,
						Reason: resourcemanagerv1alpha1.ProjectActiveReason,
					},
					{
						Type:   resourcemanagerv1alpha1.ProjectPendingDeletion,
						Status: metav1.ConditionTrue,
						Reason: resourcemanagerv1alpha1.ProjectPendingDeletionReason,
					},
				},
				SuspensionEscalation: &resourcemanagerv1alpha1.ProjectSuspensionEscalationStatus{
					DeletionAt:            metav1.NewTime(time.Now().Add(24 * time.Hour)),
					NotifiedDaysRemaining: []int32{30},
				},
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).Build()

		fakeRecorder := record.NewFakeRecorder(100)
		r := &ProjectSuspensionEscalationController{Client: c, EventRecorder: fakeRecorder, RetentionWindowDays: 30}

		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var got resourcemanagerv1alpha1.Project
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &got); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}
		if got.Status.SuspensionEscalation != nil {
			t.Errorf("expected SuspensionEscalation to be cleared, got %+v", got.Status.SuspensionEscalation)
		}
		cond := apimeta.FindStatusCondition(got.Status.Conditions, resourcemanagerv1alpha1.ProjectPendingDeletion)
		if cond == nil || cond.Status != metav1.ConditionFalse {
			t.Errorf("expected PendingDeletion condition False, got %+v", cond)
		}
	})

	t.Run("re-suspension after reinstatement sends a fresh email instead of reusing the old one", func(t *testing.T) {
		ctx := context.Background()
		firstSuspendedAt := time.Now().Add(-1 * time.Hour)
		project := newSuspendedProject("test-project", "test-org", firstSuspendedAt)
		org := newTestOrganization("test-org")

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, org).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).Build()

		r := &ProjectSuspensionEscalationController{
			Client:                    c,
			EventRecorder:             record.NewFakeRecorder(100),
			RetentionWindowDays:       30,
			NotificationDaysRemaining: []int{7, 3, 1},
			EmailTemplateName:         "deletion-warning",
			EmailNamespace:            testEmailNamespace,
			notificationCheckpoints:   computeNotificationCheckpoints(30, []int{7, 3, 1}),
		}

		// First suspension: sends the immediate 30-day notice.
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error on first suspension: %v", err)
		}

		var afterFirst notificationv1alpha1.EmailList
		if err := c.List(ctx, &afterFirst, client.InNamespace(testEmailNamespace)); err != nil {
			t.Fatalf("failed to list emails after first suspension: %v", err)
		}
		if len(afterFirst.Items) != 1 {
			t.Fatalf("expected 1 warning email after first suspension, got %d", len(afterFirst.Items))
		}
		firstEmailName := afterFirst.Items[0].Name

		// Reinstate: flip Suspended to False, which clears the escalation state.
		var got resourcemanagerv1alpha1.Project
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &got); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}
		apimeta.SetStatusCondition(&got.Status.Conditions, metav1.Condition{
			Type:    resourcemanagerv1alpha1.ProjectSuspended,
			Status:  metav1.ConditionFalse,
			Reason:  resourcemanagerv1alpha1.ProjectActiveReason,
			Message: "reinstated",
		})
		if err := c.Status().Update(ctx, &got); err != nil {
			t.Fatalf("failed to reinstate project: %v", err)
		}
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error on reinstatement: %v", err)
		}

		// Re-suspend: flip Suspended back to True with a new transition time.
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &got); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}
		apimeta.SetStatusCondition(&got.Status.Conditions, metav1.Condition{
			Type:    resourcemanagerv1alpha1.ProjectSuspended,
			Status:  metav1.ConditionTrue,
			Reason:  resourcemanagerv1alpha1.ProjectSuspendedReason,
			Message: "suspended again",
		})
		if err := c.Status().Update(ctx, &got); err != nil {
			t.Fatalf("failed to re-suspend project: %v", err)
		}
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error on second suspension: %v", err)
		}

		var afterSecond notificationv1alpha1.EmailList
		if err := c.List(ctx, &afterSecond, client.InNamespace(testEmailNamespace)); err != nil {
			t.Fatalf("failed to list emails after second suspension: %v", err)
		}
		if len(afterSecond.Items) != 2 {
			t.Fatalf("expected a second, distinct warning email after re-suspension, got %d email(s): %v", len(afterSecond.Items), afterSecond.Items)
		}
		for _, e := range afterSecond.Items {
			if e.Name == firstEmailName {
				continue
			}
			return // found the new, distinctly-named email
		}
		t.Fatalf("expected a new email distinct from %q, got only %v", firstEmailName, afterSecond.Items)
	})

	t.Run("skips and retries when organization has no contact info", func(t *testing.T) {
		ctx := context.Background()
		suspendedSince := time.Now().Add(-1 * time.Hour)
		project := newSuspendedProject("test-project", "test-org", suspendedSince)
		org := &resourcemanagerv1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{Name: "test-org"}}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, org).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).Build()

		fakeRecorder := record.NewFakeRecorder(100)
		r := &ProjectSuspensionEscalationController{
			Client:                  c,
			EventRecorder:           fakeRecorder,
			RetentionWindowDays:     30,
			EmailTemplateName:       "deletion-warning",
			EmailNamespace:          testEmailNamespace,
			notificationCheckpoints: computeNotificationCheckpoints(30, nil),
		}

		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var got resourcemanagerv1alpha1.Project
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &got); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}
		if len(got.Status.SuspensionEscalation.NotifiedDaysRemaining) != 0 {
			t.Errorf("expected no checkpoint recorded without contact info, got %v", got.Status.SuspensionEscalation.NotifiedDaysRemaining)
		}

		var emails notificationv1alpha1.EmailList
		if err := c.List(ctx, &emails, client.InNamespace(testEmailNamespace)); err != nil {
			t.Fatalf("failed to list emails: %v", err)
		}
		if len(emails.Items) != 0 {
			t.Fatalf("expected no warning email without contact info, got %d", len(emails.Items))
		}
	})

	t.Run("excluded-reason suspension schedules escalation but sends no warning email", func(t *testing.T) {
		ctx := context.Background()
		suspendedSince := time.Now().Add(-1 * time.Hour)
		project := newSuspendedProject("test-project", "test-org", suspendedSince)
		project.Status.Suspensions = []resourcemanagerv1alpha1.ProjectSuspensionInfo{
			{
				Reason:      resourcemanagerv1alpha1.ReasonAbuse,
				SuspendedAt: metav1.NewTime(suspendedSince),
			},
		}
		org := newTestOrganization("test-org")

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, org).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).Build()

		r := &ProjectSuspensionEscalationController{
			Client:                    c,
			EventRecorder:             record.NewFakeRecorder(100),
			RetentionWindowDays:       30,
			NotificationDaysRemaining: []int{7, 3, 1},
			EmailTemplateName:         "deletion-warning",
			EmailNamespace:            testEmailNamespace,
			WarningExcludedReasons: []resourcemanagerv1alpha1.ProjectSuspensionReason{
				resourcemanagerv1alpha1.ReasonFraud,
				resourcemanagerv1alpha1.ReasonAbuse,
			},
			warningExcludedReasonsSet: buildExcludedReasonsSet([]resourcemanagerv1alpha1.ProjectSuspensionReason{
				resourcemanagerv1alpha1.ReasonFraud,
				resourcemanagerv1alpha1.ReasonAbuse,
			}),
			notificationCheckpoints: computeNotificationCheckpoints(30, []int{7, 3, 1}),
		}

		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var got resourcemanagerv1alpha1.Project
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &got); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}
		if got.Status.SuspensionEscalation == nil {
			t.Fatal("expected SuspensionEscalation to be scheduled even for an excluded reason")
		}
		cond := apimeta.FindStatusCondition(got.Status.Conditions, resourcemanagerv1alpha1.ProjectPendingDeletion)
		if cond == nil || cond.Status != metav1.ConditionTrue {
			t.Errorf("expected PendingDeletion condition True, got %+v", cond)
		}
		if len(got.Status.SuspensionEscalation.NotifiedDaysRemaining) != 0 {
			t.Errorf("expected no checkpoint recorded for an excluded reason, got %v", got.Status.SuspensionEscalation.NotifiedDaysRemaining)
		}

		var emails notificationv1alpha1.EmailList
		if err := c.List(ctx, &emails, client.InNamespace(testEmailNamespace)); err != nil {
			t.Fatalf("failed to list emails: %v", err)
		}
		if len(emails.Items) != 0 {
			t.Fatalf("expected no warning email for an excluded reason, got %d", len(emails.Items))
		}
	})

	t.Run("all-excluded multi-suspension project sends no warning email", func(t *testing.T) {
		ctx := context.Background()
		suspendedSince := time.Now().Add(-1 * time.Hour)
		project := newSuspendedProject("test-project", "test-org", suspendedSince)
		project.Status.Suspensions = []resourcemanagerv1alpha1.ProjectSuspensionInfo{
			{Reason: resourcemanagerv1alpha1.ReasonFraud, SuspendedAt: metav1.NewTime(suspendedSince)},
			{Reason: resourcemanagerv1alpha1.ReasonAbuse, SuspendedAt: metav1.NewTime(suspendedSince)},
		}
		org := newTestOrganization("test-org")

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, org).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).Build()

		r := &ProjectSuspensionEscalationController{
			Client:                    c,
			EventRecorder:             record.NewFakeRecorder(100),
			RetentionWindowDays:       30,
			NotificationDaysRemaining: []int{7, 3, 1},
			EmailTemplateName:         "deletion-warning",
			EmailNamespace:            testEmailNamespace,
			WarningExcludedReasons: []resourcemanagerv1alpha1.ProjectSuspensionReason{
				resourcemanagerv1alpha1.ReasonFraud,
				resourcemanagerv1alpha1.ReasonAbuse,
			},
			warningExcludedReasonsSet: buildExcludedReasonsSet([]resourcemanagerv1alpha1.ProjectSuspensionReason{
				resourcemanagerv1alpha1.ReasonFraud,
				resourcemanagerv1alpha1.ReasonAbuse,
			}),
			notificationCheckpoints: computeNotificationCheckpoints(30, []int{7, 3, 1}),
		}

		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var emails notificationv1alpha1.EmailList
		if err := c.List(ctx, &emails, client.InNamespace(testEmailNamespace)); err != nil {
			t.Fatalf("failed to list emails: %v", err)
		}
		if len(emails.Items) != 0 {
			t.Fatalf("expected no warning email when all reasons are excluded, got %d", len(emails.Items))
		}
	})

	t.Run("mixed reasons send a warning when any reason is eligible", func(t *testing.T) {
		ctx := context.Background()
		suspendedSince := time.Now().Add(-1 * time.Hour)
		project := newSuspendedProject("test-project", "test-org", suspendedSince)
		project.Status.Suspensions = []resourcemanagerv1alpha1.ProjectSuspensionInfo{
			{Reason: resourcemanagerv1alpha1.ReasonFraud, SuspendedAt: metav1.NewTime(suspendedSince)},
			{Reason: resourcemanagerv1alpha1.ReasonBilling, SuspendedAt: metav1.NewTime(suspendedSince)},
		}
		org := newTestOrganization("test-org")

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, org).
			WithStatusSubresource(&resourcemanagerv1alpha1.Project{}).Build()

		r := &ProjectSuspensionEscalationController{
			Client:                    c,
			EventRecorder:             record.NewFakeRecorder(100),
			RetentionWindowDays:       30,
			NotificationDaysRemaining: []int{7, 3, 1},
			EmailTemplateName:         "deletion-warning",
			EmailNamespace:            testEmailNamespace,
			WarningExcludedReasons: []resourcemanagerv1alpha1.ProjectSuspensionReason{
				resourcemanagerv1alpha1.ReasonFraud,
				resourcemanagerv1alpha1.ReasonAbuse,
			},
			warningExcludedReasonsSet: buildExcludedReasonsSet([]resourcemanagerv1alpha1.ProjectSuspensionReason{
				resourcemanagerv1alpha1.ReasonFraud,
				resourcemanagerv1alpha1.ReasonAbuse,
			}),
			notificationCheckpoints: computeNotificationCheckpoints(30, []int{7, 3, 1}),
		}

		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var got resourcemanagerv1alpha1.Project
		if err := c.Get(ctx, client.ObjectKey{Name: "test-project"}, &got); err != nil {
			t.Fatalf("failed to get project: %v", err)
		}
		if len(got.Status.SuspensionEscalation.NotifiedDaysRemaining) == 0 {
			t.Errorf("expected the immediate notice to be recorded when any reason is eligible, got %v", got.Status.SuspensionEscalation.NotifiedDaysRemaining)
		}

		var emails notificationv1alpha1.EmailList
		if err := c.List(ctx, &emails, client.InNamespace(testEmailNamespace)); err != nil {
			t.Fatalf("failed to list emails: %v", err)
		}
		if len(emails.Items) != 1 {
			t.Fatalf("expected 1 warning email when any reason is eligible, got %d", len(emails.Items))
		}
	})
}

func TestProjectSuspensionEscalationController_validateConfig(t *testing.T) {
	tests := []struct {
		name                      string
		retentionWindowDays       int
		notificationDaysRemaining []int
		wantErr                   bool
	}{
		{name: "valid config", retentionWindowDays: 30, notificationDaysRemaining: []int{7, 3, 1}},
		{name: "valid with no notification days", retentionWindowDays: 30, notificationDaysRemaining: nil},
		{name: "zero retention window", retentionWindowDays: 0, notificationDaysRemaining: []int{7, 3, 1}, wantErr: true},
		{name: "negative retention window", retentionWindowDays: -5, notificationDaysRemaining: []int{7, 3, 1}, wantErr: true},
		{name: "zero notification day", retentionWindowDays: 30, notificationDaysRemaining: []int{7, 0, 1}, wantErr: true},
		{name: "negative notification day", retentionWindowDays: 30, notificationDaysRemaining: []int{7, -3, 1}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ProjectSuspensionEscalationController{
				RetentionWindowDays:       tt.retentionWindowDays,
				NotificationDaysRemaining: tt.notificationDaysRemaining,
			}
			err := r.validateConfig()
			if tt.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestProjectSuspensionEscalationController_validateConfig_WarningExcludedReasons(t *testing.T) {
	tests := []struct {
		name            string
		warningExcluded []resourcemanagerv1alpha1.ProjectSuspensionReason
		wantErr         bool
	}{
		{name: "valid excluded reasons", warningExcluded: []resourcemanagerv1alpha1.ProjectSuspensionReason{
			resourcemanagerv1alpha1.ReasonFraud, resourcemanagerv1alpha1.ReasonAbuse,
		}},
		{name: "all five known reasons accepted", warningExcluded: []resourcemanagerv1alpha1.ProjectSuspensionReason{
			resourcemanagerv1alpha1.ReasonFraud, resourcemanagerv1alpha1.ReasonAbuse,
			resourcemanagerv1alpha1.ReasonBilling, resourcemanagerv1alpha1.ReasonCompliance,
			resourcemanagerv1alpha1.ReasonAdministrative,
		}},
		{name: "empty excluded reasons is valid", warningExcluded: nil},
		{name: "unknown excluded reason rejected", warningExcluded: []resourcemanagerv1alpha1.ProjectSuspensionReason{"NotAReason"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ProjectSuspensionEscalationController{
				RetentionWindowDays:    30,
				WarningExcludedReasons: tt.warningExcluded,
			}
			err := r.validateConfig()
			if tt.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
