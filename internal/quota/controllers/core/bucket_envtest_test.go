package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// raceClaims is the number of claims created at once against a limit of one less.
const raceClaims = 20

var raceConsumer = quotav1alpha1.ConsumerRef{APIGroup: "resourcemanager.miloapis.com", Kind: "Project", Name: "race"}

// Claims created at once against a bucket one short of them, decided by one controller or by two racing ones (as a
// leader handoff can run), against a real API server: the bucket's resourceVersion lets one reservation in at a time,
// so exactly the limit is granted and the last claim is denied.
func TestAllowanceBucketGrantsExactlyItsLimitAgainstARealAPIServer(t *testing.T) {
	for name, controllers := range map[string]int{"one controller": 1, "two racing controllers": 2} {
		t.Run(name, func(t *testing.T) { raceAtTheLimit(t, controllers) })
	}
}

func raceAtTheLimit(t *testing.T, controllers int) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("KUBEBUILDER_ASSETS must be set in CI; this test needs an envtest API server")
		}
		t.Skip("KUBEBUILDER_ASSETS is not set; run setup-envtest to provide an envtest API server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases", "quota")}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	require.NoError(t, err)
	defer func() { _ = env.Stop() }()
	scheme := runtime.NewScheme()
	require.NoError(t, quotav1alpha1.AddToScheme(scheme))
	direct, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	bucket := seedRaceBucket(ctx, t, direct, raceClaims-1)
	req := mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bucket)}}

	// Each controller has a cache of its own, as two processes would; the caches start before the claims exist.
	reconcilers := make([]*AllowanceBucketController, controllers)
	for i := range reconcilers {
		cl := startCachedCluster(ctx, t, cfg, scheme, 0)
		reconcilers[i] = &AllowanceBucketController{Scheme: scheme, Manager: fakeManager{cluster: fakeCluster{Cluster: cl, client: cl.GetClient(), reader: cl.GetAPIReader()}}}
	}
	var wg sync.WaitGroup
	for i := 0; i < raceClaims; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := direct.Create(ctx, raceClaim(i)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	for _, r := range reconcilers {
		wg.Add(1)
		go func(r *AllowanceBucketController) {
			defer wg.Done()
			reconcileUntilDecided(ctx, t, direct, r, req)
		}(r)
	}
	wg.Wait()

	granted, denied := raceDecisions(ctx, t, direct)
	assert.Equal(t, raceClaims-1, granted, "granted")
	assert.Equal(t, 1, denied, "denied")
	require.NoError(t, direct.Get(ctx, client.ObjectKeyFromObject(bucket), bucket))
	assert.EqualValues(t, raceClaims-1, bucket.Status.Allocated)
}

// reconcileUntilDecided reconciles the bucket, as its controller's queue would, until every claim of the race is
// decided. A reconcile that meets a resourceVersion conflict returns it and runs again.
func reconcileUntilDecided(ctx context.Context, t *testing.T, c client.Client, r *AllowanceBucketController, req mcreconcile.Request) {
	for ctx.Err() == nil {
		_, _ = r.Reconcile(ctx, req)
		if granted, denied := raceDecisions(ctx, t, c); granted+denied == raceClaims {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("the claims were not all decided before the deadline")
}

// raceDecisions counts the race's claims that are granted and denied, read live.
func raceDecisions(ctx context.Context, t *testing.T, c client.Client) (int, int) {
	var claims quotav1alpha1.ResourceClaimList
	if err := c.List(ctx, &claims, client.InNamespace("default")); err != nil {
		t.Error(err)
		return 0, 0
	}
	granted, denied := 0, 0
	for _, claim := range claims.Items {
		for _, allocation := range claim.Status.Allocations {
			switch allocation.Status {
			case quotav1alpha1.ResourceClaimAllocationStatusGranted:
				granted++
			case quotav1alpha1.ResourceClaimAllocationStatusDenied:
				denied++
			}
		}
	}
	return granted, denied
}

// seedRaceBucket creates the race consumer's active grant of limit and its bucket.
func seedRaceBucket(ctx context.Context, t *testing.T, c client.Client, limit int64) *quotav1alpha1.AllowanceBucket {
	t.Helper()
	grant := &quotav1alpha1.ResourceGrant{ObjectMeta: metav1.ObjectMeta{Name: "grant", Namespace: "default"}, Spec: quotav1alpha1.ResourceGrantSpec{ConsumerRef: raceConsumer, Allowances: []quotav1alpha1.Allowance{{ResourceType: "example/count", Buckets: []quotav1alpha1.Bucket{{Amount: limit}}}}}}
	require.NoError(t, c.Create(ctx, grant))
	grant.Status.Conditions = []metav1.Condition{{Type: quotav1alpha1.ResourceGrantActive, Status: metav1.ConditionTrue, Reason: quotav1alpha1.ResourceGrantActiveReason, LastTransitionTime: metav1.Now()}}
	require.NoError(t, c.Status().Update(ctx, grant))
	bucket := &quotav1alpha1.AllowanceBucket{ObjectMeta: metav1.ObjectMeta{Name: "bucket", Namespace: "default"}, Spec: quotav1alpha1.AllowanceBucketSpec{ConsumerRef: raceConsumer, ResourceType: "example/count"}}
	require.NoError(t, c.Create(ctx, bucket))
	return bucket
}

func raceClaim(i int) *quotav1alpha1.ResourceClaim {
	return &quotav1alpha1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("race-%02d", i), Namespace: "default"}, Spec: quotav1alpha1.ResourceClaimSpec{ConsumerRef: raceConsumer, Requests: []quotav1alpha1.ResourceRequest{{ResourceType: "example/count", Amount: 1}}}}
}
