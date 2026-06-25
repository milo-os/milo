package projectstorage

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	generic "k8s.io/apiserver/pkg/registry/generic"

	"go.miloapis.com/milo/internal/apiserver/storage/etcdshared"
)

// Wrap the upstream RESTOptionsGetter to install a per-project decorator.
func WithProjectAwareDecorator(inner generic.RESTOptionsGetter) generic.RESTOptionsGetter {
	return roGetter{inner: inner, loopbackConfig: nil}
}

// WithProjectAwareDecoratorAndConfig wraps the RESTOptionsGetter with project-aware storage
// and provides a loopback config for bootstrapping project namespaces.
func WithProjectAwareDecoratorAndConfig(inner generic.RESTOptionsGetter, loopbackConfig *rest.Config) generic.RESTOptionsGetter {
	return roGetter{inner: inner, loopbackConfig: loopbackConfig}
}

type roGetter struct {
	inner          generic.RESTOptionsGetter
	loopbackConfig *rest.Config
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
	if opts.Decorator == nil {
		opts.Decorator = ProjectAwareDecorator(gr, etcdshared.StorageWithSharedCacher(), g.loopbackConfig)
	} else {
		opts.Decorator = ProjectAwareDecorator(gr, opts.Decorator, g.loopbackConfig)
	}
	return opts, nil
}
