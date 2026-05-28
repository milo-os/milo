package policy

import (
	"context"
	"net/http"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/record"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.miloapis.com/milo/internal/quota/validation"
	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

func testScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = quotav1alpha1.AddToScheme(scheme)
	return scheme
}

// testCluster implements cluster.Cluster, returning only the fake client.
// Only GetClient is used by the reconcilers under test.
type testCluster struct {
	client client.Client
}

func (c *testCluster) GetClient() client.Client                       { return c.client }
func (c *testCluster) GetScheme() *runtime.Scheme                     { return nil }
func (c *testCluster) GetHTTPClient() *http.Client                    { return nil }
func (c *testCluster) GetConfig() *rest.Config                        { return nil }
func (c *testCluster) GetCache() cache.Cache                          { return nil }
func (c *testCluster) GetFieldIndexer() client.FieldIndexer           { return nil }
func (c *testCluster) GetEventRecorderFor(string) record.EventRecorder { return nil }
func (c *testCluster) GetEventRecorder(string) events.EventRecorder    { return nil }
func (c *testCluster) GetRESTMapper() meta.RESTMapper                  { return nil }
func (c *testCluster) GetAPIReader() client.Reader                    { return nil }
func (c *testCluster) Start(context.Context) error                    { return nil }

// testManager implements mcmanager.Manager with only GetCluster functional.
type testManager struct {
	cluster cluster.Cluster
}

func (m *testManager) GetCluster(_ context.Context, _ multicluster.ClusterName) (cluster.Cluster, error) {
	return m.cluster, nil
}

// Unused mcmanager.Manager methods — satisfy the interface.
func (m *testManager) Add(mcmanager.Runnable) error                            { return nil }
func (m *testManager) Elected() <-chan struct{}                                { return nil }
func (m *testManager) AddMetricsServerExtraHandler(string, http.Handler) error { return nil }
func (m *testManager) AddHealthzCheck(string, healthz.Checker) error           { return nil }
func (m *testManager) AddReadyzCheck(string, healthz.Checker) error            { return nil }
func (m *testManager) Start(context.Context) error                             { return nil }
func (m *testManager) GetWebhookServer() webhook.Server                        { return nil }
func (m *testManager) GetLogger() logr.Logger                                  { return logr.Discard() }
func (m *testManager) GetControllerOptions() config.Controller                 { return config.Controller{} }
func (m *testManager) ClusterFromContext(context.Context) (cluster.Cluster, error) {
	return nil, nil
}
func (m *testManager) GetManager(context.Context, multicluster.ClusterName) (manager.Manager, error) {
	return nil, nil
}
func (m *testManager) GetLocalManager() manager.Manager { return nil }
func (m *testManager) GetProvider() multicluster.Provider { return nil }
func (m *testManager) GetFieldIndexer() client.FieldIndexer { return nil }
func (m *testManager) Engage(context.Context, multicluster.ClusterName, cluster.Cluster) error {
	return nil
}

// newFakeClient creates a fake client builder with the test scheme and status subresource support.
func newFakeClient(scheme *runtime.Scheme, objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(objs...).
		Build()
}

// newGrantPolicy creates a minimal valid GrantCreationPolicy for testing.
func newGrantPolicy(name string, generation int64) *quotav1alpha1.GrantCreationPolicy {
	return &quotav1alpha1.GrantCreationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			UID:        types.UID("test-uid"),
			Generation: generation,
		},
		Spec: quotav1alpha1.GrantCreationPolicySpec{
			Trigger: quotav1alpha1.GrantTriggerSpec{
				Resource: quotav1alpha1.GrantTriggerResource{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
			},
			Target: quotav1alpha1.GrantTargetSpec{
				ResourceGrantTemplate: quotav1alpha1.ResourceGrantTemplate{
					Metadata: quotav1alpha1.ObjectMetaTemplate{
						Name:      "test-grant",
						Namespace: "default",
					},
					Spec: quotav1alpha1.ResourceGrantSpec{
						ConsumerRef: quotav1alpha1.ConsumerRef{
							APIGroup: "test",
							Kind:     "Test",
							Name:     "test",
						},
						Allowances: []quotav1alpha1.Allowance{
							{ResourceType: "cpu", Buckets: []quotav1alpha1.Bucket{{Amount: 1}}},
						},
					},
				},
			},
		},
	}
}

// newClaimPolicy creates a minimal valid ClaimCreationPolicy for testing.
func newClaimPolicy(name string, generation int64) *quotav1alpha1.ClaimCreationPolicy {
	return &quotav1alpha1.ClaimCreationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			UID:        types.UID("test-uid"),
			Generation: generation,
		},
		Spec: quotav1alpha1.ClaimCreationPolicySpec{
			Trigger: quotav1alpha1.ClaimTriggerSpec{
				Resource: quotav1alpha1.ClaimTriggerResource{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
			},
			Target: quotav1alpha1.ClaimTargetSpec{
				ResourceClaimTemplate: quotav1alpha1.ResourceClaimTemplate{
					Metadata: quotav1alpha1.ObjectMetaTemplate{
						Name:      "test-claim",
						Namespace: "default",
					},
					Spec: quotav1alpha1.ResourceClaimSpec{
						ConsumerRef: quotav1alpha1.ConsumerRef{
							APIGroup: "test",
							Kind:     "Test",
							Name:     "test",
						},
						Requests: []quotav1alpha1.ResourceRequest{
							{ResourceType: "cpu", Amount: 1},
						},
					},
				},
			},
		},
	}
}

