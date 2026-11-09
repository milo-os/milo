package admission

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

func quotaFakeClient(t *testing.T) *fake.FakeDynamicClient {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := quotav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewSimpleDynamicClient(scheme)
}

// A wait ends at the plugin's ClaimWaitTimeout, as a timeout failure.
func TestWaitEndsAtTheClaimWaitTimeout(t *testing.T) {
	plugin := newStaleClaimPlugin(t, quotaFakeClient(t), "fixed-claim")
	plugin.config.ClaimWaitTimeout = 50 * time.Millisecond
	plugin.awaitClaim = func(ctx context.Context, _ dynamic.ResourceInterface, ref claimRef) ClaimResult {
		<-ctx.Done()
		return claimWait{ref: ref}.unfinished(ctx, nil)
	}
	done := make(chan error, 1)
	go func() {
		done <- plugin.Validate(context.Background(), deploymentAttrs("test-deployment", "default"), nil)
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "took too long to be checked against your quota") {
			t.Fatalf("want the timeout answer, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wait did not end at the claim wait timeout")
	}
}

// The wait starts from the resource version the claim's create returned.
func TestTheWaitStartsFromTheCreatesVersion(t *testing.T) {
	client := quotaFakeClient(t)
	client.PrependReactor("create", "resourceclaims", func(a clienttesting.Action) (bool, runtime.Object, error) {
		obj := a.(clienttesting.CreateAction).GetObject().(*unstructured.Unstructured)
		obj.SetUID("created")
		obj.SetResourceVersion("42")
		return true, obj, client.Tracker().Create(resourceClaimsGVR, obj, obj.GetNamespace())
	})
	plugin := newStaleClaimPlugin(t, client, "fixed-claim")
	var got claimRef
	plugin.awaitClaim = func(_ context.Context, _ dynamic.ResourceInterface, ref claimRef) ClaimResult {
		got = ref
		return ClaimResult{Granted: true}
	}
	if err := plugin.Validate(context.Background(), deploymentAttrs("test-deployment", "default"), nil); err != nil {
		t.Fatalf("want admitted, got %v", err)
	}
	if got.UID != "created" || got.ResourceVersion != "42" {
		t.Fatalf("want the wait to follow UID created from version 42, got %+v", got)
	}
}

// A retry whose kept denied claim is already gone (another retry or cleanup
// deleted it) goes on to create a fresh claim.
func TestRetryWhoseKeptClaimIsAlreadyGoneProceeds(t *testing.T) {
	client := quotaFakeClient(t)
	plugin := newStaleClaimPlugin(t, client, "fixed-claim")
	kept := judgedClaim("fixed-claim", "default", "kept", false, quotav1alpha1.ResourceClaimDeniedReason)
	if err := plugin.handleExistingClaim(context.Background(), client, resourceClaimsGVR, existingClaimDenied, kept); err != nil {
		t.Fatalf("want the retry to go on, got %v", err)
	}
}
