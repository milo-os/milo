package downstreamclient

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Label keys written by [MappedNamespaceResourceStrategy] to downstream
// resources and anchor ConfigMaps. These labels identify the upstream owner of
// a downstream object and enable cross-cluster garbage collection and
// reconciliation triggering.
const (
	// UpstreamOwnerClusterNameLabel records the upstream cluster that owns the
	// downstream resource, encoded as "cluster-<name>" with "/" replaced by "_".
	UpstreamOwnerClusterNameLabel = "meta.datumapis.com/upstream-cluster-name"

	// UpstreamOwnerGroupLabel records the API group of the upstream owner kind.
	UpstreamOwnerGroupLabel = "meta.datumapis.com/upstream-group"

	// UpstreamOwnerKindLabel records the kind name of the upstream owner.
	UpstreamOwnerKindLabel = "meta.datumapis.com/upstream-kind"

	// UpstreamOwnerNameLabel records the name of the upstream owner object.
	UpstreamOwnerNameLabel = "meta.datumapis.com/upstream-name"

	// UpstreamOwnerNamespaceLabel records the namespace of the upstream owner
	// object (or the upstream source namespace for remapped resources).
	UpstreamOwnerNamespaceLabel = "meta.datumapis.com/upstream-namespace"
)

// ResourceStrategy reduces the burden of writing controllers that produce
// downstream resources as artifacts of upstream resources, potentially in
// separate clusters.
//
// Implementations may return a client that writes to the same namespace as the
// upstream resource, remap namespaces to avoid collisions across clusters (see
// [MappedNamespaceResourceStrategy]), or align each source cluster with a
// dedicated target cluster or workspace.
//
// Controllers written against this interface can be tested or redeployed with a
// different placement strategy without changing reconciliation logic.
type ResourceStrategy interface {
	// GetClient returns a client.Client that should be used to read and write
	// downstream resources. The returned client may transparently remap
	// namespaces or perform other transformations.
	GetClient() client.Client

	// ObjectMetaFromUpstreamObject derives the downstream ObjectMeta (Namespace
	// and Name at minimum) that corresponds to the given upstream object.
	ObjectMetaFromUpstreamObject(context.Context, metav1.Object) (metav1.ObjectMeta, error)

	// GetDownstreamNamespaceNameForUpstreamNamespace returns the downstream
	// namespace name that corresponds to the given upstream namespace name.
	GetDownstreamNamespaceNameForUpstreamNamespace(ctx context.Context, name string) (string, error)

	// SetControllerReference establishes a controller ownership relationship
	// between owner and controlled in the downstream cluster, creating any
	// necessary anchor objects.
	SetControllerReference(context.Context, metav1.Object, metav1.Object, ...controllerutil.OwnerReferenceOption) error

	// SetOwnerReference establishes a non-controller ownership relationship
	// between owner and object in the downstream cluster.
	SetOwnerReference(context.Context, metav1.Object, metav1.Object, ...controllerutil.OwnerReferenceOption) error

	// DeleteAnchorForObject removes the anchor object that tracks ownership of
	// the given upstream owner, which triggers garbage collection of dependent
	// downstream resources.
	DeleteAnchorForObject(ctx context.Context, owner client.Object) error
}
