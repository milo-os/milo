package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

type fakeCluster struct {
	cluster.Cluster
	client client.Client
	reader client.Reader
}

func (c fakeCluster) GetClient() client.Client { return c.client }
func (c fakeCluster) GetAPIReader() client.Reader {
	if c.reader != nil {
		return c.reader
	}
	return c.client
}

type fakeManager struct {
	mcmanager.Manager
	cluster cluster.Cluster
}

func (m fakeManager) GetCluster(context.Context, multicluster.ClusterName) (cluster.Cluster, error) {
	return m.cluster, nil
}

func TestAllowanceBucketReconcileStopsAtReservationConflict(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, quotav1alpha1.AddToScheme(scheme))

	consumer := quotav1alpha1.ConsumerRef{
		APIGroup: "resourcemanager.miloapis.com",
		Kind:     "Project",
		Name:     "p1",
	}
	resourceType := "resourcemanager.miloapis.com/projects"
	bucketKey := types.NamespacedName{
		Name:      generateAllowanceBucketName(resourceType, consumer),
		Namespace: getBucketNamespace(consumer),
	}

	bucket := &quotav1alpha1.AllowanceBucket{
		ObjectMeta: metav1.ObjectMeta{Name: bucketKey.Name, Namespace: bucketKey.Namespace},
		Spec:       quotav1alpha1.AllowanceBucketSpec{ConsumerRef: consumer, ResourceType: resourceType},
	}
	grant := &quotav1alpha1.ResourceGrant{
		ObjectMeta: metav1.ObjectMeta{Name: "grant", Namespace: "milo-system"},
		Spec: quotav1alpha1.ResourceGrantSpec{
			ConsumerRef: consumer,
			Allowances: []quotav1alpha1.Allowance{{
				ResourceType: resourceType,
				Buckets:      []quotav1alpha1.Bucket{{Amount: 10}},
			}},
		},
		Status: quotav1alpha1.ResourceGrantStatus{
			Conditions: []metav1.Condition{{
				Type:               quotav1alpha1.ResourceGrantActive,
				Status:             metav1.ConditionTrue,
				Reason:             "Active",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
	claim := &quotav1alpha1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: "milo-system"},
		Spec: quotav1alpha1.ResourceClaimSpec{
			ConsumerRef: consumer,
			Requests:    []quotav1alpha1.ResourceRequest{{ResourceType: resourceType, Amount: 1}},
		},
	}

	bucketStatusUpdates := 0
	claimStatusPatches := 0
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&quotav1alpha1.ResourceGrant{}, "spec.consumerRef.kind", func(obj client.Object) []string {
			return []string{obj.(*quotav1alpha1.ResourceGrant).Spec.ConsumerRef.Kind}
		}).
		WithIndex(&quotav1alpha1.ResourceGrant{}, "spec.consumerRef.name", func(obj client.Object) []string {
			return []string{obj.(*quotav1alpha1.ResourceGrant).Spec.ConsumerRef.Name}
		}).
		WithIndex(&quotav1alpha1.ResourceClaim{}, "spec.consumerRef.kind", func(obj client.Object) []string {
			return []string{obj.(*quotav1alpha1.ResourceClaim).Spec.ConsumerRef.Kind}
		}).
		WithIndex(&quotav1alpha1.ResourceClaim{}, "spec.consumerRef.name", func(obj client.Object) []string {
			return []string{obj.(*quotav1alpha1.ResourceClaim).Spec.ConsumerRef.Name}
		}).
		WithObjects(bucket, grant, claim).
		WithStatusSubresource(&quotav1alpha1.AllowanceBucket{}, &quotav1alpha1.ResourceClaim{}).
		WithIndex(&quotav1alpha1.ResourceClaim{}, resourceClaimConsumerRefIndex, func(obj client.Object) []string {
			return []string{consumerRefKey(obj.(*quotav1alpha1.ResourceClaim).Spec.ConsumerRef)}
		}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if _, ok := obj.(*quotav1alpha1.AllowanceBucket); ok {
					bucketStatusUpdates++
					return apierrors.NewConflict(schema.GroupResource{Group: "quota.miloapis.com", Resource: "allowancebuckets"}, obj.GetName(), nil)
				}
				return cl.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
			SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				claimStatusPatches++
				return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	r := &AllowanceBucketController{
		Scheme:  scheme,
		Manager: fakeManager{cluster: fakeCluster{client: c}},
	}

	_, err := r.Reconcile(context.Background(), mcreconcile.Request{Request: ctrl.Request{NamespacedName: bucketKey}})

	require.Error(t, err)
	assert.True(t, apierrors.IsConflict(err), "expected a conflict error so the workqueue retries with backoff, got %v", err)
	assert.Equal(t, 1, bucketStatusUpdates, "the reconcile must stop at the conflicting reservation instead of writing the stale bucket again")
	assert.Zero(t, claimStatusPatches, "the claim must not be granted when the reservation was not written")
}

// A queued event may run before the informer observes our preceding status write.
// The live bucket's resource version must be used, without weakening the write CAS.
func TestAllowanceBucketReconcileReadsCurrentBucket(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, quotav1alpha1.AddToScheme(scheme))
	bucket := &quotav1alpha1.AllowanceBucket{ObjectMeta: metav1.ObjectMeta{Name: "bucket", Namespace: "milo-system"}, Status: quotav1alpha1.AllowanceBucketStatus{Limit: 1, Available: 1}}
	live := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&quotav1alpha1.ResourceGrant{}, "spec.consumerRef.kind", func(obj client.Object) []string {
			return []string{obj.(*quotav1alpha1.ResourceGrant).Spec.ConsumerRef.Kind}
		}).
		WithIndex(&quotav1alpha1.ResourceGrant{}, "spec.consumerRef.name", func(obj client.Object) []string {
			return []string{obj.(*quotav1alpha1.ResourceGrant).Spec.ConsumerRef.Name}
		}).
		WithIndex(&quotav1alpha1.ResourceClaim{}, "spec.consumerRef.kind", func(obj client.Object) []string {
			return []string{obj.(*quotav1alpha1.ResourceClaim).Spec.ConsumerRef.Kind}
		}).
		WithIndex(&quotav1alpha1.ResourceClaim{}, "spec.consumerRef.name", func(obj client.Object) []string {
			return []string{obj.(*quotav1alpha1.ResourceClaim).Spec.ConsumerRef.Name}
		}).WithObjects(bucket).WithStatusSubresource(&quotav1alpha1.AllowanceBucket{}).WithIndex(&quotav1alpha1.ResourceClaim{}, resourceClaimConsumerRefIndex, func(obj client.Object) []string {
		return []string{consumerRefKey(obj.(*quotav1alpha1.ResourceClaim).Spec.ConsumerRef)}
	}).Build()
	require.NoError(t, live.Get(ctx, client.ObjectKeyFromObject(bucket), bucket))
	stale := bucket.DeepCopy()
	bucket.Status.Limit = 2
	bucket.Status.Available = 2
	require.NoError(t, live.Status().Update(ctx, bucket))
	cached := interceptor.NewClient(live, interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if b, ok := obj.(*quotav1alpha1.AllowanceBucket); ok {
			*b = *stale.DeepCopy()
			return nil
		}
		return c.Get(ctx, key, obj, opts...)
	}})
	r := &AllowanceBucketController{Scheme: scheme, Manager: fakeManager{cluster: fakeCluster{client: cached, reader: live}}}
	_, err := r.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bucket)}})
	require.NoError(t, err)
	require.NoError(t, live.Get(ctx, client.ObjectKeyFromObject(bucket), bucket))
	assert.Zero(t, bucket.Status.Limit, "no active grants remain")
}

