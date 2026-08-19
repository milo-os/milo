package emailverification

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"

	"go.miloapis.com/milo/pkg/features"
)

// projectGVR is an arbitrary non-exempt resource, standing in for "any write a
// user might attempt".
var projectGVR = schema.GroupVersionResource{
	Group:    "resourcemanager.miloapis.com",
	Version:  "v1alpha1",
	Resource: "projects",
}

var projectGVK = schema.GroupVersionKind{
	Group:   "resourcemanager.miloapis.com",
	Version: "v1alpha1",
	Kind:    "Project",
}

// humanWith builds a user carrying the verification extra key set to value.
func humanWith(value string) user.Info {
	return &user.DefaultInfo{
		Name:   "someone@example.com",
		UID:    "377818122914103359",
		Groups: []string{user.AllAuthenticated},
		Extra:  map[string][]string{EmailVerifiedExtraKey: {value}},
	}
}

// machine builds an identity with NO verification key, which is how
// zitadel-provider represents a token with no email claim.
func machine() user.Info {
	return &user.DefaultInfo{
		Name:   "sa-billing-sync",
		UID:    "8f14e45f-ceea-467a-9f6c-0d1f2e3a4b5c",
		Groups: []string{user.AllAuthenticated},
		Extra:  map[string][]string{"iam.miloapis.com/registrationApproval": {"Approved"}},
	}
}

// terminating returns an object already carrying a deletionTimestamp.
func terminating() runtime.Object {
	obj := &unstructured.Unstructured{}
	obj.SetName("doomed")
	obj.SetDeletionTimestamp(&metav1.Time{Time: time.Unix(1, 0)})
	return obj
}

func attrs(u user.Info, op admission.Operation, gvr schema.GroupVersionResource, oldObject runtime.Object) admission.Attributes {
	return admission.NewAttributesRecord(
		&unstructured.Unstructured{}, oldObject,
		projectGVK, "default", "some-name",
		gvr, "", op, nil, false, u,
	)
}

func TestValidate(t *testing.T) {
	// The gate selects deny-vs-observe. These cases describe the DENY side;
	// TestObserveModeCountsWithoutDenying covers the other.
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EmailVerifiedGate, true)

	accessReviewGVR := schema.GroupVersionResource{
		Group: "authorization.k8s.io", Version: "v1", Resource: "selfsubjectaccessreviews",
	}

	tests := []struct {
		name       string
		user       user.Info
		operation  admission.Operation
		gvr        schema.GroupVersionResource
		oldObject  runtime.Object
		wantDenied bool
		why        string
	}{
		{
			name: "verified human is admitted", user: humanWith("true"),
			operation: admission.Create, gvr: projectGVR,
			why: "the whole point of the gate is to let verified users through",
		},
		{
			name: "unverified human is denied", user: humanWith("false"),
			operation: admission.Create, gvr: projectGVR, wantDenied: true,
			why: "this is the population the gate exists to block; if this passes the gate is decorative",
		},
		{
			name: "unverified human is denied on update too", user: humanWith("false"),
			operation: admission.Update, gvr: projectGVR, wantDenied: true,
			why: "gating create but not update would let an unverified user mutate anything that already exists",
		},
		{
			name: "identity with no key is admitted", user: machine(),
			operation: admission.Create, gvr: projectGVR,
			why: "absence means machine identity, per the contract with zitadel-provider",
		},
		{
			name:      "identity with an empty value slice is admitted",
			user:      &user.DefaultInfo{Name: "odd", Extra: map[string][]string{EmailVerifiedExtraKey: {}}},
			operation: admission.Create, gvr: projectGVR,
			why: "a present-but-empty slice is indexed at [0] elsewhere; reading it must not panic",
		},
		{
			name: "unexpected value is denied", user: humanWith("yes"),
			operation: admission.Create, gvr: projectGVR, wantDenied: true,
			why: "only \"true\" admits, so a producer emitting some other truthy spelling cannot grant access by accident",
		},
		{
			name: "access review is exempt even when unverified", user: humanWith("false"),
			operation: admission.Create, gvr: accessReviewGVR,
			why: "denying the review rather than the write makes an unverified account look like it lost all access",
		},
		{
			name: "system:masters is exempt",
			user: &user.DefaultInfo{
				Name:   "admin",
				Groups: []string{user.SystemPrivilegedGroup},
				Extra:  map[string][]string{EmailVerifiedExtraKey: {"false"}},
			},
			operation: admission.Create, gvr: projectGVR,
			why: "Kubernetes convention: the privileged group bypasses admission-time policy",
		},
		{
			name: "update to a terminating object is exempt", user: humanWith("false"),
			operation: admission.Update, gvr: projectGVR, oldObject: terminating(),
			why: "finalizer removal is a plain Update; without this, resources stick in Terminating forever",
		},
		{
			name: "create is denied even though oldObject is nil", user: humanWith("false"),
			operation: admission.Create, gvr: projectGVR, oldObject: nil, wantDenied: true,
			why: "the terminating exemption must not accidentally fire when there is no old object to inspect",
		},
	}

	plugin, err := NewPlugin()
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := plugin.Validate(context.Background(), attrs(tc.user, tc.operation, tc.gvr, tc.oldObject), nil)
			if tc.wantDenied && err == nil {
				t.Fatalf("expected denial, got admit — %s", tc.why)
			}
			if !tc.wantDenied && err != nil {
				t.Fatalf("expected admit, got %v — %s", err, tc.why)
			}
		})
	}
}

