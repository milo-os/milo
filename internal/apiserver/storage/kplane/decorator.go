// Package kplane wires Milo's apiserver into kplane-dev's shared-storage
// stack. A single per-resource cacher (built via kplane-dev/storage's
// cluster-identity-aware decorator) provides per-project isolation by
// embedding the project name in the storage key.
//
// All actual storage and key-rewriting logic is delegated to
// kplane-dev/apiserver/pkg/multicluster; this file is a thin adapter that
// constructs the right Options for Milo and translates Milo's existing
// request.ProjectID(ctx) carrier to kplane's mc.WithCluster context.
package kplane

import (
	"net/http"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	generic "k8s.io/apiserver/pkg/registry/generic"

	mc "github.com/kplane-dev/apiserver/pkg/multicluster"

	"go.miloapis.com/milo/pkg/request"
)

// ServerName is reported in kplane's storage metrics.
const ServerName = "milo-apiserver"

// Options returns the multicluster options Milo uses when running on kplane
// storage. EtcdPrefix is left empty so kplane uses its default (/registry),
// matching the upstream apiserver default; the underlying storage decorator
// re-reads the actual etcd prefix from each ConfigForResource at runtime.
func Options() mc.Options {
	return mc.Options{
		DefaultCluster: mc.DefaultClusterName, // "root" — used when no project is in context
		ServerName:     ServerName,
	}
}

// WithKplaneStorage swaps the storage decorator on every RESTOptions returned
// by inner with kplane's cluster-identity decorator, leaving CRD definitions
// alone (those stay cluster-global so discovery is shared across projects).
func WithKplaneStorage(inner generic.RESTOptionsGetter) generic.RESTOptionsGetter {
	return kplaneGetter{
		delegate: mc.RESTOptionsDecorator{
			Delegate: inner,
			Options:  Options(),
		},
	}
}

type kplaneGetter struct {
	delegate mc.RESTOptionsDecorator
}

func (g kplaneGetter) GetRESTOptions(gr schema.GroupResource, example runtime.Object) (generic.RESTOptions, error) {
	// CRDs themselves stay global. Falling back to the underlying getter here
	// preserves the existing behavior (a single, project-unaware store for
	// /apis/apiextensions.k8s.io/v1/customresourcedefinitions). CR *instances*
	// flow through the kplane decorator like any other resource.
	if gr.Group == "apiextensions.k8s.io" && gr.Resource == "customresourcedefinitions" {
		return g.delegate.Delegate.GetRESTOptions(gr, example)
	}
	return g.delegate.GetRESTOptions(gr, example)
}

// WithProjectAsCluster bridges Milo's request.ProjectID(ctx) marker into
// kplane's mc.WithCluster(ctx, ...) context so the kplane storage layer can
// extract the cluster ID via its own mc.FromContext / mc.FromContextScope
// helpers.
//
// Install this middleware AFTER pkg/server/filters.ProjectRouterWithRequestInfo
// — which is the call that stashes the project ID via request.WithProject —
// and BEFORE anything that consults storage.
func WithProjectAsCluster(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if proj, ok := request.ProjectID(r.Context()); ok && proj != "" {
			r = r.WithContext(mc.WithCluster(r.Context(), proj, false))
		}
		next.ServeHTTP(w, r)
	})
}