func TestAllowanceBucketDoesNotReserveFromStaleClaims(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, quotav1alpha1.AddToScheme(scheme))
	consumer := quotav1alpha1.ConsumerRef{Kind: "Project", Name: "p1"}
	resourceType := "example/count"
	bucket := &quotav1alpha1.AllowanceBucket{ObjectMeta: metav1.ObjectMeta{Name: "bucket", Namespace: "milo-system"}, Spec: quotav1alpha1.AllowanceBucketSpec{ConsumerRef: consumer, ResourceType: resourceType}, Status: quotav1alpha1.AllowanceBucketStatus{Limit: 1, Allocated: 1}}
	grant := &quotav1alpha1.ResourceGrant{ObjectMeta: metav1.ObjectMeta{Name: "grant"}, Spec: quotav1alpha1.ResourceGrantSpec{ConsumerRef: consumer, Allowances: []quotav1alpha1.Allowance{{ResourceType: resourceType, Buckets: []quotav1alpha1.Bucket{{Amount: 1}}}}}, Status: quotav1alpha1.ResourceGrantStatus{Conditions: []metav1.Condition{{Type: quotav1alpha1.ResourceGrantActive, Status: metav1.ConditionTrue}}}}
	granted := &quotav1alpha1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: "first"}, Spec: quotav1alpha1.ResourceClaimSpec{ConsumerRef: consumer, Requests: []quotav1alpha1.ResourceRequest{{ResourceType: resourceType, Amount: 1}}}, Status: quotav1alpha1.ResourceClaimStatus{Allocations: []quotav1alpha1.ResourceClaimAllocationStatus{{ResourceType: resourceType, Status: quotav1alpha1.ResourceClaimAllocationStatusGranted, AllocatedAmount: 1, AllocatingBucket: "bucket"}}}}
	pending := granted.DeepCopy()
	pending.Name = "second"
	pending.Status = quotav1alpha1.ResourceClaimStatus{}
	live := fake.NewClientBuilder().WithScheme(scheme).WithObjects(bucket, grant, granted, pending).WithStatusSubresource(&quotav1alpha1.AllowanceBucket{}).
		WithIndex(&quotav1alpha1.ResourceGrant{}, "spec.consumerRef.kind", func(obj client.Object) []string {
			return []string{obj.(*quotav1alpha1.ResourceGrant).Spec.ConsumerRef.Kind}
		}).
		WithIndex(&quotav1alpha1.ResourceGrant{}, "spec.consumerRef.name", func(obj client.Object) []string {
			return []string{obj.(*quotav1alpha1.ResourceGrant).Spec.ConsumerRef.Name}
		}).
		WithIndex(&quotav1alpha1.ResourceClaim{}, "spec.consumerRef.kind", func(obj client.Object) []string {
			return []string{obj.(*quotav1alpha1.ResourceClaim).Spec.ConsumerRef.Kind}
		}).
		WithIndex(&quotav1alpha1.ResourceClaim{}, "spec.consumerRef.name", func(obj client.Object) []string {
			return []string{obj.(*quotav1alpha1.ResourceClaim).Spec.ConsumerRef.Name}
		}).Build()
	patches := 0
	cached := interceptor.NewClient(live, interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if claims, ok := list.(*quotav1alpha1.ResourceClaimList); ok {
				old := granted.DeepCopy()
				old.Status = quotav1alpha1.ResourceClaimStatus{}
				claims.Items = []quotav1alpha1.ResourceClaim{*old, *pending}
				return nil
			}
			return c.List(ctx, list, opts...)
		},
		SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			claim := obj.(*quotav1alpha1.ResourceClaim)
			patches++
			assert.Equal(t, "second", claim.Name, "already granted claims must not be reserved again")
			assert.Equal(t, quotav1alpha1.ResourceClaimAllocationStatusDenied, claim.Status.Allocations[0].Status, "the first claim already occupies all capacity")
			return nil
		},
	})
	r := &AllowanceBucketController{Scheme: scheme, Manager: fakeManager{cluster: fakeCluster{client: cached, reader: live}}}
	_, err := r.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bucket)}})
	require.NoError(t, err)
	require.Equal(t, 1, patches)
	require.NoError(t, live.Get(ctx, client.ObjectKeyFromObject(bucket), bucket))
	assert.EqualValues(t, 1, bucket.Status.Allocated)
	assert.Zero(t, bucket.Status.Available)
}

