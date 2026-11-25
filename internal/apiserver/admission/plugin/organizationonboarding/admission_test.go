package organizationonboarding

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	"k8s.io/component-base/metrics/testutil"

	"go.miloapis.com/milo/internal/apiserver/orgstate"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
	milorequest "go.miloapis.com/milo/pkg/request"
)

var workloadGVR = schema.GroupVersionResource{Group: "compute.datumapis.com", Version: "v1alpha", Resource: "workloads"}
var workloadGVK = schema.GroupVersionKind{Group: "compute.datumapis.com", Version: "v1alpha", Kind: "Workload"}

type fakeProjects map[string]string

func (f fakeProjects) Organization(project string) (string, bool) {
	org, ok := f[project]
	return org, ok
}

type fakeOrgs map[string]orgstate.Onboarding

func (f fakeOrgs) Onboarding(name string) (orgstate.Onboarding, bool) {
	o, ok := f[name]
	return o, ok
}

var (
	onboarded      = orgstate.Onboarding{Complete: true, Reason: resourcemanagerv1alpha1.OrganizationOnboardingCompleteReasonReady}
	noPayment      = orgstate.Onboarding{Reason: resourcemanagerv1alpha1.OrganizationOnboardingCompleteReasonPaymentMethodNotReady}
	noBilling      = orgstate.Onboarding{Reason: resourcemanagerv1alpha1.OrganizationOnboardingCompleteReasonBillingAccountMissing}
	noContactInfo  = orgstate.Onboarding{Reason: resourcemanagerv1alpha1.OrganizationOnboardingCompleteReasonContactInfoIncomplete}
	standardOrgs   = fakeOrgs{"org-paid": onboarded, "org-unpaid": noPayment, "org-nobilling": noBilling, "org-nocontact": noContactInfo}
	standardProjects = fakeProjects{"proj-paid": "org-paid", "proj-unpaid": "org-unpaid", "proj-nobilling": "org-nobilling", "proj-nocontact": "org-nocontact", "proj-orphan": "org-missing"}
)

func newTestPlugin(t *testing.T, enforcing bool) *Plugin {
	t.Helper()
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.OrganizationOnboardingGate, enforcing)
	p, err := NewPlugin()
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	p.projects = standardProjects
	p.orgs = standardOrgs
	if err := p.ValidateInitialization(); err != nil {
		t.Fatalf("ValidateInitialization() error = %v", err)
	}
	return p
}

func attrsFor(op admission.Operation, gvr schema.GroupVersionResource, subresource string, oldObj runtime.Object, userInfo user.Info) admission.Attributes {
	if userInfo == nil {
		userInfo = &user.DefaultInfo{Name: "someone@example.com", Groups: []string{user.AllAuthenticated}}
	}
	return admission.NewAttributesRecord(nil, oldObj, workloadGVK, "default", "w1", gvr, subresource, op, nil, false, userInfo)
}

func inProject(project string) context.Context {
	return milorequest.WithProject(context.Background(), project)
}

func TestValidate_DeniesWritesInProjectsOfOrganizationsNotOnboarded(t *testing.T) {
	p := newTestPlugin(t, true)

	cases := map[string]string{
		"proj-unpaid":    "add a payment method",
		"proj-nobilling": "set up a billing account",
		"proj-nocontact": "add the organization's contact information",
		"proj-orphan":    "finish setting up the organization",
	}
	for project, step := range cases {
		for _, op := range []admission.Operation{admission.Create, admission.Update} {
			err := p.Validate(inProject(project), attrsFor(op, workloadGVR, "", nil, nil), nil)
			if !apierrors.IsForbidden(err) {
				t.Fatalf("%s %s: Validate() error = %v, want Forbidden", project, op, err)
			}
			statusErr := err.(*apierrors.StatusError)
			if !strings.Contains(statusErr.ErrStatus.Message, step) {
				t.Errorf("%s: message = %q, want it to contain %q", project, statusErr.ErrStatus.Message, step)
			}
			if !hasCause(statusErr, resourcemanagerv1alpha1.OrganizationOnboardingIncompleteCause) {
				t.Errorf("%s: causes = %+v, want %q", project, statusErr.ErrStatus.Details.Causes, resourcemanagerv1alpha1.OrganizationOnboardingIncompleteCause)
			}
		}
	}
}

