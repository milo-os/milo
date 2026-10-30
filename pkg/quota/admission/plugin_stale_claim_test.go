package admission

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"go.miloapis.com/milo/pkg/quota/engine"
)

var resourceClaimGVR = schema.GroupVersionResource{
	Group:    "quota.miloapis.com",
	Version:  "v1alpha1",
	Resource: "resourceclaims",
}

const (
	staleClaimUID = types.UID("stale-claim")
	freshClaimUID = types.UID("fresh-claim")
)

// TestRetryIsNotRefusedByItsStaleClaimsDeletion checks that a retried create,
// which deletes its stale pending claim inline, is judged on the claim it
// creates. The stale claim's delete event must not end the new waiter.
func TestRetryIsNotRefusedByItsStaleClaimsDeletion(t *testing.T) {
	err := retryOverStaleClaim(t, true, false)
	if err != nil {
		t.Fatalf("the retry was refused, but its own claim was granted: %v", err)
	}
}

// TestRetryIsNotAdmittedByItsStaleClaimsGrant checks that a retried create is
// not admitted on the stale claim's grant. The stale claim is granted after the
// admission read it as pending and before the inline delete; the claim the
// retry creates is denied.
func TestRetryIsNotAdmittedByItsStaleClaimsGrant(t *testing.T) {
	err := retryOverStaleClaim(t, false, true)
	if err == nil {
		t.Fatal("the retry was admitted on the stale claim's grant, but its own claim was denied")
	}
	if !strings.Contains(err.Error(), "You've reached your quota") {
		t.Fatalf("the retry was refused, but not as a denial: %v", err)
	}
}

// retryOverStaleClaim admits a create whose claim name holds a stale pending
// claim, with the real watch manager on the fake client's watch. The fresh
// claim is granted or denied when it is added, as the quota controller would.
// With staleGrantedLate, the stale claim is granted inside the inline delete,
// just before it goes.
func retryOverStaleClaim(t *testing.T, grantFresh, staleGrantedLate bool) error {
	t.Helper()
	const claimName, namespace = "fixed-claim", "default"
	scheme := runtime.NewScheme()
	if err := quotav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	stale := judgedClaim(claimName, namespace, staleClaimUID, false, quotav1alpha1.ResourceClaimPendingReason)
	stale.SetCreationTimestamp(metav1.NewTime(time.Now().Add(-10 * time.Minute)))
	client := fake.NewSimpleDynamicClient(scheme, stale)
	client.PrependReactor("delete", "resourceclaims", func(action clienttesting.Action) (bool, runtime.Object, error) {
		pre := action.(clienttesting.DeleteAction).GetDeleteOptions().Preconditions
		if pre == nil || pre.UID == nil || *pre.UID != stale.GetUID() || pre.ResourceVersion == nil || *pre.ResourceVersion != stale.GetResourceVersion() {
			t.Error("stale cleanup must bind deletion to inspected UID and resource version")
		}
		return false, nil, nil
	})
	// The API server gives a created object a UID of its own.
	client.PrependReactor("create", "resourceclaims", func(action clienttesting.Action) (bool, runtime.Object, error) {
		action.(clienttesting.CreateAction).GetObject().(*unstructured.Unstructured).SetUID(freshClaimUID)
		return false, nil, nil
	})
	if staleGrantedLate {
		client.PrependReactor("delete", "resourceclaims", func(clienttesting.Action) (bool, runtime.Object, error) {
			granted := judgedClaim(claimName, namespace, staleClaimUID, true, quotav1alpha1.ResourceClaimGrantedReason)
			return false, nil, client.Tracker().Update(resourceClaimGVR, granted, namespace)
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	judgeFreshClaim(ctx, t, client, grantFresh)

	plugin := newStaleClaimPlugin(t, client, claimName)
	defer func() {
		if wm, ok := plugin.watchManagers.Load(""); ok {
			wm.(*watchManager).Stop()
		}
	}()
	return plugin.Validate(ctx, deploymentAttrs("test-deployment", namespace), nil)
}

// judgeFreshClaim grants or denies the fresh claim when it is added, as the
// quota controller would.
func judgeFreshClaim(ctx context.Context, t *testing.T, client *fake.FakeDynamicClient, grant bool) {
	t.Helper()
	claims, err := client.Resource(resourceClaimGVR).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reason := quotav1alpha1.ResourceClaimDeniedReason
	if grant {
		reason = quotav1alpha1.ResourceClaimGrantedReason
	}
	go func() {
		defer claims.Stop()
		for event := range claims.ResultChan() {
			claim, ok := event.Object.(*unstructured.Unstructured)
			if !ok || event.Type != watch.Added || claim.GetUID() != freshClaimUID {
				continue
			}
			judged := judgedClaim(claim.GetName(), claim.GetNamespace(), freshClaimUID, grant, reason)
			if err := client.Tracker().Update(resourceClaimGVR, judged, claim.GetNamespace()); err != nil {
				t.Errorf("judge the fresh claim: %v", err)
			}
			return
		}
	}()
}

func judgedClaim(name, namespace string, uid types.UID, granted bool, reason string) *unstructured.Unstructured {
	claim := existingClaim(name, namespace, ptr.To(granted), reason, reason)
	claim.SetUID(uid)
	return claim
}

func newStaleClaimPlugin(t *testing.T, client *fake.FakeDynamicClient, claimName string) *ResourceQuotaEnforcementPlugin {
	t.Helper()
	celEngine, err := engine.NewCELEngine()
	if err != nil {
		t.Fatalf("create CEL engine: %v", err)
	}
	logger := zap.New(zap.UseDevMode(true))
	config := DefaultAdmissionPluginConfig()
	// A waiter that is never judged fails the test in seconds, not after the
	// production timeout.
	config.WatchManager.DefaultTimeout = 5 * time.Second
	return &ResourceQuotaEnforcementPlugin{
		Handler:        admission.NewHandler(admission.Create),
		dynamicClient:  client,
		policyEngine:   &testPolicyEngine{policy: newFixedNameClaimPolicy(claimName), gvk: deploymentGVK()},
		templateEngine: engine.NewTemplateEngine(celEngine, logger.WithName("template")),
		config:         config,
		logger:         logger.WithName("plugin"),
	}
}

func deploymentGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
}

func deploymentAttrs(name, namespace string) *testAdmissionAttributes {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace},
	}}
	return &testAdmissionAttributes{
		operation: admission.Create,
		object:    obj,
		gvk:       deploymentGVK(),
		name:      name,
		namespace: namespace,
		userInfo:  &user.DefaultInfo{Name: "test-user"},
	}
}

func TestConcurrentRequestDoesNotDeletePendingClaim(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := quotav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	claim := judgedClaim("fixed-claim", "default", staleClaimUID, false, quotav1alpha1.ResourceClaimPendingReason)
	claim.SetCreationTimestamp(metav1.Now())
	client := fake.NewSimpleDynamicClient(scheme, claim)
	plugin := newStaleClaimPlugin(t, client, "fixed-claim")
	defer func() {
		if wm, ok := plugin.watchManagers.Load(""); ok {
			wm.(*watchManager).Stop()
		}
	}()
	err := plugin.Validate(context.Background(), deploymentAttrs("test-deployment", "default"), nil)
	if err == nil {
		t.Fatal("pending claim must not admit another request")
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "delete" {
			t.Fatal("concurrent request deleted an in-flight claim")
		}
	}
}