// Live grant reads must stay scoped as the number of project consumers grows.
func TestAllowanceBucketGrantReadIsConsumerScoped(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, quotav1alpha1.AddToScheme(scheme))
	consumer := quotav1alpha1.ConsumerRef{Kind: "Project", Name: "p1"}
	bucket := &quotav1alpha1.AllowanceBucket{Spec: quotav1alpha1.AllowanceBucketSpec{ConsumerRef: consumer, ResourceType: "example/count"}}
	reader := interceptor.NewClient(fake.NewClientBuilder().WithScheme(scheme).Build(), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			options := (&client.ListOptions{}).ApplyOptions(opts)
			require.NotNil(t, options.FieldSelector, "avoid fetching every consumer's grants")
			kind, ok := options.FieldSelector.RequiresExactMatch("spec.consumerRef.kind")
			require.True(t, ok)
			require.Equal(t, consumer.Kind, kind)
			name, ok := options.FieldSelector.RequiresExactMatch("spec.consumerRef.name")
			require.True(t, ok)
			require.Equal(t, consumer.Name, name)
			assert.Empty(t, options.Namespace, "consumer grants may span namespaces")
			list.(*quotav1alpha1.ResourceGrantList).Items = []quotav1alpha1.ResourceGrant{{
				ObjectMeta: metav1.ObjectMeta{Name: "grant"},
				Spec:       quotav1alpha1.ResourceGrantSpec{ConsumerRef: consumer, Allowances: []quotav1alpha1.Allowance{{ResourceType: "example/count", Buckets: []quotav1alpha1.Bucket{{Amount: 7}}}}},
				Status:     quotav1alpha1.ResourceGrantStatus{Conditions: []metav1.Condition{{Type: quotav1alpha1.ResourceGrantActive, Status: metav1.ConditionTrue}}},
			}}
			return nil
		},
	})
	require.NoError(t, (&AllowanceBucketController{}).updateLimitsFromGrants(context.Background(), reader, bucket))
	assert.EqualValues(t, 7, bucket.Status.Limit)
	assert.EqualValues(t, 1, bucket.Status.GrantCount)
}