func TestValidate_AdmitsWritesInProjectsOfOnboardedOrganizations(t *testing.T) {
	p := newTestPlugin(t, true)
	if err := p.Validate(inProject("proj-paid"), attrsFor(admission.Create, workloadGVR, "", nil, nil), nil); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestValidate_AdmitsOutsideProjects(t *testing.T) {
	p := newTestPlugin(t, true)
	if err := p.Validate(context.Background(), attrsFor(admission.Create, workloadGVR, "", nil, nil), nil); err != nil {
		t.Fatalf("Validate() outside a project error = %v, want nil", err)
	}
}

func TestValidate_AdmitsProjectsItDoesNotKnow(t *testing.T) {
	p := newTestPlugin(t, true)
	if err := p.Validate(inProject("proj-unknown"), attrsFor(admission.Create, workloadGVR, "", nil, nil), nil); err != nil {
		t.Fatalf("Validate() for an unknown project error = %v, want nil", err)
	}
}

func TestValidate_Exemptions(t *testing.T) {
	p := newTestPlugin(t, true)
	ctx := inProject("proj-unpaid")

	review := schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "selfsubjectaccessreviews"}
	if err := p.Validate(ctx, attrsFor(admission.Create, review, "", nil, nil), nil); err != nil {
		t.Errorf("access review: Validate() error = %v, want nil", err)
	}

	masters := &user.DefaultInfo{Name: "admin", Groups: []string{user.SystemPrivilegedGroup}}
	if err := p.Validate(ctx, attrsFor(admission.Create, workloadGVR, "", nil, masters), nil); err != nil {
		t.Errorf("system:masters: Validate() error = %v, want nil", err)
	}

	terminating := &unstructured.Unstructured{Object: map[string]interface{}{}}
	now := metav1.Now()
	terminating.SetDeletionTimestamp(&now)
	if err := p.Validate(ctx, attrsFor(admission.Update, workloadGVR, "", terminating, nil), nil); err != nil {
		t.Errorf("finalizer removal: Validate() error = %v, want nil", err)
	}
}

func TestValidate_ObservesWhenGateDisabled(t *testing.T) {
	p := newTestPlugin(t, false)

	counter := onboardingDenials.WithLabelValues("false", resourcemanagerv1alpha1.OrganizationOnboardingCompleteReasonPaymentMethodNotReady)
	before, err := testutil.GetCounterMetricValue(counter)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}

	if err := p.Validate(inProject("proj-unpaid"), attrsFor(admission.Create, workloadGVR, "", nil, nil), nil); err != nil {
		t.Fatalf("Validate() with gate disabled error = %v, want nil", err)
	}

	after, err := testutil.GetCounterMetricValue(counter)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if after-before != 1 {
		t.Errorf("denials counter moved by %v, want 1", after-before)
	}
}

func TestValidateInitialization_RequiresBothViews(t *testing.T) {
	p, _ := NewPlugin()
	if err := p.ValidateInitialization(); err == nil {
		t.Errorf("ValidateInitialization() with no views error = nil, want error")
	}
	p.projects = standardProjects
	if err := p.ValidateInitialization(); err == nil {
		t.Errorf("ValidateInitialization() with no organization view error = nil, want error")
	}
}

func hasCause(err *apierrors.StatusError, cause metav1.CauseType) bool {
	if err.ErrStatus.Details == nil {
		return false
	}
	for _, c := range err.ErrStatus.Details.Causes {
		if c.Type == cause {
			return true
		}
	}
	return false
}
