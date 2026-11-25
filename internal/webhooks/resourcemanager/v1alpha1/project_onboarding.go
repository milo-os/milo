package v1alpha1

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

const onboardingReasonUnknown = "Unknown"

// onboardingDenials counts project creates refused, or that would have been
// refused, because the owning organization has not finished onboarding.
var onboardingDenials = metrics.NewCounterVec(
	&metrics.CounterOpts{
		Subsystem:      "milo_organization_onboarding",
		Name:           "denials_total",
		Help:           "Project creates ruled against because the owning organization has not finished onboarding, labelled by whether the gate was enforcing and the onboarding reason.",
		StabilityLevel: metrics.ALPHA,
	},
	[]string{"enforced", "reason"},
)

func init() {
	legacyregistry.MustRegister(onboardingDenials)
}

// checkOrganizationOnboarded refuses a new Project while its organization's
// OnboardingComplete condition is not True. OrganizationOnboardingGate selects
// deny over observe: while it is off, the refusal is only counted.
func (v *ProjectValidator) checkOrganizationOnboarded(ctx context.Context, project *v1alpha1.Project) error {
	org := &v1alpha1.Organization{}
	if err := v.Client.Get(ctx, client.ObjectKey{Name: project.Spec.OwnerRef.Name}, org); err != nil {
		return fmt.Errorf("failed to get organization %q: %w", project.Spec.OwnerRef.Name, err)
	}

	reason := onboardingReasonUnknown
	if cond := apimeta.FindStatusCondition(org.Status.Conditions, v1alpha1.OrganizationConditionOnboardingComplete); cond != nil {
		if cond.Status == metav1.ConditionTrue {
			return nil
		}
		reason = cond.Reason
	}

	enforcing := utilfeature.DefaultFeatureGate.Enabled(features.OrganizationOnboardingGate)
	onboardingDenials.WithLabelValues(strconv.FormatBool(enforcing), reason).Inc()
	if !enforcing {
		return nil
	}
	return newOnboardingForbiddenError(project, org.Name, reason)
}

// newOnboardingForbiddenError builds a 403 carrying
// OrganizationOnboardingIncompleteCause, so a client can send the user to
// finish setup instead of showing a permission error.
func newOnboardingForbiddenError(project *v1alpha1.Project, org, reason string) error {
	msg := fmt.Sprintf("organization %q has not finished onboarding (%s); %s before creating a project", org, reason, nextOnboardingStep(reason))

	err := apierrors.NewForbidden(v1alpha1.GroupVersion.WithResource("projects").GroupResource(), project.Name, errors.New(msg))
	err.ErrStatus.Details.Causes = append(err.ErrStatus.Details.Causes, metav1.StatusCause{
		Type:    v1alpha1.OrganizationOnboardingIncompleteCause,
		Message: msg,
	})
	return err
}

func nextOnboardingStep(reason string) string {
	switch reason {
	case v1alpha1.OrganizationOnboardingCompleteReasonContactInfoIncomplete:
		return "add the organization's contact information"
	case v1alpha1.OrganizationOnboardingCompleteReasonBillingAccountMissing:
		return "set up a billing account"
	case v1alpha1.OrganizationOnboardingCompleteReasonPaymentMethodNotReady:
		return "add a payment method"
	default:
		return "finish setting up the organization"
	}
}