// claimTestCluster builds a live store holding the bucket, its grant and the claims, and a cache with the consumer
// index the controller registers. countLive counts the live ResourceClaim LISTs a reconcile makes.
func claimTestCluster(t *testing.T, bucket *quotav1alpha1.AllowanceBucket, grant *quotav1alpha1.ResourceGrant, claims []*quotav1alpha1.ResourceClaim, countLive *int) fakeCluster {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, quotav1alpha1.AddToScheme(scheme))
	objects := []client.Object{bucket, grant}
	for _, c := range claims {
		objects = append(objects, c)
	}
	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&quotav1alpha1.AllowanceBucket{}, &quotav1alpha1.ResourceClaim{})
	for _, field := range []string{"spec.consumerRef.kind", "spec.consumerRef.name"} {
		field := field
		builder = builder.WithIndex(&quotav1alpha1.ResourceGrant{}, field, func(obj client.Object) []string {
			ref := obj.(*quotav1alpha1.ResourceGrant).Spec.ConsumerRef
			return []string{map[string]string{"spec.consumerRef.kind": ref.Kind, "spec.consumerRef.name": ref.Name}[field]}
		}).WithIndex(&quotav1alpha1.ResourceClaim{}, field, func(obj client.Object) []string {
			ref := obj.(*quotav1alpha1.ResourceClaim).Spec.ConsumerRef
			return []string{map[string]string{"spec.consumerRef.kind": ref.Kind, "spec.consumerRef.name": ref.Name}[field]}
		})
	}
	store := builder.WithIndex(&quotav1alpha1.ResourceClaim{}, resourceClaimConsumerRefIndex, func(obj client.Object) []string {
		return []string{consumerRefKey(obj.(*quotav1alpha1.ResourceClaim).Spec.ConsumerRef)}
	}).Build()
	live := interceptor.NewClient(store, interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*quotav1alpha1.ResourceClaimList); ok {
				*countLive++
			}
			return c.List(ctx, list, opts...)
		},
	})
	return fakeCluster{client: store, reader: live}
}

// cacheHolding makes the cluster's cache return these claims, as an informer that lags the store does; with none, the
// store answers. Claim status patches go to the store, so a later live read sees them. It returns the decision each
// claim gets from a reconcile, by claim name.
func cacheHolding(c *fakeCluster, claims ...*quotav1alpha1.ResourceClaim) map[string]string {
	store := c.client.(client.WithWatch)
	decisions := map[string]string{}
	funcs := interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, cl client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			claim := obj.(*quotav1alpha1.ResourceClaim)
			decisions[claim.Name] = claim.Status.Allocations[0].Status
			var current quotav1alpha1.ResourceClaim
			if err := store.Get(ctx, client.ObjectKeyFromObject(claim), &current); err != nil {
				return err
			}
			current.Status.Allocations = claim.Status.Allocations
			return store.Status().Update(ctx, &current)
		},
	}
	if claims != nil {
		funcs.List = func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if cached, ok := list.(*quotav1alpha1.ResourceClaimList); ok {
				cached.Items = nil
				for _, claim := range claims {
					cached.Items = append(cached.Items, *claim.DeepCopy())
				}
				return nil
			}
			return cl.List(ctx, list, opts...)
		}
	}
	c.client = interceptor.NewClient(store, funcs)
	return decisions
}

// testClaim returns p1's claim of amount units of example/count; a status records that decision.
func testClaim(name string, amount int64, status string) *quotav1alpha1.ResourceClaim {
	claim := &quotav1alpha1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: quotav1alpha1.ResourceClaimSpec{ConsumerRef: quotav1alpha1.ConsumerRef{Kind: "Project", Name: "p1"}, Requests: []quotav1alpha1.ResourceRequest{{ResourceType: "example/count", Amount: amount}}}}
	if status == quotav1alpha1.ResourceClaimAllocationStatusGranted {
		claim.Status.Allocations = []quotav1alpha1.ResourceClaimAllocationStatus{{ResourceType: "example/count", Status: status, AllocatedAmount: amount, AllocatingBucket: "bucket"}}
	} else if status != "" {
		claim.Status.Allocations = []quotav1alpha1.ResourceClaimAllocationStatus{{ResourceType: "example/count", Status: status}}
	}
	return claim
}

