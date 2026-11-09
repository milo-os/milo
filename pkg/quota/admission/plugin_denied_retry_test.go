package admission

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// withDeletePreconditions makes the fake client's deletes follow the API
// server's rule: a delete whose UID precondition does not match the stored
// object answers 409 Conflict, and leaves the object.
func withDeletePreconditions(client *fake.FakeDynamicClient) {
	client.PrependReactor("delete", "resourceclaims", func(a clienttesting.Action) (bool, runtime.Object, error) {
		d := a.(clienttesting.DeleteAction)
		stored, err := client.Tracker().Get(resourceClaimsGVR, d.GetNamespace(), d.GetName())
		if err != nil {
			return true, nil, err
		}
		pre := d.GetDeleteOptions().Preconditions
		if pre != nil && pre.UID != nil && *pre.UID != stored.(*unstructured.Unstructured).GetUID() {
			return true, nil, apierrors.NewConflict(resourceClaimsGVR.GroupResource(), d.GetName(), nil)
		}
		return true, nil, client.Tracker().Delete(resourceClaimsGVR, d.GetNamespace(), d.GetName())
	})
}

// Two retries of one resource read the same kept denied claim. Retry A
// replaces it and is admitted; retry B, acting on its earlier read, then meets
// the precondition conflict: it answers a conflict and leaves A's fresh claim.
func TestTwoRetriesAtOnceEndWithOneFreshClaim(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := quotav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kept := judgedClaim("fixed-claim", "default", "kept", false, quotav1alpha1.ResourceClaimDeniedReason)
	kept.SetResourceVersion("5")
	client := fake.NewSimpleDynamicClient(scheme, kept.DeepCopy())
	withDeletePreconditions(client)
	plugin := newStaleClaimPlugin(t, client, "fixed-claim")
	plugin.awaitClaim = decidedBy("grant")

	if err := plugin.Validate(context.Background(), deploymentAttrs("test-deployment", "default"), nil); err != nil {
		t.Fatalf("retry A: want admitted, got %v", err)
	}
	fresh, err := client.Tracker().Get(resourceClaimsGVR, "default", "fixed-claim")
	if err != nil {
		t.Fatalf("retry A left no claim: %v", err)
	}

	err = plugin.handleExistingClaim(context.Background(), client, resourceClaimsGVR, existingClaimDenied, kept)
	if _, ok := asClaimFailure(err); !ok {
		t.Fatalf("retry B: want a conflict, got %v", err)
	}
	after, err := client.Tracker().Get(resourceClaimsGVR, "default", "fixed-claim")
	if err != nil {
		t.Fatalf("retry B deleted retry A's fresh claim: %v", err)
	}
	if uid := after.(*unstructured.Unstructured).GetUID(); uid != fresh.(*unstructured.Unstructured).GetUID() || uid == types.UID("kept") {
		t.Fatalf("want retry A's fresh claim, got UID %q", uid)
	}
}

// claimChangedText is the part of the user's message that says a concurrent
// change, not cleanup, stopped the request.
const claimChangedText = "changed while we were processing your request"

// failClaimVerb makes the fake client answer every call of verb on claims with err.
func failClaimVerb(client *fake.FakeDynamicClient, verb string, err error) {
	client.PrependReactor(verb, "resourceclaims", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, err
	})
}

// assertUserMessage fails unless err is a claim failure whose user-facing
// message contains want and never says a previous attempt is being cleaned up.
func assertUserMessage(t *testing.T, err error, want string) {
	t.Helper()
	f, ok := asClaimFailure(err)
	if !ok {
		t.Fatalf("want a claim failure, got %v", err)
	}
	msg := userFacingClaimError(f).Error()
	if !strings.Contains(msg, want) || strings.Contains(msg, "still cleaning up") {
		t.Fatalf("message %q: want %q, and no cleanup", msg, want)
	}
}

// A delete that loses its precondition means the claim changed after the
// plugin read it, by a concurrent retry or by the claim's controller. No
// previous attempt is being cleaned up, so the message must not say so.
func TestALostDeletePreconditionSaysTheClaimChanged(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := quotav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kept := judgedClaim("fixed-claim", "default", "kept", false, quotav1alpha1.ResourceClaimDeniedReason)
	replaced := judgedClaim("fixed-claim", "default", "replaced", true, quotav1alpha1.ResourceClaimGrantedReason)
	client := fake.NewSimpleDynamicClient(scheme, replaced)
	withDeletePreconditions(client)
	plugin := newStaleClaimPlugin(t, client, "fixed-claim")

	err := plugin.handleExistingClaim(context.Background(), client, resourceClaimsGVR, existingClaimDenied, kept)
	assertUserMessage(t, err, claimChangedText)
}

// A delete that fails for any other reason is not a conflict: the user sees
// the internal error's message, and the cause stays in the failure for the log.
func TestAFailedDeleteIsAnInternalError(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := quotav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kept := judgedClaim("fixed-claim", "default", "kept", false, quotav1alpha1.ResourceClaimDeniedReason)
	client := fake.NewSimpleDynamicClient(scheme, kept.DeepCopy())
	cause := apierrors.NewInternalError(errors.New("etcd unavailable"))
	failClaimVerb(client, "delete", cause)
	plugin := newStaleClaimPlugin(t, client, "fixed-claim")

	err := plugin.handleExistingClaim(context.Background(), client, resourceClaimsGVR, existingClaimDenied, kept)
	assertUserMessage(t, err, "Something went wrong while checking your quota")
	if !errors.Is(err, cause) {
		t.Fatalf("the failure lost its cause: %v", err)
	}
}

// A create that finds the claim's name taken again lost a race to a
// concurrent request for the same resource: the claim changed, so the user
// retries.
func TestACreateThatFindsTheNameTakenSaysTheClaimChanged(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := quotav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	client := fake.NewSimpleDynamicClient(scheme)
	failClaimVerb(client, "create", apierrors.NewAlreadyExists(resourceClaimsGVR.GroupResource(), "fixed-claim"))
	plugin := newStaleClaimPlugin(t, client, "fixed-claim")
	plugin.awaitClaim = decidedBy("grant")

	err := plugin.Validate(context.Background(), deploymentAttrs("test-deployment", "default"), nil)
	if err == nil || !strings.Contains(err.Error(), claimChangedText) || strings.Contains(err.Error(), "still cleaning up") {
		t.Fatalf("want the claim-changed message, got %v", err)
	}
}
