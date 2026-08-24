// Package emailverification implements the EmailVerificationEnforcement
// admission plugin. It denies Create and Update from end users whose email
// address is not verified, reading the verdict off the authenticated identity
// rather than looking it up.
//
// The signal arrives in userInfo.extra, stamped by zitadel-provider's
// TokenReview webhook next to iam.miloapis.com/registrationApproval. That is
// why this package is short. An earlier version read User.status.emailVerified,
// which cost a per-write API call, an informer cache of ServiceAccount UIDs to
// tell machines from humans, and a readiness check to hold the apiserver out of
// rotation until that cache synced. Moving the fact onto the identity deleted
// all three, along with the startup contention the cache caused.
//
// This DUPLICATES unverified-email-policy.yaml in the datum repo, deliberately
// and temporarily. A ValidatingAdmissionPolicy is a resource deployed to one
// control plane; this is compiled into every milo apiserver, including project
// control planes, which the CRD bootstrap does not seed policies into. The two
// reach identical verdicts, so running both is harmless. Once it is settled
// whether project control planes are in scope for this gate, one of them should
// go — and if they are out of scope, prefer the policy and delete this package.
//
// ABSENCE ADMITS, which is a contract with the producer rather than an
// oversight. Machine identities carry no email claim, so zitadel-provider
// stamps no key for them and they pass here. The converse is guaranteed on the
// producer side: a human ALWAYS carries the key, "true" or "false", never
// omitted. If that guarantee breaks, this plugin fails open for humans — the
// two sides have to change together.
//
// It also means the plugin is inert until the producer ships: with no key on
// any identity there is nothing to rule on.
//
// EmailVerifiedGate selects deny-vs-observe, not on-vs-off. The plugin always
// evaluates and always counts; disabled, it admits anyway. That is affordable
// only because the verdict is a map lookup on the request itself — the earlier
// CR-reading version needed a gate to avoid paying for an informer cache while
// idle, which is the opposite reason for the same switch.
//
// Writes only, and structurally so. Reads never route through admission, so
// this cannot gate them however it is written — restricting what an unverified
// user SEES is the portal's job. Delete is likewise unregistered, so an
// unverified user can still clean up after themselves.
package emailverification

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

	"go.miloapis.com/milo/pkg/features"
)

// EmailVerifiedExtraKey is the userInfo.extra key carrying the verification
// verdict. Written by zitadel-provider's authentication webhook
// (internal/webhook/response.go); the same string is hardcoded there, matching
// how iam.miloapis.com/registrationApproval is already handled on both sides.
const EmailVerifiedExtraKey = "iam.miloapis.com/emailVerified"

// verifiedValue is the only value that admits. Anything else — "false", an
// empty string, an unexpected value from a future producer — denies, so a
// producer bug cannot accidentally grant access.
const verifiedValue = "true"

// EmailNotVerifiedCause marks a denial as caused by an unverified email
// address, distinct from quota, RBAC or project-suspension denials.
//
// This is the one thing the equivalent ValidatingAdmissionPolicy cannot
// express: a policy's reason is limited to the built-in enum and it has no way
// to populate Details.Causes. cloud-portal already classifies suspension 403s
// on details[].code (see classify-suspension-error.ts), so a stable code here
// is what lets it prompt for verification instead of rendering a generic
// permission error.
const EmailNotVerifiedCause metav1.CauseType = "EmailNotVerified"

// accessReviewResources lists resources that must never be blocked by this
// plugin. Clients — notably cloud-portal — create a SelfSubjectAccessReview to
// decide whether to render an affordance at all; denying the review rather than
// the write would make an unverified account look like it had lost access
// entirely, instead of being asked to verify. Mirrors the same list in
// plugin/projectsuspension.
// emailVerificationDenials counts unverified writes the plugin ruled on. The
// enforced label separates what was actually denied from what merely would
// have been, so the population can be sized before EmailVerifiedGate is turned
// on. Mirrors the enforcement_action label a ValidatingAdmissionPolicy reports,
// so the two stay comparable if both ever run.
var emailVerificationDenials = metrics.NewCounterVec(
	&metrics.CounterOpts{
		Subsystem:      "milo_email_verification",
		Name:           "denials_total",
		Help:           "Writes ruled unverified, labelled by whether the gate was enforcing.",
		StabilityLevel: metrics.ALPHA,
	},
	[]string{"enforced"},
)