// reconcileWithCache reconciles p1's example/count bucket, whose grant allows limit and whose live status records
// allocated, over a store holding live and a cache holding cached (nil: the store answers). It returns the decisions,
// the stored bucket and the number of live claim LISTs.
func reconcileWithCache(t *testing.T, limit, allocated int64, live, cached []*quotav1alpha1.ResourceClaim) (map[string]string, *quotav1alpha1.AllowanceBucket, int) {
	t.Helper()
	consumer := quotav1alpha1.ConsumerRef{Kind: "Project", Name: "p1"}
	bucket := &quotav1alpha1.AllowanceBucket{ObjectMeta: metav1.ObjectMeta{Name: "bucket", Namespace: "milo-system"}, Spec: quotav1alpha1.AllowanceBucketSpec{ConsumerRef: consumer, ResourceType: "example/count"}, Status: quotav1alpha1.AllowanceBucketStatus{Limit: limit, Allocated: allocated}}
	grant := &quotav1alpha1.ResourceGrant{ObjectMeta: metav1.ObjectMeta{Name: "grant"}, Spec: quotav1alpha1.ResourceGrantSpec{ConsumerRef: consumer, Allowances: []quotav1alpha1.Allowance{{ResourceType: "example/count", Buckets: []quotav1alpha1.Bucket{{Amount: limit}}}}}, Status: quotav1alpha1.ResourceGrantStatus{Conditions: []metav1.Condition{{Type: quotav1alpha1.ResourceGrantActive, Status: metav1.ConditionTrue}}}}
	liveLists := 0
	c := claimTestCluster(t, bucket, grant, live, &liveLists)
	decisions := cacheHolding(&c, cached...)
	r := &AllowanceBucketController{Scheme: c.client.Scheme(), Manager: fakeManager{cluster: c}}
	_, err := r.Reconcile(context.Background(), mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bucket)}})
	require.NoError(t, err)
	require.NoError(t, c.reader.Get(context.Background(), client.ObjectKeyFromObject(bucket), bucket))
	return decisions, bucket, liveLists
}

// A live LIST of every claim of the consumer on every reconcile costs the API server the plane's whole claim set
// (milo#949's cost at 2,159 claims). When the cache agrees with the live bucket's allocation, it is current enough.
func TestAllowanceBucketReadsAgreeingClaimsFromTheCache(t *testing.T) {
	consumer := quotav1alpha1.ConsumerRef{Kind: "Project", Name: "p1"}
	resourceType := "example/count"
	bucket := &quotav1alpha1.AllowanceBucket{ObjectMeta: metav1.ObjectMeta{Name: "bucket", Namespace: "milo-system"}, Spec: quotav1alpha1.AllowanceBucketSpec{ConsumerRef: consumer, ResourceType: resourceType}, Status: quotav1alpha1.AllowanceBucketStatus{Limit: 2, Allocated: 1}}
	grant := &quotav1alpha1.ResourceGrant{ObjectMeta: metav1.ObjectMeta{Name: "grant"}, Spec: quotav1alpha1.ResourceGrantSpec{ConsumerRef: consumer, Allowances: []quotav1alpha1.Allowance{{ResourceType: resourceType, Buckets: []quotav1alpha1.Bucket{{Amount: 2}}}}}, Status: quotav1alpha1.ResourceGrantStatus{Conditions: []metav1.Condition{{Type: quotav1alpha1.ResourceGrantActive, Status: metav1.ConditionTrue}}}}
	granted := &quotav1alpha1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: "first"}, Spec: quotav1alpha1.ResourceClaimSpec{ConsumerRef: consumer, Requests: []quotav1alpha1.ResourceRequest{{ResourceType: resourceType, Amount: 1}}}, Status: quotav1alpha1.ResourceClaimStatus{Allocations: []quotav1alpha1.ResourceClaimAllocationStatus{{ResourceType: resourceType, Status: quotav1alpha1.ResourceClaimAllocationStatusGranted, AllocatedAmount: 1, AllocatingBucket: "bucket"}}}}
	pending := granted.DeepCopy()
	pending.Name = "second"
	pending.Status = quotav1alpha1.ResourceClaimStatus{}
	liveLists := 0
	c := claimTestCluster(t, bucket, grant, []*quotav1alpha1.ResourceClaim{granted, pending}, &liveLists)
	decisions := cacheHolding(&c, granted, pending)
	r := &AllowanceBucketController{Scheme: c.client.Scheme(), Manager: fakeManager{cluster: c}}
	_, err := r.Reconcile(context.Background(), mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bucket)}})
	require.NoError(t, err)
	assert.Zero(t, liveLists, "the cache agrees with the live bucket, so no live claim LIST is needed")
	assert.Equal(t, map[string]string{"second": quotav1alpha1.ResourceClaimAllocationStatusGranted}, decisions)
	require.NoError(t, c.reader.Get(context.Background(), client.ObjectKeyFromObject(bucket), bucket))
	assert.EqualValues(t, 2, bucket.Status.Allocated)
}

