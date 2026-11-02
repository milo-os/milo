package core

import (
	"context"
	"fmt"
	"path/filepath"
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
	r := &AllowanceBucketController{Scheme: scheme, Manager: fakeManager{cluster: cl}}
	req := mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bucket)}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Reconcile(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

// seedBucketPlane creates the namespace's bucket, one active grant and benchClaims granted claims, and sets the
// bucket's allocation to their sum, as a reconcile at rest would.
func seedBucketPlane(ctx context.Context, b *testing.B, c client.Client) *quotav1alpha1.AllowanceBucket {
	b.Helper()
	consumer := quotav1alpha1.ConsumerRef{APIGroup: "resourcemanager.miloapis.com", Kind: "Project", Name: "heavy"}
	resourceType := "core.miloapis.com/configmaps"
	grant := &quotav1alpha1.ResourceGrant{ObjectMeta: metav1.ObjectMeta{Name: "grant", Namespace: "default"}, Spec: quotav1alpha1.ResourceGrantSpec{ConsumerRef: consumer, Allowances: []quotav1alpha1.Allowance{{ResourceType: resourceType, Buckets: []quotav1alpha1.Bucket{{Amount: 100000}}}}}}
	must(b, c.Create(ctx, grant))
	grant.Status.Conditions = []metav1.Condition{{Type: quotav1alpha1.ResourceGrantActive, Status: metav1.ConditionTrue, Reason: "Bench", LastTransitionTime: metav1.Now()}}
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