// setupGrantReconciler creates a GrantCreationPolicyReconciler with real validators.
func setupGrantReconciler(t *testing.T, objs ...client.Object) (*GrantCreationPolicyReconciler, client.Client) {
	t.Helper()
	scheme := testScheme()
	c := newFakeClient(scheme, objs...)

	celValidator, err := validation.NewCELValidator()
	if err != nil {
		t.Fatalf("Failed to create CEL validator: %v", err)
	}

	rtv := &noopResourceTypeValidator{}
	gtv, err := validation.NewGrantTemplateValidator(rtv)
	if err != nil {
		t.Fatalf("Failed to create grant template validator: %v", err)
	}

	pv := validation.NewGrantCreationPolicyValidator(celValidator, gtv)

	mgr := &testManager{cluster: &testCluster{client: c}}

	return &GrantCreationPolicyReconciler{
		Scheme:          scheme,
		Manager:         mgr,
		PolicyValidator: pv,
	}, c
}

// setupClaimReconciler creates a ClaimCreationPolicyReconciler with real validators.
func setupClaimReconciler(t *testing.T, objs ...client.Object) (*ClaimCreationPolicyReconciler, client.Client) {
	t.Helper()
	scheme := testScheme()
	c := newFakeClient(scheme, objs...)

	rtv := &noopResourceTypeValidator{}
	pv := validation.NewClaimCreationPolicyValidator(rtv)

	mgr := &testManager{cluster: &testCluster{client: c}}

	return &ClaimCreationPolicyReconciler{
		Scheme:          scheme,
		Manager:         mgr,
		PolicyValidator: pv,
	}, c
}

// noopResourceTypeValidator accepts all resource types.
type noopResourceTypeValidator struct{}

func (v *noopResourceTypeValidator) ValidateResourceType(context.Context, string) error { return nil }
func (v *noopResourceTypeValidator) IsClaimingResourceAllowed(context.Context, string, quotav1alpha1.ConsumerRef, string, string) (bool, []string, error) {
	return true, nil, nil
}
func (v *noopResourceTypeValidator) IsResourceTypeRegistered(string) bool { return true }
func (v *noopResourceTypeValidator) HasSynced() bool                      { return true }

func reconcileRequest(name string) mcreconcile.Request {
	return mcreconcile.Request{
		Request: ctrl.Request{
			NamespacedName: types.NamespacedName{Name: name},
		},
	}
}

func TestGrantCreationPolicyReconciler_ObservedGenerationAdvancesOnNoOpSpecChange(t *testing.T) {
	policy := newGrantPolicy("test-policy", 1)
	reconciler, c := setupGrantReconciler(t, policy)
	ctx := context.Background()

	// First reconcile: generation=1 → observedGeneration should become 1
	if _, err := reconciler.Reconcile(ctx, reconcileRequest("test-policy")); err != nil {
		t.Fatalf("First reconcile failed: %v", err)
	}

	var after quotav1alpha1.GrantCreationPolicy
	if err := c.Get(ctx, types.NamespacedName{Name: "test-policy"}, &after); err != nil {
		t.Fatalf("Failed to get policy after first reconcile: %v", err)
	}
	if after.Status.ObservedGeneration != 1 {
		t.Errorf("Expected observedGeneration=1 after first reconcile, got %d", after.Status.ObservedGeneration)
	}

	// Simulate a spec-only change that bumps generation but produces identical conditions.
	// The fake client doesn't auto-increment generation, so we set it manually.
	after.Generation = 2
	if err := c.Update(ctx, &after); err != nil {
		t.Fatalf("Failed to update policy generation: %v", err)
	}

	// Second reconcile: generation=2, same validation result → observedGeneration must advance to 2
	if _, err := reconciler.Reconcile(ctx, reconcileRequest("test-policy")); err != nil {
		t.Fatalf("Second reconcile failed: %v", err)
	}

	if err := c.Get(ctx, types.NamespacedName{Name: "test-policy"}, &after); err != nil {
		t.Fatalf("Failed to get policy after second reconcile: %v", err)
	}
	if after.Status.ObservedGeneration != 2 {
		t.Errorf("Expected observedGeneration=2 after generation bump, got %d", after.Status.ObservedGeneration)
	}
}