// The live bucket's allocation drops only when a reconcile recomputes it, so after a claim is deleted it still counts
// that claim. A cache that still holds the deleted claim then agrees with the live bucket. A pending claim that fits
// once the deleted claim is gone must be granted, as a live read grants it, and not denied from the cache's view.
func TestAllowanceBucketDoesNotDenyFromADeletedClaimTheCacheHolds(t *testing.T) {
	consumer := quotav1alpha1.ConsumerRef{Kind: "Project", Name: "p1"}
	resourceType := "example/count"
	bucket := &quotav1alpha1.AllowanceBucket{ObjectMeta: metav1.ObjectMeta{Name: "bucket", Namespace: "milo-system"}, Spec: quotav1alpha1.AllowanceBucketSpec{ConsumerRef: consumer, ResourceType: resourceType}, Status: quotav1alpha1.AllowanceBucketStatus{Limit: 2, Allocated: 2}}
	grant := &quotav1alpha1.ResourceGrant{ObjectMeta: metav1.ObjectMeta{Name: "grant"}, Spec: quotav1alpha1.ResourceGrantSpec{ConsumerRef: consumer, Allowances: []quotav1alpha1.Allowance{{ResourceType: resourceType, Buckets: []quotav1alpha1.Bucket{{Amount: 2}}}}}, Status: quotav1alpha1.ResourceGrantStatus{Conditions: []metav1.Condition{{Type: quotav1alpha1.ResourceGrantActive, Status: metav1.ConditionTrue}}}}
	kept := &quotav1alpha1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: "kept"}, Spec: quotav1alpha1.ResourceClaimSpec{ConsumerRef: consumer, Requests: []quotav1alpha1.ResourceRequest{{ResourceType: resourceType, Amount: 1}}}, Status: quotav1alpha1.ResourceClaimStatus{Allocations: []quotav1alpha1.ResourceClaimAllocationStatus{{ResourceType: resourceType, Status: quotav1alpha1.ResourceClaimAllocationStatusGranted, AllocatedAmount: 1, AllocatingBucket: "bucket"}}}}
	deleted := kept.DeepCopy()
	deleted.Name = "deleted"
	pending := kept.DeepCopy()
	pending.Name = "pending"
	pending.Status = quotav1alpha1.ResourceClaimStatus{}
	liveLists := 0
	c := claimTestCluster(t, bucket, grant, []*quotav1alpha1.ResourceClaim{kept, pending}, &liveLists)
	decisions := cacheHolding(&c, kept, deleted, pending)
	r := &AllowanceBucketController{Scheme: c.client.Scheme(), Manager: fakeManager{cluster: c}}
	_, err := r.Reconcile(context.Background(), mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bucket)}})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"pending": quotav1alpha1.ResourceClaimAllocationStatusGranted}, decisions, "the deleted claim no longer takes capacity")
	assert.Equal(t, 1, liveLists, "a denial is decided from the live claims")
	require.NoError(t, c.reader.Get(context.Background(), client.ObjectKeyFromObject(bucket), bucket))
	assert.EqualValues(t, 2, bucket.Status.Allocated)
}

// A cache can lag a decision and a delete at once and still give the live bucket's allocation. A claim it shows as
// pending may have been decided since: it is read live first, so it is never reserved twice, and a denial stays final.
func TestAllowanceBucketReadsAPendingClaimLiveBeforeDecidingIt(t *testing.T) {
	granted, denied := quotav1alpha1.ResourceClaimAllocationStatusGranted, quotav1alpha1.ResourceClaimAllocationStatusDenied
	for name, tc := range map[string]struct {
		limit, allocated int64
		live, cached     []*quotav1alpha1.ResourceClaim
	}{
		"granted since the cache saw it": {3, 2, []*quotav1alpha1.ResourceClaim{testClaim("w", 1, granted), testClaim("x", 1, granted)}, []*quotav1alpha1.ResourceClaim{testClaim("deleted", 1, granted), testClaim("w", 1, granted), testClaim("x", 1, "")}},
		"denied since the cache saw it":  {3, 1, []*quotav1alpha1.ResourceClaim{testClaim("c", 1, granted), testClaim("x", 2, denied)}, []*quotav1alpha1.ResourceClaim{testClaim("deleted", 1, granted), testClaim("x", 2, "")}},
	} {
		t.Run(name, func(t *testing.T) {
			decisions, bucket, _ := reconcileWithCache(t, tc.limit, tc.allocated, tc.live, tc.cached)
			assert.Empty(t, decisions, "x was decided live already")
			assert.EqualValues(t, tc.allocated, bucket.Status.Allocated)
		})
	}
}