func init() {
	legacyregistry.MustRegister(emailVerificationDenials)
}

var accessReviewResources = map[schema.GroupResource]bool{
	{Group: "authorization.k8s.io", Resource: "subjectaccessreviews"}:      true,
	{Group: "authorization.k8s.io", Resource: "localsubjectaccessreviews"}: true,
	{Group: "authorization.k8s.io", Resource: "selfsubjectaccessreviews"}:  true,
	{Group: "authorization.k8s.io", Resource: "selfsubjectrulesreviews"}:   true,
}

// Plugin denies Create and Update from identities whose email address is not
// verified. It holds no state: the verdict is already in the request.
type Plugin struct {
	*admission.Handler
}

var _ admission.ValidationInterface = &Plugin{}

// NewPlugin creates a new EmailVerificationEnforcement admission plugin.
func NewPlugin() (*Plugin, error) {
	return &Plugin{
		Handler: admission.NewHandler(admission.Create, admission.Update),
	}, nil
}

// Validate implements admission.ValidationInterface.
func (p *Plugin) Validate(_ context.Context, attrs admission.Attributes, _ admission.ObjectInterfaces) error {
	if isExempt(attrs) {
		return nil
	}

	values, present := attrs.GetUserInfo().GetExtra()[EmailVerifiedExtraKey]
	if !present || len(values) == 0 {
		// Not a human, as far as the authenticator could tell. See the package
		// comment: this is the machine-identity path, not an "unknown" path.
		return nil
	}
	if values[0] == verifiedValue {
		return nil
	}

	// Always evaluated, denied only when gated. Disabled is an OBSERVE mode
	// rather than an off switch: the counter below is what tells you how many
	// accounts enabling the gate would start blocking.
	enforcing := utilfeature.DefaultFeatureGate.Enabled(features.EmailVerifiedGate)
	emailVerificationDenials.WithLabelValues(strconv.FormatBool(enforcing)).Inc()
	if !enforcing {
		return nil
	}
	return newUnverifiedForbiddenError(attrs)
}

// isExempt reports whether the request bypasses this plugin entirely.
//
// Three exemptions, all copied from plugin/projectsuspension for consistency:
//
//  1. Access reviews, so an unverified account is asked to verify rather than
//     appearing to have lost access. See accessReviewResources.
//  2. system:masters, following the Kubernetes convention that the privileged
//     group bypasses admission-time policy checks.
//  3. Updates to objects already mid-deletion, so finalizer removal can
//     complete. This one is not optional: removing a finalizer is a plain
//     Update and Update is registered, so without it any finalizer-bearing
//     resource owned by an unverified user would be stuck Terminating forever.
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

// isAlreadyTerminating reports whether obj already carries a deletionTimestamp,
// i.e. it is mid-deletion. There is no generic "/finalize" subresource outside
// a handful of built-in types, so finalizer removal on an arbitrary CRD is
// indistinguishable from any other Update without this check.
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

// newUnverifiedForbiddenError builds a 403 carrying EmailNotVerifiedCause in
// Details.Causes, so a client can tell "verify your email" apart from "you do
// not have permission".
func newUnverifiedForbiddenError(attrs admission.Attributes) error {
	msg := fmt.Sprintf(
		"email address for %q is not verified; verify it before creating or modifying resources",
		attrs.GetUserInfo().GetName(),
	)

	err := admission.NewForbidden(attrs, errors.New(msg))
	if apiErr, ok := err.(*apierrors.StatusError); ok && apiErr.ErrStatus.Details != nil {
		apiErr.ErrStatus.Details.Causes = append(apiErr.ErrStatus.Details.Causes, metav1.StatusCause{
			Type:    EmailNotVerifiedCause,
			Message: msg,
		})
	}
	return err
}