func TestGrantCreationPolicyReconciler_NoStatusWriteWhenNothingChanges(t *testing.T) {
	policy := newGrantPolicy("test-policy", 1)
	reconciler, c := setupGrantReconciler(t, policy)
	ctx := context.Background()

	// First reconcile sets status
	if _, err := reconciler.Reconcile(ctx, reconcileRequest("test-policy")); err != nil {
		t.Fatalf("First reconcile failed: %v", err)
	}

	var after quotav1alpha1.GrantCreationPolicy
	if err := c.Get(ctx, types.NamespacedName{Name: "test-policy"}, &after); err != nil {
		t.Fatalf("Failed to get policy: %v", err)
	}
	rvAfterFirst := after.ResourceVersion

	// Second reconcile with no changes — should still succeed (no-op write is harmless with fake client)
	if _, err := reconciler.Reconcile(ctx, reconcileRequest("test-policy")); err != nil {
		t.Fatalf("Second reconcile failed: %v", err)
	}

	if err := c.Get(ctx, types.NamespacedName{Name: "test-policy"}, &after); err != nil {
		t.Fatalf("Failed to get policy: %v", err)
	}

	// With the DeepEqual optimization, resourceVersion should not change on a true no-op
	if after.ResourceVersion != rvAfterFirst {
		t.Errorf("Expected no status write on no-op reconcile: resourceVersion changed from %s to %s", rvAfterFirst, after.ResourceVersion)
	}
}

func TestClaimCreationPolicyReconciler_ObservedGenerationAdvancesOnNoOpSpecChange(t *testing.T) {
	policy := newClaimPolicy("test-policy", 1)
	reconciler, c := setupClaimReconciler(t, policy)
	ctx := context.Background()

	// First reconcile: generation=1
	if _, err := reconciler.Reconcile(ctx, reconcileRequest("test-policy")); err != nil {
		t.Fatalf("First reconcile failed: %v", err)
	}

	var after quotav1alpha1.ClaimCreationPolicy
	if err := c.Get(ctx, types.NamespacedName{Name: "test-policy"}, &after); err != nil {
		t.Fatalf("Failed to get policy: %v", err)
	}
	if after.Status.ObservedGeneration != 1 {
		t.Errorf("Expected observedGeneration=1, got %d", after.Status.ObservedGeneration)
	}

	// Bump generation without changing validation outcome
	after.Generation = 2
	if err := c.Update(ctx, &after); err != nil {
		t.Fatalf("Failed to update policy: %v", err)
	}

	// Second reconcile: observedGeneration must advance to 2
	if _, err := reconciler.Reconcile(ctx, reconcileRequest("test-policy")); err != nil {
		t.Fatalf("Second reconcile failed: %v", err)
	}

	if err := c.Get(ctx, types.NamespacedName{Name: "test-policy"}, &after); err != nil {
		t.Fatalf("Failed to get policy: %v", err)
	}
	if after.Status.ObservedGeneration != 2 {
		t.Errorf("Expected observedGeneration=2 after generation bump, got %d", after.Status.ObservedGeneration)
	}
}

func TestClaimCreationPolicyReconciler_NoStatusWriteWhenNothingChanges(t *testing.T) {
	policy := newClaimPolicy("test-policy", 1)
	reconciler, c := setupClaimReconciler(t, policy)
	ctx := context.Background()

	// First reconcile
	if _, err := reconciler.Reconcile(ctx, reconcileRequest("test-policy")); err != nil {
		t.Fatalf("First reconcile failed: %v", err)
	}

	var after quotav1alpha1.ClaimCreationPolicy
	if err := c.Get(ctx, types.NamespacedName{Name: "test-policy"}, &after); err != nil {
		t.Fatalf("Failed to get policy: %v", err)
	}
	rvAfterFirst := after.ResourceVersion

	// Second reconcile — true no-op
	if _, err := reconciler.Reconcile(ctx, reconcileRequest("test-policy")); err != nil {
		t.Fatalf("Second reconcile failed: %v", err)
	}

	if err := c.Get(ctx, types.NamespacedName{Name: "test-policy"}, &after); err != nil {
		t.Fatalf("Failed to get policy: %v", err)
	}
	if after.ResourceVersion != rvAfterFirst {
		t.Errorf("Expected no status write on no-op reconcile: resourceVersion changed from %s to %s", rvAfterFirst, after.ResourceVersion)
	}
}

func TestGrantCreationPolicyReconciler_ObservedGenerationSetOnValidationFailure(t *testing.T) {
	policy := newGrantPolicy("test-policy", 1)
	// Add an invalid CEL constraint to trigger validation failure
	policy.Spec.Trigger.Constraints = []quotav1alpha1.ConditionExpression{
		{Expression: "invalid[[["},
	}
	reconciler, c := setupGrantReconciler(t, policy)
	ctx := context.Background()

	if _, err := reconciler.Reconcile(ctx, reconcileRequest("test-policy")); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	var after quotav1alpha1.GrantCreationPolicy
	if err := c.Get(ctx, types.NamespacedName{Name: "test-policy"}, &after); err != nil {
		t.Fatalf("Failed to get policy: %v", err)
	}

	if after.Status.ObservedGeneration != 1 {
		t.Errorf("Expected observedGeneration=1 even on validation failure, got %d", after.Status.ObservedGeneration)
	}

	readyCond := meta.FindStatusCondition(after.Status.Conditions, quotav1alpha1.GrantCreationPolicyReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Errorf("Expected Ready=False on validation failure, got %v", readyCond)
	}
}
