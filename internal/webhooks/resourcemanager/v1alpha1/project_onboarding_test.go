package v1alpha1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	"k8s.io/component-base/metrics/testutil"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	"go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

func organizationWithOnboarding(name string, cond *metav1.Condition) *v1alpha1.Organization {
	org := &v1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-uid")}}
	if cond != nil {
		cond.Type = v1alpha1.OrganizationConditionOnboardingComplete
		org.Status.Conditions = []metav1.Condition{*cond}
	}
	return org
}

func newOnboardingValidator(t *testing.T, enforcing bool) *ProjectValidator {
	t.Helper()
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.OrganizationOnboardingGate, enforcing)

	user := &iamv1alpha1.User{ObjectMeta: metav1.ObjectMeta{Name: "test-user", UID: types.UID("user-123")}}
	fakeClient := fake.NewClientBuilder().
		WithScheme(runtimeScheme).
		WithObjects(
			user,
			organizationWithOnboarding("org-paid", &metav1.Condition{Status: metav1.ConditionTrue, Reason: v1alpha1.OrganizationOnboardingCompleteReasonReady}),
			organizationWithOnboarding("org-unpaid", &metav1.Condition{Status: metav1.ConditionFalse, Reason: v1alpha1.OrganizationOnboardingCompleteReasonPaymentMethodNotReady}),
			organizationWithOnboarding("org-nobilling", &metav1.Condition{Status: metav1.ConditionFalse, Reason: v1alpha1.OrganizationOnboardingCompleteReasonBillingAccountMissing}),
			organizationWithOnboarding("org-nocontact", &metav1.Condition{Status: metav1.ConditionFalse, Reason: v1alpha1.OrganizationOnboardingCompleteReasonContactInfoIncomplete}),
			organizationWithOnboarding("org-new", nil),
		).
		Build()

	return &ProjectValidator{Client: fakeClient, ProjectOwnerRoleName: "project-owner"}
}

func createRequest(dryRun bool) context.Context {
	return admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			DryRun:    ptr.To(dryRun),
			UserInfo:  authenticationv1.UserInfo{Username: "test@example.com", UID: "test-user"},
		},
	})
}

func projectIn(org string) *v1alpha1.Project {
	return &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "proj-1", UID: types.UID("proj-1-uid")},
		Spec:       v1alpha1.ProjectSpec{OwnerRef: v1alpha1.OwnerReference{Kind: "Organization", Name: org}},
	}
}

func TestValidateCreate_RefusesProjectsInOrganizationsNotOnboarded(t *testing.T) {
	v := newOnboardingValidator(t, true)

	cases := map[string]string{
		"org-unpaid":    "add a payment method",
		"org-nobilling": "set up a billing account",
		"org-nocontact": "add the organization's contact information",
		"org-new":       "finish setting up the organization",
	}
	for org, step := range cases {
		t.Run(org, func(t *testing.T) {
			for _, dryRun := range []bool{false, true} {
				_, err := v.ValidateCreate(createRequest(dryRun), projectIn(org))
				require.True(t, apierrors.IsForbidden(err), "dryRun=%v: error = %v, want Forbidden", dryRun, err)

				statusErr := err.(*apierrors.StatusError)
				assert.Contains(t, statusErr.ErrStatus.Message, step)
				require.NotNil(t, statusErr.ErrStatus.Details)
				assert.Contains(t, causeTypes(statusErr), v1alpha1.OrganizationOnboardingIncompleteCause)
			}
		})
	}
}

func TestValidateCreate_AdmitsProjectsInOnboardedOrganizations(t *testing.T) {
	v := newOnboardingValidator(t, true)

	_, err := v.ValidateCreate(createRequest(false), projectIn("org-paid"))
	require.NoError(t, err)

	bindings := &iamv1alpha1.PolicyBindingList{}
	require.NoError(t, v.Client.List(context.Background(), bindings))
	assert.Len(t, bindings.Items, 1, "the owner PolicyBinding is still created")
}

func TestValidateCreate_ObservesWhenGateDisabled(t *testing.T) {
	v := newOnboardingValidator(t, false)

	counter := onboardingDenials.WithLabelValues("false", v1alpha1.OrganizationOnboardingCompleteReasonPaymentMethodNotReady)
	before, err := testutil.GetCounterMetricValue(counter)
	require.NoError(t, err)

	_, err = v.ValidateCreate(createRequest(true), projectIn("org-unpaid"))
	require.NoError(t, err)

	after, err := testutil.GetCounterMetricValue(counter)
	require.NoError(t, err)
	assert.Equal(t, float64(1), after-before)
}

func TestValidateCreate_FailsWhenTheOrganizationCannotBeRead(t *testing.T) {
	v := newOnboardingValidator(t, false)

	_, err := v.ValidateCreate(createRequest(true), projectIn("org-missing"))
	assert.Error(t, err)
}

func causeTypes(err *apierrors.StatusError) []metav1.CauseType {
	var out []metav1.CauseType
	for _, c := range err.ErrStatus.Details.Causes {
		out = append(out, c.Type)
	}
	return out
}
