package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
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
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		b.Skip("KUBEBUILDER_ASSETS is not set; run setup-envtest to provide an envtest API server")
	}
	// The reconciler logs each pending claim; with no sink, controller-runtime prints a stack trace after 30 s.
	ctrl.SetLogger(logr.Discard())
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
	cl := startCachedCluster(ctx, b, cfg, scheme, benchClaims)
	reads := &liveReads{}
	r := &AllowanceBucketController{Scheme: scheme, Manager: fakeManager{cluster: countingCluster(cl, reads)}}
	req := mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bucket)}}
	b.Run("at-rest", func(b *testing.B) {
		*reads = liveReads{}
		for i := 0; i < b.N; i++ {
			must(b, reconcileErr(r.Reconcile(ctx, req)))
		}
		b.ReportMetric(float64(reads.lists)/float64(b.N), "live-claim-LISTs/op")
		b.ReportMetric(float64(reads.gets)/float64(b.N), "live-claim-GETs/op")
	})
	full := seedFullBucket(ctx, b, direct)
	fullReq := mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(full)}}
	b.Run("deny", func(b *testing.B) {
		*reads = liveReads{}
		for i := 0; i < b.N; i++ {
			denyOne(ctx, b, direct, cl, r, fullReq)
		}
		b.ReportMetric(float64(reads.lists)/float64(b.N), "live-claim-LISTs/op")
		b.ReportMetric(float64(reads.gets)/float64(b.N), "live-claim-GETs/op")
	})
	b.Run("burst-100", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			*reads = liveReads{}
			reconciles := burst(ctx, b, direct, r, req)
			b.ReportMetric(float64(reconciles), "reconciles/burst")
			b.ReportMetric(float64(reads.lists), "live-claim-LISTs/burst")
			b.ReportMetric(float64(reads.gets), "live-claim-GETs/burst")
		}
	})
}

// burstRound names each burst's claims apart: the sub-benchmark runs -count times on one API server.
var burstRound int

// burst interleaves 100 creates with reconciles, as a create burst drives the controller: each create, then one
// reconcile. The cache lags the controller's own grants here, which is where the fix may fall back to a live LIST.
// Then it reconciles until no claim of the burst is pending. It returns the number of reconciles.
func burst(ctx context.Context, b *testing.B, c client.Client, r *AllowanceBucketController, req mcreconcile.Request) int {
	b.Helper()
	round := burstRound
	burstRound++
	consumer := quotav1alpha1.ConsumerRef{APIGroup: "resourcemanager.miloapis.com", Kind: "Project", Name: "heavy"}
	reconciles := 0
	for j := 0; j < 100; j++ {
		claim := &quotav1alpha1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("burst-%d-%03d", round, j), Namespace: "default"}, Spec: quotav1alpha1.ResourceClaimSpec{ConsumerRef: consumer, Requests: []quotav1alpha1.ResourceRequest{{ResourceType: "core.miloapis.com/configmaps", Amount: 1}}}}
		must(b, c.Create(ctx, claim))
		must(b, reconcileErr(r.Reconcile(ctx, req)))
		reconciles++
	}
	for pendingClaims(ctx, b, c, round) > 0 && reconciles < 200 {
		must(b, reconcileErr(r.Reconcile(ctx, req)))
		reconciles++
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

// fullResourceType has no allowance in the plane's grant, so its bucket's limit is 0 and every request is denied.
const fullResourceType = "core.miloapis.com/secrets"

// denials names each denied claim apart: a sub-benchmark runs more than once on one API server.
var denials int

// seedFullBucket creates a bucket of the heavy consumer for a resource type with no allowance.
func seedFullBucket(ctx context.Context, b *testing.B, c client.Client) *quotav1alpha1.AllowanceBucket {
	b.Helper()
	consumer := quotav1alpha1.ConsumerRef{APIGroup: "resourcemanager.miloapis.com", Kind: "Project", Name: "heavy"}
	bucket := &quotav1alpha1.AllowanceBucket{ObjectMeta: metav1.ObjectMeta{Name: "bucket-full", Namespace: "default"}, Spec: quotav1alpha1.AllowanceBucketSpec{ConsumerRef: consumer, ResourceType: fullResourceType}}
	must(b, c.Create(ctx, bucket))
	return bucket
}

// denyOne creates one claim over the full bucket's limit and times the reconcile that denies it. As the controller,
// it reconciles once the cache holds the claim, and it checks that the claim was denied.
func denyOne(ctx context.Context, b *testing.B, c client.Client, cl cluster.Cluster, r *AllowanceBucketController, req mcreconcile.Request) {
	b.Helper()
	b.StopTimer()
	denials++
	consumer := quotav1alpha1.ConsumerRef{APIGroup: "resourcemanager.miloapis.com", Kind: "Project", Name: "heavy"}
	claim := &quotav1alpha1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("deny-%04d", denials), Namespace: "default"}, Spec: quotav1alpha1.ResourceClaimSpec{ConsumerRef: consumer, Requests: []quotav1alpha1.ResourceRequest{{ResourceType: fullResourceType, Amount: 1}}}}
	must(b, c.Create(ctx, claim))
	for cl.GetCache().Get(ctx, client.ObjectKeyFromObject(claim), &quotav1alpha1.ResourceClaim{}) != nil {
		time.Sleep(time.Millisecond)
	}
	b.StartTimer()
	must(b, reconcileErr(r.Reconcile(ctx, req)))
	b.StopTimer()
	must(b, c.Get(ctx, client.ObjectKeyFromObject(claim), claim))
	if len(claim.Status.Allocations) != 1 || claim.Status.Allocations[0].Status != quotav1alpha1.ResourceClaimAllocationStatusDenied {
		b.Fatalf("claim %s was not denied: %+v", claim.Name, claim.Status.Allocations)
	}
	b.StartTimer()
}

// liveReads counts the live ResourceClaim reads a reconcile makes.
type liveReads struct{ lists, gets int }

// countingCluster wraps a cluster so that its API reader counts the live ResourceClaim LISTs and GETs.
func countingCluster(cl cluster.Cluster, reads *liveReads) fakeCluster {
	return fakeCluster{Cluster: cl, client: cl.GetClient(), reader: countingReader{Reader: cl.GetAPIReader(), reads: reads}}
}

// countingReader counts ResourceClaim LISTs and GETs, and passes every read on.
type countingReader struct {
	client.Reader
	reads *liveReads
}

func (c countingReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*quotav1alpha1.ResourceClaim); ok {
		c.reads.gets++
	}
	return c.Reader.Get(ctx, key, obj, opts...)
}

func (c countingReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*quotav1alpha1.ResourceClaimList); ok {
		c.reads.lists++
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
// until its cache holds wantClaims claims.
func startCachedCluster(ctx context.Context, b testing.TB, cfg *rest.Config, scheme *runtime.Scheme, wantClaims int) cluster.Cluster {
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
	if len(claims.Items) != wantClaims {
		b.Fatalf("the cache holds %d claims, want %d", len(claims.Items), wantClaims)
	}
	return cl
}

func must(b testing.TB, err error) {
	b.Helper()
	if err != nil {
		b.Fatal(err)
	}
}
