package v1alpha1

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

var claimCRDFile = filepath.Join("..", "..", "..", "..", "config", "crd", "bases", "quota", "quota.miloapis.com_resourceclaims.yaml")

// A claim holds one allocation per resource type, so the API server refuses a claim that requests one type twice.
func TestResourceClaimRefusesTwoRequestsOfOneType(t *testing.T) {
	ctx, c := startClaimEnv(t, readClaimCRD(t))
	err := c.Create(ctx, twoOfOneType("twice"))
	require.True(t, apierrors.IsInvalid(err), "a claim with two requests of one type: %v", err)
	require.NoError(t, c.Create(ctx, &ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "default"},
		Spec: ResourceClaimSpec{Requests: []ResourceRequest{{ResourceType: "example/a", Amount: 1}, {ResourceType: "example/b", Amount: 1}}}}))
}

// A claim stored before the rule keeps working after it: the API server ratchets a list-map rule that the stored
// object already broke, so its updates, its status, its finalizer and its deletion are all still accepted.
func TestResourceClaimStoredWithTwoRequestsOfOneTypeStaysUpdatable(t *testing.T) {
	current := readClaimCRD(t)
	ctx, c := startClaimEnv(t, withoutRequestsListType(t, current))
	legacy := twoOfOneType("legacy")
	require.NoError(t, c.Create(ctx, legacy), "the old schema stores a claim with two requests of one type")

	var served apiextensionsv1.CustomResourceDefinition
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: current.Name}, &served))
	served.Spec = current.Spec
	require.NoError(t, c.Update(ctx, &served))
	require.Eventually(t, func() bool {
		return apierrors.IsInvalid(c.Create(ctx, twoOfOneType("probe")))
	}, 30*time.Second, 200*time.Millisecond, "the upgraded schema never refused a new claim")

	legacy.Finalizers = []string{"example.com/hold"}
	require.NoError(t, c.Update(ctx, legacy), "an update of the stored claim")
	legacy.Status.Allocations = []ResourceClaimAllocationStatus{{ResourceType: "example/count", Status: ResourceClaimAllocationStatusDenied,
		Reason: ResourceClaimDeniedReason, Message: "test", LastTransitionTime: metav1.Now()}}
	require.NoError(t, c.Status().Update(ctx, legacy), "a status update of the stored claim")
	legacy.Finalizers = nil
	require.NoError(t, c.Update(ctx, legacy), "the stored claim's finalizer removed")
	require.NoError(t, c.Delete(ctx, legacy))
	require.Eventually(t, func() bool {
		return apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(legacy), &ResourceClaim{}))
	}, 30*time.Second, 200*time.Millisecond, "the stored claim was not deleted")
}

func startClaimEnv(t *testing.T, crd *apiextensionsv1.CustomResourceDefinition) (context.Context, client.Client) {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("KUBEBUILDER_ASSETS must be set in CI; this test needs an envtest API server")
		}
		t.Skip("KUBEBUILDER_ASSETS is not set; run setup-envtest to provide an envtest API server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	env := &envtest.Environment{CRDs: []*apiextensionsv1.CustomResourceDefinition{crd}}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })
	scheme := runtime.NewScheme()
	require.NoError(t, AddToScheme(scheme))
	require.NoError(t, apiextensionsv1.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	return ctx, c
}

func readClaimCRD(t *testing.T) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	data, err := os.ReadFile(claimCRDFile)
	require.NoError(t, err)
	var crd apiextensionsv1.CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(data, &crd))
	return &crd
}

// withoutRequestsListType returns the CRD as it was before spec.requests became a map list keyed by resourceType.
func withoutRequestsListType(t *testing.T, crd *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	old := crd.DeepCopy()
	for i := range old.Spec.Versions {
		spec := old.Spec.Versions[i].Schema.OpenAPIV3Schema.Properties["spec"]
		requests := spec.Properties["requests"]
		requests.XListType, requests.XListMapKeys = nil, nil
		spec.Properties["requests"] = requests
		old.Spec.Versions[i].Schema.OpenAPIV3Schema.Properties["spec"] = spec
	}
	return old
}

func twoOfOneType(name string) *ResourceClaim {
	return &ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: ResourceClaimSpec{Requests: []ResourceRequest{{ResourceType: "example/count", Amount: 1}, {ResourceType: "example/count", Amount: 2}}}}
}
