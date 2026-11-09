package admission

import (
	"context"
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
