package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// benchClaims is staging's heaviest project plane on 2026-11-02: 2,159 ResourceClaims of one consumer.
const benchClaims = 2159

// BenchmarkAllowanceBucketReconcile times one bucket reconcile against a real API server whose plane holds
// benchClaims granted claims, the shape where milo#949's live claim LIST costs the most.
func BenchmarkAllowanceBucketReconcile(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases", "quota")}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = env.Stop() }()
	scheme := runtime.NewScheme()
	if err := quotav1alpha1.AddToScheme(scheme); err != nil {
		b.Fatal(err)
	}
	direct, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		b.Fatal(err)
	}
	bucket := seedBucketPlane(ctx, b, direct)
	cl := startCachedCluster(ctx, b, cfg, scheme)
	lists := 0
	r := &AllowanceBucketController{Scheme: scheme, Manager: fakeManager{cluster: countingCluster(cl, &lists)}}
	req := mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bucket)}}
	b.Run("at-rest", func(b *testing.B) {
		lists = 0
		for i := 0; i < b.N; i++ {
			must(b, reconcileErr(r.Reconcile(ctx, req)))
		}
		b.ReportMetric(float64(lists)/float64(b.N), "live-claim-LISTs/op")
	})
	b.Run("burst-100", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			lists = 0
			reconciles := burst(ctx, b, direct, r, req, i)
			b.ReportMetric(float64(reconciles), "reconciles/burst")
			b.ReportMetric(float64(lists), "live-claim-LISTs/burst")
		}
	})
}

// burst creates 100 pending claims at once, then reconciles the bucket until none is pending; it returns the
// number of reconciles. The cache lags the controller's own grants here, which is where a fallback can happen.
func burst(ctx context.Context, b *testing.B, c client.Client, r *AllowanceBucketController, req mcreconcile.Request, round int) int {
	b.Helper()
	consumer := quotav1alpha1.ConsumerRef{APIGroup: "resourcemanager.miloapis.com", Kind: "Project", Name: "heavy"}
	for j := 0; j < 100; j++ {
		claim := &quotav1alpha1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("burst-%d-%03d", round, j), Namespace: "default"}, Spec: quotav1alpha1.ResourceClaimSpec{ConsumerRef: consumer, Requests: []quotav1alpha1.ResourceRequest{{ResourceType: "core.miloapis.com/configmaps", Amount: 1}}}}
		must(b, c.Create(ctx, claim))
	}
	reconciles := 0
	for pending := 100; pending > 0 && reconciles < 1000; reconciles++ {
		must(b, reconcileErr(r.Reconcile(ctx, req)))
		pending = pendingClaims(ctx, b, c, round)
	}
	return reconciles
}

// pendingClaims counts the burst's claims that hold no allocation yet, read live.
func pendingClaims(ctx context.Context, b *testing.B, c client.Client, round int) int {
	var claims quotav1alpha1.ResourceClaimList
	must(b, c.List(ctx, &claims, client.InNamespace("default")))
	n := 0
	for _, claim := range claims.Items {
		if strings.HasPrefix(claim.Name, fmt.Sprintf("burst-%d-", round)) && len(claim.Status.Allocations) == 0 {
			n++
		}
	}
	return n
}

// countingCluster wraps a cluster so that its API reader counts the live ResourceClaim LISTs.
func countingCluster(cl cluster.Cluster, lists *int) fakeCluster {
	return fakeCluster{Cluster: cl, client: cl.GetClient(), reader: countingReader{Reader: cl.GetAPIReader(), lists: lists}}
}

// countingReader counts ResourceClaim LISTs, and passes every read on.
type countingReader struct {
	client.Reader
	lists *int
}

func (c countingReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*quotav1alpha1.ResourceClaimList); ok {
		*c.lists++
	}
	return c.Reader.List(ctx, list, opts...)
}

func reconcileErr(_ ctrl.Result, err error) error { return err }