// The usual denial: the cache is current, the bucket is full, and a pending claim does not fit. It is denied from
// a live read.
func TestAllowanceBucketDeniesFromALiveReadWhenTheCacheAgrees(t *testing.T) {
	claims := []*quotav1alpha1.ResourceClaim{testClaim("kept", 1, quotav1alpha1.ResourceClaimAllocationStatusGranted), testClaim("pending", 1, "")}
	decisions, bucket, liveLists := reconcileWithCache(t, 1, 1, claims, claims)
	assert.Equal(t, map[string]string{"pending": quotav1alpha1.ResourceClaimAllocationStatusDenied}, decisions)
	assert.Equal(t, 1, liveLists)
	assert.EqualValues(t, 1, bucket.Status.ClaimCount, "a denied claim is not counted")
}

// The cache answers the bucket's consumer's claims only: another project's pending claim is not decided here.
func TestAllowanceBucketDecidesOnlyItsConsumersClaims(t *testing.T) {
	foreign := testClaim("foreign", 1, "")
	foreign.Spec.ConsumerRef.Name = "p2"
	decisions, _, _ := reconcileWithCache(t, 5, 0, []*quotav1alpha1.ResourceClaim{foreign}, nil)
	assert.Empty(t, decisions)
}

// Nothing read from the cache is written: a cache whose total matches the live bucket's allocation, but from claims
// of other sizes, leaves the live claim count as it is.
func TestAllowanceBucketWritesNoClaimCountFromTheCache(t *testing.T) {
	granted := quotav1alpha1.ResourceClaimAllocationStatusGranted
	consumer := quotav1alpha1.ConsumerRef{Kind: "Project", Name: "p1"}
	bucket := &quotav1alpha1.AllowanceBucket{ObjectMeta: metav1.ObjectMeta{Name: "bucket", Namespace: "milo-system"}, Spec: quotav1alpha1.AllowanceBucketSpec{ConsumerRef: consumer, ResourceType: "example/count"}, Status: quotav1alpha1.AllowanceBucketStatus{Limit: 3, Allocated: 2, ClaimCount: 2}}
	grant := &quotav1alpha1.ResourceGrant{ObjectMeta: metav1.ObjectMeta{Name: "grant"}, Spec: quotav1alpha1.ResourceGrantSpec{ConsumerRef: consumer, Allowances: []quotav1alpha1.Allowance{{ResourceType: "example/count", Buckets: []quotav1alpha1.Bucket{{Amount: 3}}}}}, Status: quotav1alpha1.ResourceGrantStatus{Conditions: []metav1.Condition{{Type: quotav1alpha1.ResourceGrantActive, Status: metav1.ConditionTrue}}}}
	liveLists := 0
	c := claimTestCluster(t, bucket, grant, []*quotav1alpha1.ResourceClaim{testClaim("a", 1, granted), testClaim("b", 1, granted)}, &liveLists)
	cacheHolding(&c, testClaim("big", 2, granted))
	r := &AllowanceBucketController{Scheme: c.client.Scheme(), Manager: fakeManager{cluster: c}}
	_, err := r.Reconcile(context.Background(), mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bucket)}})
	require.NoError(t, err)
	require.NoError(t, c.reader.Get(context.Background(), client.ObjectKeyFromObject(bucket), bucket))
	assert.EqualValues(t, 2, bucket.Status.ClaimCount)
	assert.Zero(t, liveLists)
}

// The cache's total tells when the live bucket's allocation is stale: after a delete, a current cache's total is
// below it, and the allocation is recomputed from the live claims.
func TestAllowanceBucketRecomputesAStaleAllocationFromALiveRead(t *testing.T) {
	kept := testClaim("kept", 1, quotav1alpha1.ResourceClaimAllocationStatusGranted)
	_, bucket, liveLists := reconcileWithCache(t, 3, 2, []*quotav1alpha1.ResourceClaim{kept}, []*quotav1alpha1.ResourceClaim{kept})
	assert.EqualValues(t, 1, bucket.Status.Allocated)
	assert.Equal(t, 1, liveLists)
}

// A grant counts its claim at once, as it adds to the allocation: a cache that then agrees with the allocation makes
// no recompute, so a count left for a recompute would stay behind. A claim counts once, whatever its requests.
func TestAllowanceBucketCountsAGrantedClaimOnce(t *testing.T) {
	twice := testClaim("twice", 1, "")
	twice.Spec.Requests = append(twice.Spec.Requests, twice.Spec.Requests[0])
	for name, claim := range map[string]*quotav1alpha1.ResourceClaim{"one request": testClaim("once", 1, ""), "two requests of one type": twice} {
		t.Run(name, func(t *testing.T) {
			_, bucket, _ := reconcileWithCache(t, 5, 0, []*quotav1alpha1.ResourceClaim{claim}, []*quotav1alpha1.ResourceClaim{claim})
			assert.EqualValues(t, 1, bucket.Status.ClaimCount)
		})
	}
}

