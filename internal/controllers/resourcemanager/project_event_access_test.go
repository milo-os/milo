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

// Keys for the reviews CheckAccess sends, used to make the fake API server
// refuse one of them.
const (
	reviewImpersonateUser       = "impersonate user"
	reviewImpersonateParentType = "impersonate parent-type"
	reviewImpersonateParentName = "impersonate parent-name"
	reviewCreateEvents          = "create events"
)

// reviewKey names the review a set of resource attributes asks about.
func reviewKey(attrs *authorizationv1.ResourceAttributes) string {
	switch {
	case attrs.Verb == "create" && attrs.Resource == "events":
		return reviewCreateEvents
	case attrs.Verb == "impersonate" && attrs.Resource == "users":
		return reviewImpersonateUser
	case attrs.Verb == "impersonate" && attrs.Resource == "userextras" && attrs.Subresource == "iam.miloapis.com/parent-type":
		return reviewImpersonateParentType
	case attrs.Verb == "impersonate" && attrs.Resource == "userextras" && attrs.Subresource == "iam.miloapis.com/parent-name":
		return reviewImpersonateParentName
	}
	return "unexpected review"
}

// newAccessTestEmitter returns an emitter whose client allows every access
// review except the one named by refuse, and the reviews it was sent.
func newAccessTestEmitter(t *testing.T, refuse string) (*ProjectEventEmitter, *[]runtime.Object) {
	t.Helper()

	e, _, cs := newTestEmitter(t, &rest.Config{Host: "https://milo.example"})
	var sent []runtime.Object

	cs.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		review := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		sent = append(sent, review)
		review.Status.Allowed = reviewKey(review.Spec.ResourceAttributes) != refuse
		return true, review, nil
	})
	cs.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		review := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sent = append(sent, review)
		review.Status.Allowed = reviewKey(review.Spec.ResourceAttributes) != refuse
		return true, review, nil
	})

	return e, &sent
}

func TestProjectEventEmitter_CheckAccess(t *testing.T) {
	tests := []struct {
		name      string
		refuse    string
		wantError string
	}{
		{name: "allowed"},
		{name: "controller cannot impersonate the user", refuse: reviewImpersonateUser, wantError: "cannot impersonate"},
		{name: "controller cannot assert the parent type", refuse: reviewImpersonateParentType, wantError: "iam.miloapis.com/parent-type"},
		{name: "controller cannot assert every parent name", refuse: reviewImpersonateParentName, wantError: "iam.miloapis.com/parent-name"},
		{name: "user cannot create events", refuse: reviewCreateEvents, wantError: "cannot create events"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newAccessTestEmitter(t, tt.refuse)

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
	e, sent := newAccessTestEmitter(t, "")

	if err := e.CheckAccess(context.Background()); err != nil {
		t.Fatalf("CheckAccess returned error: %v", err)
	}

	impersonation := map[string]*authorizationv1.ResourceAttributes{}
	var create *authorizationv1.SubjectAccessReview
	for _, obj := range *sent {
		switch review := obj.(type) {
		case *authorizationv1.SelfSubjectAccessReview:
			impersonation[reviewKey(review.Spec.ResourceAttributes)] = review.Spec.ResourceAttributes
		case *authorizationv1.SubjectAccessReview:
			create = review
		}
	}

	// Each impersonated value is authorized on its own, so the check asks
	// about each one the emitter sends.
	want := map[string]authorizationv1.ResourceAttributes{
		reviewImpersonateUser: {Verb: "impersonate", Resource: "users", Name: DefaultProjectEventUser},
		reviewImpersonateParentType: {
			Verb: "impersonate", Group: "authentication.k8s.io", Resource: "userextras",
			Subresource: "iam.miloapis.com/parent-type", Name: projectKind,
		},
		// An empty name asks whether every project is allowed, since the
		// emitter sends whichever project changed.
		reviewImpersonateParentName: {
			Verb: "impersonate", Group: "authentication.k8s.io", Resource: "userextras",
			Subresource: "iam.miloapis.com/parent-name",
		},
	}
	if len(impersonation) != len(want) {
		t.Errorf("expected %d impersonation reviews, got %d: %v", len(want), len(impersonation), impersonation)
	}
	for key, attrs := range want {
		got, ok := impersonation[key]
		if !ok {
			t.Errorf("no review for %s", key)
			continue
		}
		if *got != attrs {
			t.Errorf("%s review: got %+v, want %+v", key, *got, attrs)
		}
	}

	if create == nil {
		t.Fatal("expected a create review")
	}
	if create.Spec.User != DefaultProjectEventUser {
		t.Errorf("expected the create review for user %q, got %q", DefaultProjectEventUser, create.Spec.User)
	}
	if got := create.Spec.ResourceAttributes; got.Verb != "create" || got.Resource != "events" || got.Group != "" || got.Namespace != clusterScopedEventNamespace {
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
