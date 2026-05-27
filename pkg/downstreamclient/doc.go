// Package downstreamclient provides abstractions for writing Kubernetes
// resources to downstream clusters (e.g. Karmada federation targets) as
// artifacts of upstream resources.
//
// The central abstraction is [ResourceStrategy], which decouples controller
// logic from the mechanics of where and how downstream resources are placed.
// A controller can be written as if it is writing resources into the same
// namespace as the upstream resource; the strategy handles any necessary
// namespace or name remapping transparently.
//
// # MappedNamespace strategy
//
// [MappedNamespaceResourceStrategy] implements the ns-<upstream-namespace-uid>
// convention used across the Datum platform. Upstream namespaces are looked up
// by name on the upstream cluster, and the resulting UID drives a stable
// downstream namespace name of the form "ns-<uid>". This prevents name
// collisions when aggregating resources from multiple upstream clusters into a
// single downstream API server.
//
// Ownership is tracked through anchor ConfigMaps that carry the
// meta.datumapis.com/* labels defined in this package. The
// [TypedEnqueueRequestsForUpstreamOwner] handler reads those labels to
// reconcile upstream owners when downstream objects change.
package downstreamclient
