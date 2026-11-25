// Package organizationonboarding implements the
// OrganizationOnboardingEnforcement admission plugin. It denies Create and
// Update inside a project whose owning Organization has not finished
// onboarding: contact information, a billing account, and a payment method or
// payment terms, as the organization controller reports them in the
// OnboardingComplete condition.
//
// Clients such as cloud-portal and datumctl already hold a user on setup until
// that condition is True. This plugin is the same rule on the server, so a
// client that calls the API directly is held to it too.
//
// Organization-scoped writes are not gated: that is where the billing account,
// payment method and contact information are set, so gating them would leave
// no way to finish onboarding. Deletes are not registered, so an organization
// can always clean up.
//
// OrganizationOnboardingGate selects deny-vs-observe, not on-vs-off, as
// EmailVerifiedGate does for EmailVerificationEnforcement.
package organizationonboarding

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"

	"go.miloapis.com/milo/internal/apiserver/orgstate"
	"go.miloapis.com/milo/internal/apiserver/projectstate"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
	milorequest "go.miloapis.com/milo/pkg/request"
)

// reasonUnknownOrganization labels a write in a project whose Organization
// this server has not seen.
const reasonUnknownOrganization = "UnknownOrganization"

// onboardingDenials counts project writes the plugin ruled on, by whether the
// gate was enforcing and by the Organization's onboarding reason, so the
// population can be sized before OrganizationOnboardingGate is turned on.
var onboardingDenials = metrics.NewCounterVec(
	&metrics.CounterOpts{
		Subsystem:      "milo_organization_onboarding",
		Name:           "denials_total",
		Help:           "Project writes ruled against because the owning organization has not finished onboarding, labelled by whether the gate was enforcing and the onboarding reason.",
		StabilityLevel: metrics.ALPHA,
	},
	[]string{"enforced", "reason"},
)

func init() {
	legacyregistry.MustRegister(onboardingDenials)
}

// accessReviewResources must never be blocked, for the reason given in
// plugin/projectsuspension: clients create access reviews to decide what to
// render, and a denied review reads as lost access.
var accessReviewResources = map[schema.GroupResource]bool{
	{Group: "authorization.k8s.io", Resource: "subjectaccessreviews"}:      true,
	{Group: "authorization.k8s.io", Resource: "localsubjectaccessreviews"}: true,
	{Group: "authorization.k8s.io", Resource: "selfsubjectaccessreviews"}:  true,
	{Group: "authorization.k8s.io", Resource: "selfsubjectrulesreviews"}:   true,
}

type projectOwners interface {
	Organization(project string) (string, bool)
}

type organizations interface {
	Onboarding(name string) (orgstate.Onboarding, bool)
}

// Plugin denies Create and Update in projects whose Organization has not
// finished onboarding.
type Plugin struct {
	*admission.Handler

	projects projectOwners
	orgs     organizations
}

var (
	_ admission.ValidationInterface     = &Plugin{}
	_ admission.InitializationValidator = &Plugin{}
)

// NewPlugin creates a new OrganizationOnboardingEnforcement admission plugin.
func NewPlugin() (*Plugin, error) {
	return &Plugin{
		Handler: admission.NewHandler(admission.Create, admission.Update),
	}, nil
}

// SetProjectStates receives the API server's shared view of Projects.
func (p *Plugin) SetProjectStates(c *projectstate.Cache) {
	p.projects = c
}

// SetOrganizationStates receives the API server's shared view of
// Organizations.
func (p *Plugin) SetOrganizationStates(c *orgstate.Cache) {
	p.orgs = c
}

// ValidateInitialization implements admission.InitializationValidator.
func (p *Plugin) ValidateInitialization() error {
	if p.projects == nil {
		return fmt.Errorf("%s: missing project states", PluginName)
	}
	if p.orgs == nil {
		return fmt.Errorf("%s: missing organization states", PluginName)
	}
	return nil
}

// Validate implements admission.ValidationInterface.
func (p *Plugin) Validate(ctx context.Context, attrs admission.Attributes, _ admission.ObjectInterfaces) error {
	if isExempt(attrs) {
		return nil
	}

	projectID, ok := milorequest.ProjectID(ctx)
	if !ok || projectID == "" {
		return nil
	}

	org, ok := p.projects.Organization(projectID)
	if !ok {
		return nil
	}

	onboarding, known := p.orgs.Onboarding(org)
	if known && onboarding.Complete {
		return nil
	}

	reason := onboarding.Reason
	if !known {
		reason = reasonUnknownOrganization
	}

	enforcing := utilfeature.DefaultFeatureGate.Enabled(features.OrganizationOnboardingGate)
	onboardingDenials.WithLabelValues(strconv.FormatBool(enforcing), reason).Inc()
	if !enforcing {
		return nil
	}
	return newOnboardingForbiddenError(attrs, projectID, org, reason)
}

// isExempt reports whether the request bypasses this plugin entirely: access
// reviews, system:masters, and updates to objects already mid-deletion so
// finalizer removal can complete. The same three as plugin/projectsuspension.
func isExempt(attrs admission.Attributes) bool {
	if accessReviewResources[attrs.GetResource().GroupResource()] {
		return true
	}

	for _, g := range attrs.GetUserInfo().GetGroups() {
		if g == user.SystemPrivilegedGroup {
			return true
		}
	}

	if attrs.GetOperation() == admission.Update && isAlreadyTerminating(attrs.GetOldObject()) {
		return true
	}

	return false
}

func isAlreadyTerminating(obj runtime.Object) bool {
	if obj == nil {
		return false
	}
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return false
	}
	return accessor.GetDeletionTimestamp() != nil
}

// newOnboardingForbiddenError builds a 403 carrying
// OrganizationOnboardingIncompleteCause in Details.Causes, so a client can
// send the user to finish setup.
func newOnboardingForbiddenError(attrs admission.Attributes, projectID, org, reason string) error {
	msg := fmt.Sprintf(
		"organization %q has not finished onboarding (%s); %s before creating or modifying resources in project %q",
		org, reason, nextStep(reason), projectID,
	)

	err := admission.NewForbidden(attrs, errors.New(msg))
	if apiErr, ok := err.(*apierrors.StatusError); ok && apiErr.ErrStatus.Details != nil {
		apiErr.ErrStatus.Details.Causes = append(apiErr.ErrStatus.Details.Causes, metav1.StatusCause{
			Type:    resourcemanagerv1alpha1.OrganizationOnboardingIncompleteCause,
			Message: msg,
		})
	}
	return err
}

func nextStep(reason string) string {
	switch reason {
	case resourcemanagerv1alpha1.OrganizationOnboardingCompleteReasonContactInfoIncomplete:
		return "add the organization's contact information"
	case resourcemanagerv1alpha1.OrganizationOnboardingCompleteReasonBillingAccountMissing:
		return "set up a billing account"
	case resourcemanagerv1alpha1.OrganizationOnboardingCompleteReasonPaymentMethodNotReady:
		return "add a payment method"
	default:
		return "finish setting up the organization"
	}
}
