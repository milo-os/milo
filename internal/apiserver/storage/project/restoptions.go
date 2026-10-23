package projectstorage

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	generic "k8s.io/apiserver/pkg/registry/generic"

	"go.miloapis.com/milo/internal/apiserver/storage/etcdshared"
)

// Wrap the upstream RESTOptionsGetter to install a per-project decorator that looks each request's
// Project up in projects.
func WithProjectAwareDecorator(inner generic.RESTOptionsGetter, projects ProjectLookup) generic.RESTOptionsGetter {
	return roGetter{inner: inner, projects: projects}
}

// WithProjectAwareDecoratorAndBootstrapper wraps the RESTOptionsGetter with project-aware storage
// and bootstraps project namespaces through the shared bootstrapper.
func WithProjectAwareDecoratorAndBootstrapper(inner generic.RESTOptionsGetter, projects ProjectLookup,
	bootstrapper *NamespaceBootstrapper) generic.RESTOptionsGetter {
	return roGetter{inner: inner, projects: projects, bootstrapper: bootstrapper}
}

type roGetter struct {
	inner        generic.RESTOptionsGetter
	projects     ProjectLookup
	bootstrapper *NamespaceBootstrapper
	storage      generic.StorageDecorator // the per-project child's storage; nil is the shared etcd cacher
}

// NOTE: matches your two-arg signature (GroupResource, runtime.Object).
func (g roGetter) GetRESTOptions(gr schema.GroupResource, example runtime.Object) (generic.RESTOptions, error) {
	opts, err := g.inner.GetRESTOptions(gr, example)
	if err != nil {
		return opts, err
	}
	// 🔒 Leave CRD *definitions* global so discovery is shared cluster-wide
	if gr.Group == "apiextensions.k8s.io" && gr.Resource == "customresourcedefinitions" {
		return opts, nil
	}

	// Ensure we always wrap with our project-aware decorator.
	storage := g.storage
	if storage == nil {
		storage = etcdshared.StorageWithSharedCacher()
	}
	opts.Decorator = ProjectAwareDecorator(gr, storage, g.projects, g.bootstrapper)
	return opts, nil
}
