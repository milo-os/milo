package resourcemanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	legacyregistry "k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/component-base/metrics/testutil"
)

// reviewAnswers sets what the fake API server answers to each access review.
type reviewAnswers struct {
	canImpersonate bool
	userCanCreate  bool
}

// newAccessTestEmitter returns an emitter whose client answers access reviews
// from answers, and the reviews it was sent.
func newAccessTestEmitter(t *testing.T, answers reviewAnswers) (*ProjectEventEmitter, *[]runtime.Object) {
	t.Helper()

	e, _, cs := newTestEmitter(t, &rest.Config{Host: "https://milo.example"})
	var sent []runtime.Object

	cs.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		review := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		sent = append(sent, review)
		review.Status.Allowed = answers.canImpersonate
		return true, review, nil
	})
	cs.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		review := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sent = append(sent, review)
		review.Status.Allowed = answers.userCanCreate
		return true, review, nil
	})

	return e, &sent
}

func TestProjectEventEmitter_CheckAccess(t *testing.T) {
	tests := []struct {
		name      string
		answers   reviewAnswers
		wantError string
	}{
		{name: "allowed", answers: reviewAnswers{canImpersonate: true, userCanCreate: true}},
		{name: "controller cannot impersonate the user", answers: reviewAnswers{canImpersonate: false, userCanCreate: true}, wantError: "cannot impersonate"},
		{name: "user cannot create events", answers: reviewAnswers{canImpersonate: true, userCanCreate: false}, wantError: "cannot create events"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newAccessTestEmitter(t, tt.answers)

			err := e.CheckAccess(context.Background())

			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("expected access, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected an error containing %q, got %v", tt.wantError, err)
			}
			if !strings.Contains(err.Error(), DefaultProjectEventUser) {
				t.Errorf("expected the error to name the user %q, got %v", DefaultProjectEventUser, err)
			}
		})
	}
}

func TestProjectEventEmitter_CheckAccessAsksAboutTheRealRequest(t *testing.T) {
	e, sent := newAccessTestEmitter(t, reviewAnswers{canImpersonate: true, userCanCreate: true})

	if err := e.CheckAccess(context.Background()); err != nil {
		t.Fatalf("CheckAccess returned error: %v", err)
	}

	var ssar *authorizationv1.SelfSubjectAccessReview
	var sar *authorizationv1.SubjectAccessReview
	for _, obj := range *sent {
		switch review := obj.(type) {
		case *authorizationv1.SelfSubjectAccessReview:
			ssar = review
		case *authorizationv1.SubjectAccessReview:
			sar = review
		}
	}
	if ssar == nil || sar == nil {
		t.Fatalf("expected one review of each kind, got %d reviews", len(*sent))
	}

	if got := ssar.Spec.ResourceAttributes; got.Verb != "impersonate" || got.Resource != "users" || got.Name != DefaultProjectEventUser {
		t.Errorf("unexpected impersonation review: %+v", got)
	}

	if sar.Spec.User != DefaultProjectEventUser {
		t.Errorf("expected the create review for user %q, got %q", DefaultProjectEventUser, sar.Spec.User)
	}
	if got := sar.Spec.ResourceAttributes; got.Verb != "create" || got.Resource != "events" || got.Group != "" || got.Namespace != clusterScopedEventNamespace {
		t.Errorf("unexpected create review: %+v", got)
	}
}

func TestRecordProjectEventAccess(t *testing.T) {
	const user = "system:example:access-test-user"

	recordProjectEventAccess(user, nil)
	if got, _ := testutil.GetGaugeMetricValue(projectEventUserAuthorized.WithLabelValues(user)); got != 1 {
		t.Errorf("expected the gauge at 1 after a passing check, got %v", got)
	}

	recordProjectEventAccess(user, errors.New("denied"))
	if got, _ := testutil.GetGaugeMetricValue(projectEventUserAuthorized.WithLabelValues(user)); got != 0 {
		t.Errorf("expected the gauge at 0 after a failing check, got %v", got)
	}
}

func TestEventEmitFailedTotalStartsAtZeroForEachReason(t *testing.T) {
	// increase() cannot see a counter whose first sample is already 1, so each
	// reason's series must be exported at 0 before the first failure. Read the
	// registry rather than calling WithLabelValues, which would create the
	// series itself.
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	exported := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "milo_resourcemanager_project_lifecycle_event_emit_failed_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, label := range m.GetLabel() {
				if label.GetName() == "reason" {
					exported[label.GetValue()] = m.GetCounter().GetValue()
				}
			}
		}
	}

	for _, reason := range projectLifecycleEventReasons {
		value, ok := exported[reason]
		if !ok {
			t.Errorf("reason %q: series not exported", reason)
			continue
		}
		if value != 0 {
			t.Errorf("reason %q: expected 0 before any failure, got %v", reason, value)
		}
	}
}