// seedBucketPlane creates the namespace's bucket, one active grant and benchClaims granted claims, and sets the
// bucket's allocation to their sum, as a reconcile at rest would.
func seedBucketPlane(ctx context.Context, b *testing.B, c client.Client) *quotav1alpha1.AllowanceBucket {
	b.Helper()
	consumer := quotav1alpha1.ConsumerRef{APIGroup: "resourcemanager.miloapis.com", Kind: "Project", Name: "heavy"}
	resourceType := "core.miloapis.com/configmaps"
	grant := &quotav1alpha1.ResourceGrant{ObjectMeta: metav1.ObjectMeta{Name: "grant", Namespace: "default"}, Spec: quotav1alpha1.ResourceGrantSpec{ConsumerRef: consumer, Allowances: []quotav1alpha1.Allowance{{ResourceType: resourceType, Buckets: []quotav1alpha1.Bucket{{Amount: 100000}}}}}}
	must(b, c.Create(ctx, grant))
	grant.Status.Conditions = []metav1.Condition{{Type: quotav1alpha1.ResourceGrantActive, Status: metav1.ConditionTrue, Reason: quotav1alpha1.ResourceGrantActiveReason, LastTransitionTime: metav1.Now()}}
	must(b, c.Status().Update(ctx, grant))
	for i := 0; i < benchClaims; i++ {
		claim := &quotav1alpha1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("claim-%04d", i), Namespace: "default"}, Spec: quotav1alpha1.ResourceClaimSpec{ConsumerRef: consumer, Requests: []quotav1alpha1.ResourceRequest{{ResourceType: resourceType, Amount: 1}}}}
		must(b, c.Create(ctx, claim))
		claim.Status.Allocations = []quotav1alpha1.ResourceClaimAllocationStatus{{ResourceType: resourceType, Status: quotav1alpha1.ResourceClaimAllocationStatusGranted, AllocatedAmount: 1, AllocatingBucket: "bucket", LastTransitionTime: metav1.Now()}}
		must(b, c.Status().Update(ctx, claim))
	}
	bucket := &quotav1alpha1.AllowanceBucket{ObjectMeta: metav1.ObjectMeta{Name: "bucket", Namespace: "default"}, Spec: quotav1alpha1.AllowanceBucketSpec{ConsumerRef: consumer, ResourceType: resourceType}}
	must(b, c.Create(ctx, bucket))
	bucket.Status.Limit, bucket.Status.Allocated, bucket.Status.ClaimCount = 100000, benchClaims, benchClaims
	must(b, c.Status().Update(ctx, bucket))
	return bucket
}

// startCachedCluster starts an informer-backed cluster with the claim index the controller registers, and waits
// for its cache.
func startCachedCluster(ctx context.Context, b *testing.B, cfg *rest.Config, scheme *runtime.Scheme) cluster.Cluster {
	b.Helper()
	cl, err := cluster.New(cfg, func(o *cluster.Options) { o.Scheme = scheme })
	must(b, err)
	must(b, cl.GetFieldIndexer().IndexField(ctx, &quotav1alpha1.ResourceClaim{}, resourceClaimConsumerRefIndex, func(obj client.Object) []string {
		return []string{consumerRefKey(obj.(*quotav1alpha1.ResourceClaim).Spec.ConsumerRef)}
	}))
	go func() { _ = cl.Start(ctx) }()
	if !cl.GetCache().WaitForCacheSync(ctx) {
		b.Fatal("the cache did not sync")
	}
	var claims quotav1alpha1.ResourceClaimList
	must(b, cl.GetCache().List(ctx, &claims))
	if len(claims.Items) != benchClaims {
		b.Fatalf("the cache holds %d claims, want %d", len(claims.Items), benchClaims)
	}
	return cl
}

func must(b *testing.B, err error) {
	b.Helper()
	if err != nil {
		b.Fatal(err)
	}
}