// TestDenialCarriesCause pins the one thing the equivalent
// ValidatingAdmissionPolicy cannot express. cloud-portal classifies 403s on
// details[].code, so losing this turns "verify your email" into a generic
// permission error in the UI.
func TestDenialCarriesCause(t *testing.T) {
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EmailVerifiedGate, true)

	plugin, err := NewPlugin()
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}

	err = plugin.Validate(context.Background(), attrs(humanWith("false"), admission.Create, projectGVR, nil), nil)
	if err == nil {
		t.Fatal("expected denial")
	}

	statusErr, ok := err.(*apierrors.StatusError)
	if !ok {
		t.Fatalf("denial must be a StatusError so clients can read details, got %T", err)
	}
	if statusErr.ErrStatus.Details == nil {
		t.Fatal("denial carries no Details, so no cause can be read from it")
	}

	for _, c := range statusErr.ErrStatus.Details.Causes {
		if c.Type == EmailNotVerifiedCause {
			return
		}
	}
	t.Fatalf("denial must carry the %s cause so clients can prompt for verification; causes were %+v",
		EmailNotVerifiedCause, statusErr.ErrStatus.Details.Causes)
}

// TestHandlesRegisteredOperations guards the registration itself. Delete must
// stay unregistered so an unverified user can clean up after themselves, and
// Connect has no meaning here.
func TestHandlesRegisteredOperations(t *testing.T) {
	plugin, err := NewPlugin()
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}

	for op, want := range map[admission.Operation]bool{
		admission.Create:  true,
		admission.Update:  true,
		admission.Delete:  false,
		admission.Connect: false,
	} {
		if got := plugin.Handles(op); got != want {
			t.Errorf("Handles(%v) = %v, want %v", op, got, want)
		}
	}
}

// TestObserveModeCountsWithoutDenying pins the default. The gate ships off, and
// off must not mean "does nothing" — the plugin still has to rule on the
// request so its counter can size the population that enabling would block.
// If this ever starts denying, the rollout loses the number it depends on.
func TestObserveModeCountsWithoutDenying(t *testing.T) {
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EmailVerifiedGate, false)

	plugin, err := NewPlugin()
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}

	err = plugin.Validate(context.Background(), attrs(humanWith("false"), admission.Create, projectGVR, nil), nil)
	if err != nil {
		t.Fatalf("gate disabled must admit, got %v", err)
	}
}

// TestGateDefaultsOff guards the rollout order: enabling enforcement has to be
// a deliberate act, never something a deploy turns on.
func TestGateDefaultsOff(t *testing.T) {
	if utilfeature.DefaultFeatureGate.Enabled(features.EmailVerifiedGate) {
		t.Fatal("EmailVerifiedGate must default off; enabling it denies writes to every unverified account")
	}
}
