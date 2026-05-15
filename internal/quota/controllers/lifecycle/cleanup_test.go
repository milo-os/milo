package lifecycle

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// testCluster implements cluster.Cluster with only GetClient functional.
type testCluster struct{ client client.Client }

func (c *testCluster) GetClient() client.Client                        { return c.client }
func (c *testCluster) GetScheme() *runtime.Scheme                      { return nil }
func (c *testCluster) GetHTTPClient() *http.Client                     { return nil }
func (c *testCluster) GetConfig() *rest.Config                         { return nil }
func (c *testCluster) GetCache() cache.Cache                           { return nil }
func (c *testCluster) GetFieldIndexer() client.FieldIndexer            { return nil }
func (c *testCluster) GetEventRecorderFor(string) record.EventRecorder { return nil }
func (c *testCluster) GetRESTMapper() meta.RESTMapper                  { return nil }
func (c *testCluster) GetAPIReader() client.Reader                     { return nil }
func (c *testCluster) Start(context.Context) error                     { return nil }

// testManager implements mcmanager.Manager with only GetCluster functional.
type testManager struct{ cluster cluster.Cluster }

func (m *testManager) GetCluster(_ context.Context, _ string) (cluster.Cluster, error) {
	return m.cluster, nil
}
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
func (m *testManager) GetManager(context.Context, string) (manager.Manager, error) {
	return nil, nil
}
func (m *testManager) GetLocalManager() manager.Manager                      { return nil }
func (m *testManager) GetProvider() multicluster.Provider                    { return nil }
func (m *testManager) GetFieldIndexer() client.FieldIndexer                  { return nil }
func (m *testManager) Engage(context.Context, string, cluster.Cluster) error { return nil }

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := quotav1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add to scheme: %v", err)
	}
	return s
}

// autoClaim returns an auto-created ResourceClaim with the given Granted
// condition (or no condition when status=="").
func autoClaim(name, namespace, status, reason string, created time.Time) *quotav1alpha1.ResourceClaim {
	claim := &quotav1alpha1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(name),
			Labels: map[string]string{
				"quota.miloapis.com/auto-created": "true",
				"quota.miloapis.com/policy":       "test-policy",
			},
			Annotations: map[string]string{
				"quota.miloapis.com/created-by":    "claim-creation-plugin",
				"quota.miloapis.com/resource-name": "test-resource",
			},
			CreationTimestamp: metav1.Time{Time: created},
		},
	}
	if status != "" {
		claim.Status.Conditions = []metav1.Condition{{
			Type:               quotav1alpha1.ResourceClaimGranted,
			Status:             metav1.ConditionStatus(status),
			Reason:             reason,
			LastTransitionTime: metav1.Time{Time: created},
		}}
	}
	return claim
}

func manualClaim(name, namespace string) *quotav1alpha1.ResourceClaim {
	return &quotav1alpha1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(name),
		},
	}
}

// TestDeniedAutoClaimCleanupController covers every decision in Reconcile:
// denied claims deleted immediately, pending claims deleted once stale,
// fresh pending claims requeued rather than deleted, granted and manual
// claims ignored.
func TestDeniedAutoClaimCleanupController(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	scheme := testScheme(t)

	cases := []struct {
		name           string
		claim          *quotav1alpha1.ResourceClaim
		stalePendingAge time.Duration
		wantDeleted    bool
		wantRequeue    bool
	}{
		{
			name:        "denied auto-created claim is deleted",
			claim:       autoClaim("denied", "default", "False", quotav1alpha1.ResourceClaimDeniedReason, now.Add(-1*time.Minute)),
			wantDeleted: true,
		},
		{
			name:            "stale pending auto-created claim is deleted",
			claim:           autoClaim("stale-pending", "default", "False", quotav1alpha1.ResourceClaimPendingReason, now.Add(-10*time.Minute)),
			stalePendingAge: 5 * time.Minute,
			wantDeleted:     true,
		},
		{
			name:            "fresh pending auto-created claim is requeued",
			claim:           autoClaim("fresh-pending", "default", "False", quotav1alpha1.ResourceClaimPendingReason, now.Add(-1*time.Minute)),
			stalePendingAge: 5 * time.Minute,
			wantDeleted:     false,
			wantRequeue:     true,
		},
		{
			name:        "pending auto-created claim with no Granted condition is considered pending",
			claim:       autoClaim("no-cond", "default", "", "", now.Add(-10*time.Minute)),
			wantDeleted: true,
		},
		{
			name:        "granted auto-created claim is left alone",
			claim:       autoClaim("granted", "default", "True", quotav1alpha1.ResourceClaimGrantedReason, now.Add(-10*time.Minute)),
			wantDeleted: false,
		},
		{
			name:        "manual claim is ignored",
			claim:       manualClaim("manual", "default"),
			wantDeleted: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.claim).Build()
			mgr := &testManager{cluster: &testCluster{client: cli}}

			r := &DeniedAutoClaimCleanupController{
				Scheme:               scheme,
				Manager:              mgr,
				StalePendingClaimAge: tc.stalePendingAge,
				logger:               logr.Discard(),
				now:                  func() time.Time { return now },
			}

			req := mcreconcile.Request{
				Request: ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      tc.claim.Name,
						Namespace: tc.claim.Namespace,
					},
				},
			}

			result, err := r.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("Reconcile returned error: %v", err)
			}

			var got quotav1alpha1.ResourceClaim
			getErr := cli.Get(context.Background(), client.ObjectKey{Name: tc.claim.Name, Namespace: tc.claim.Namespace}, &got)
			deleted := getErr != nil

			if deleted != tc.wantDeleted {
				t.Errorf("deleted=%v want=%v (get err=%v)", deleted, tc.wantDeleted, getErr)
			}

			if tc.wantRequeue && result.RequeueAfter == 0 {
				t.Errorf("expected RequeueAfter > 0, got %v", result.RequeueAfter)
			}
			if !tc.wantRequeue && !tc.wantDeleted && result.RequeueAfter != 0 {
				t.Errorf("expected zero RequeueAfter for no-op, got %v", result.RequeueAfter)
			}
		})
	}
}
