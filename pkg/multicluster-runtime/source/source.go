package source

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"
)

func NewClusterSource[object client.Object, request mcreconcile.ClusterAware[request]](
	cl cluster.Cluster,
	obj object,
	hdler mchandler.TypedEventHandlerFunc[object, request],
	predicates ...predicate.TypedPredicate[object],
) (source.TypedSource[request], error) {
	src := mcsource.TypedKind(
		obj,
		hdler,
		predicates...,
	)

	typedSrc, _, err := src.ForCluster(multicluster.ClusterName(""), cl)
	return typedSrc, err
}

func MustNewClusterSource[object client.Object, request mcreconcile.ClusterAware[request]](
	cl cluster.Cluster,
	obj object,
	hdler mchandler.TypedEventHandlerFunc[object, request],
	predicates ...predicate.TypedPredicate[object],
) source.TypedSource[request] {
	src, err := NewClusterSource(cl, obj, hdler, predicates...)
	if err != nil {
		panic(err)
	}

	return src
}
