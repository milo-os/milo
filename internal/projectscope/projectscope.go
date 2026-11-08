// Package projectscope says which kinds Milo stores once for every project
// rather than once per project. The API server's storage wiring and the
// controller that purges a deleted project both read it, so it imports nothing
// from either.
package projectscope

import "k8s.io/apimachinery/pkg/runtime/schema"

// global holds the kinds every project shares. CRD definitions stay global so
// discovery is shared cluster-wide, and APIServices stay global because they
// route every project's requests to the same API servers.
var global = map[schema.GroupResource]struct{}{
	{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}: {},
	{Group: "apiregistration.k8s.io", Resource: "apiservices"}:             {},
}

// StaysGlobal reports whether gr is stored once for every project, so a
// project never owns its objects.
func StaysGlobal(gr schema.GroupResource) bool {
	_, ok := global[gr]
	return ok
}