// Allocations are keyed by resource type, so a claim holds one allocation per type. Its requests of one type are
// decided together, as one amount: two reservations would leave one allocation and leak the other amount.
func TestAllowanceBucketDecidesAClaimsRequestsOfOneTypeTogether(t *testing.T) {
	claim := testClaim("both", 1, "")
	claim.Spec.Requests = append(claim.Spec.Requests, quotav1alpha1.ResourceRequest{ResourceType: "example/count", Amount: 2})
	consumer := claim.Spec.ConsumerRef
	bucket := &quotav1alpha1.AllowanceBucket{ObjectMeta: metav1.ObjectMeta{Name: "bucket", Namespace: "milo-system"}, Spec: quotav1alpha1.AllowanceBucketSpec{ConsumerRef: consumer, ResourceType: "example/count"}, Status: quotav1alpha1.AllowanceBucketStatus{Limit: 5}}
	grant := &quotav1alpha1.ResourceGrant{ObjectMeta: metav1.ObjectMeta{Name: "grant"}, Spec: quotav1alpha1.ResourceGrantSpec{ConsumerRef: consumer, Allowances: []quotav1alpha1.Allowance{{ResourceType: "example/count", Buckets: []quotav1alpha1.Bucket{{Amount: 5}}}}}, Status: quotav1alpha1.ResourceGrantStatus{Conditions: []metav1.Condition{{Type: quotav1alpha1.ResourceGrantActive, Status: metav1.ConditionTrue}}}}
	liveLists := 0
	c := claimTestCluster(t, bucket, grant, []*quotav1alpha1.ResourceClaim{claim}, &liveLists)
	cacheHolding(&c, claim)
	r := &AllowanceBucketController{Scheme: c.client.Scheme(), Manager: fakeManager{cluster: c}}
	_, err := r.Reconcile(context.Background(), mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bucket)}})
	require.NoError(t, err)
	require.NoError(t, c.reader.Get(context.Background(), client.ObjectKeyFromObject(bucket), bucket))
	require.NoError(t, c.reader.Get(context.Background(), client.ObjectKeyFromObject(claim), claim))
	held, _ := grantedAmount(claim, "example/count")
	assert.EqualValues(t, 3, held, "the claim's one allocation holds both requests")
	assert.Equal(t, held, bucket.Status.Allocated, "the bucket reserves what the claim holds, and leaks nothing")
	assert.EqualValues(t, 1, bucket.Status.ClaimCount)
}

// A reservation that no claim holds shows as a cached total below the live allocation once the cache is current, and
// that makes a live recompute. A quiet bucket gets no event to show it, so every reconcile comes back after an interval.
func TestAllowanceBucketComesBackAfterTheRecheckInterval(t *testing.T) {
	claims := []*quotav1alpha1.ResourceClaim{testClaim("kept", 1, quotav1alpha1.ResourceClaimAllocationStatusGranted)}
	consumer := claims[0].Spec.ConsumerRef
	bucket := &quotav1alpha1.AllowanceBucket{ObjectMeta: metav1.ObjectMeta{Name: "bucket", Namespace: "milo-system"}, Spec: quotav1alpha1.AllowanceBucketSpec{ConsumerRef: consumer, ResourceType: "example/count"}, Status: quotav1alpha1.AllowanceBucketStatus{Limit: 3, Allocated: 1}}
	grant := &quotav1alpha1.ResourceGrant{ObjectMeta: metav1.ObjectMeta{Name: "grant"}, Spec: quotav1alpha1.ResourceGrantSpec{ConsumerRef: consumer, Allowances: []quotav1alpha1.Allowance{{ResourceType: "example/count", Buckets: []quotav1alpha1.Bucket{{Amount: 3}}}}}, Status: quotav1alpha1.ResourceGrantStatus{Conditions: []metav1.Condition{{Type: quotav1alpha1.ResourceGrantActive, Status: metav1.ConditionTrue}}}}
	liveLists := 0
	c := claimTestCluster(t, bucket, grant, claims, &liveLists)
	cacheHolding(&c, claims...)
	r := &AllowanceBucketController{Scheme: c.client.Scheme(), Manager: fakeManager{cluster: c}}
	result, err := r.Reconcile(context.Background(), mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bucket)}})
	require.NoError(t, err)
	assert.Equal(t, bucketRecheckInterval, result.RequeueAfter)
	assert.Positive(t, bucketRecheckInterval)
}
