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
}

func (c fakeCluster) GetClient() client.Client { return c.client }

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
