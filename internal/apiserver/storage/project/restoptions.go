package projectstorage

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	generic "k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
)

// WithProjectAwareDecorator wraps the upstream RESTOptionsGetter to install
// a shared-storage decorator that isolates tenants by rewriting etcd keys to
// embed /clusters/<projID>/ from the request context.
func WithProjectAwareDecorator(inner generic.RESTOptionsGetter) generic.RESTOptionsGetter {
	return roGetter{inner: inner}
}

type roGetter struct {
	inner generic.RESTOptionsGetter
}

func (g roGetter) GetRESTOptions(gr schema.GroupResource, example runtime.Object) (generic.RESTOptions, error) {
	opts, err := g.inner.GetRESTOptions(gr, example)
	if err != nil {
		return opts, err
	}
	// Leave CRD *definitions* global so discovery is shared cluster-wide.
	if gr.Group == "apiextensions.k8s.io" && gr.Resource == "customresourcedefinitions" {
		return opts, nil
	}
	if opts.Decorator == nil {
		opts.Decorator = ProjectAwareDecorator(gr, genericregistry.StorageWithCacher())
	} else {
		opts.Decorator = ProjectAwareDecorator(gr, opts.Decorator)
	}
	return opts, nil
}
