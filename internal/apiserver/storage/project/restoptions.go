package projectstorage

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	generic "k8s.io/apiserver/pkg/registry/generic"

	"go.miloapis.com/milo/internal/apiserver/storage/etcdshared"
	"go.miloapis.com/milo/internal/projectscope"
)

// Option adjusts the project-aware storage.
type Option func(*roGetter)

// WithStorage replaces etcd storage for both project-scoped and shared resources.
func WithStorage(child generic.StorageDecorator) Option {
	return func(g *roGetter) { g.storage = child }
}

// Wrap the upstream RESTOptionsGetter to install a per-project decorator that looks each request's
// Project up in projects, and destroys a gone Project's children after drain.
func WithProjectAwareDecorator(inner generic.RESTOptionsGetter, projects ProjectLookup,
	drain time.Duration, opts ...Option) generic.RESTOptionsGetter {
	return WithProjectAwareDecoratorAndBootstrapper(inner, projects, nil, drain, opts...)
}

// WithProjectAwareDecoratorAndBootstrapper wraps the RESTOptionsGetter with project-aware storage
// and bootstraps project namespaces through the shared bootstrapper.
func WithProjectAwareDecoratorAndBootstrapper(inner generic.RESTOptionsGetter, projects ProjectLookup,
	bootstrapper *NamespaceBootstrapper, drain time.Duration, opts ...Option) generic.RESTOptionsGetter {
	g := roGetter{inner: inner, projects: projects, bootstrapper: bootstrapper, drain: drain}
	for _, o := range opts {
		o(&g)
	}
	return g
}

type roGetter struct {
	inner        generic.RESTOptionsGetter
	projects     ProjectLookup
	bootstrapper *NamespaceBootstrapper
	storage      generic.StorageDecorator // the per-project child's storage; nil is the shared etcd cacher
	drain        time.Duration
}

func (g roGetter) GetRESTOptions(gr schema.GroupResource, example runtime.Object) (generic.RESTOptions, error) {
	opts, err := g.inner.GetRESTOptions(gr, example)
	if err != nil {
		return opts, err
	}
	if projectscope.StaysGlobal(gr) {
		if g.storage != nil {
			opts.Decorator = g.storage
		}
		return opts, nil
	}

	storage := g.storage
	if storage == nil {
		storage = etcdshared.StorageWithSharedCacher()
	}
	opts.Decorator = ProjectAwareDecorator(gr, storage, g.projects, g.bootstrapper, g.drain)
	return opts, nil
}
