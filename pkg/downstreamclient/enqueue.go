package downstreamclient

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

// TypedEnqueueRequestsForUpstreamOwner returns an event handler that enqueues
// reconcile requests for the upstream owner of a downstream object.
//
// The handler reads the meta.datumapis.com/* labels (see [UpstreamOwnerKindLabel]
// and friends) that [MappedNamespaceResourceStrategy.SetControllerReference]
// writes onto downstream resources. When a downstream object changes, the
// handler maps it back to the upstream owner's cluster, namespace, and name and
// enqueues a [mcreconcile.Request].
//
// ownerType must be a registered scheme object whose Group and Kind match the
// [UpstreamOwnerGroupLabel] and [UpstreamOwnerKindLabel] values written onto
// downstream resources. The scheme is resolved per-cluster at handler
// construction time; a panic is raised if the type cannot be found.
func TypedEnqueueRequestsForUpstreamOwner[object client.Object](ownerType client.Object) mchandler.TypedEventHandlerFunc[object, mcreconcile.Request] {
	return func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[object, mcreconcile.Request] {
		e := &enqueueRequestForOwner[object]{
			ownerType: ownerType,
		}
		if err := e.parseOwnerTypeGroupKind(cl.GetScheme()); err != nil {
			panic(err)
		}

		return e
	}
}

// enqueueRequestForOwner is the internal typed event handler.
type enqueueRequestForOwner[object client.Object] struct {
	ownerType runtime.Object
	groupKind schema.GroupKind
}

// Create implements [handler.TypedEventHandler].
func (e *enqueueRequestForOwner[object]) Create(ctx context.Context, evt event.TypedCreateEvent[object], q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
	reqs := map[mcreconcile.Request]struct{}{}
	e.getOwnerReconcileRequest(evt.Object, reqs)
	for req := range reqs {
		q.Add(req)
	}
}

// Update implements [handler.TypedEventHandler].
func (e *enqueueRequestForOwner[object]) Update(ctx context.Context, evt event.TypedUpdateEvent[object], q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
	reqs := map[mcreconcile.Request]struct{}{}
	e.getOwnerReconcileRequest(evt.ObjectOld, reqs)
	e.getOwnerReconcileRequest(evt.ObjectNew, reqs)
	for req := range reqs {
		q.Add(req)
	}
}

// Delete implements [handler.TypedEventHandler].
func (e *enqueueRequestForOwner[object]) Delete(ctx context.Context, evt event.TypedDeleteEvent[object], q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
	reqs := map[mcreconcile.Request]struct{}{}
	e.getOwnerReconcileRequest(evt.Object, reqs)
	for req := range reqs {
		q.Add(req)
	}
}

// Generic implements [handler.TypedEventHandler].
func (e *enqueueRequestForOwner[object]) Generic(ctx context.Context, evt event.TypedGenericEvent[object], q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
	reqs := map[mcreconcile.Request]struct{}{}
	e.getOwnerReconcileRequest(evt.Object, reqs)
	for req := range reqs {
		q.Add(req)
	}
}

// parseOwnerTypeGroupKind resolves and caches the GroupKind of ownerType using
// the provided scheme. It returns an error if the type is not registered or is
// ambiguous.
func (e *enqueueRequestForOwner[object]) parseOwnerTypeGroupKind(scheme *runtime.Scheme) error {
	kinds, _, err := scheme.ObjectKinds(e.ownerType)
	if err != nil {
		return err
	}
	if len(kinds) != 1 {
		return fmt.Errorf("expected exactly 1 kind for OwnerType %T, but found %s kinds", e.ownerType, kinds)
	}
	e.groupKind = schema.GroupKind{Group: kinds[0].Group, Kind: kinds[0].Kind}
	return nil
}

// getOwnerReconcileRequest inspects the object's labels for upstream owner
// metadata and, when the labels match the expected GroupKind, appends a
// reconcile request to result.
func (e *enqueueRequestForOwner[object]) getOwnerReconcileRequest(obj metav1.Object, result map[mcreconcile.Request]struct{}) {
	labels := obj.GetLabels()
	if labels[UpstreamOwnerKindLabel] != e.groupKind.Kind || labels[UpstreamOwnerGroupLabel] != e.groupKind.Group {
		return
	}

	clusterName := strings.TrimPrefix(
		strings.ReplaceAll(labels[UpstreamOwnerClusterNameLabel], "_", "/"),
		"cluster-",
	)

	result[mcreconcile.Request{
		Request: reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      labels[UpstreamOwnerNameLabel],
				Namespace: labels[UpstreamOwnerNamespaceLabel],
			},
		},
		ClusterName: clusterName,
	}] = struct{}{}
}
