package projectstorage

import (
	"go.miloapis.com/milo/internal/apiserver/storage/etcdshared"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	generic "k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	storagebackend "k8s.io/apiserver/pkg/storage/storagebackend"
	factory "k8s.io/apiserver/pkg/storage/storagebackend/factory"

	"k8s.io/client-go/tools/cache"
)

// ProjectAwareDecorator builds per-project storage isolation using etcd prefix separation.
// When bootstrapper is non-nil, it ensures the milo-system namespace exists in project control planes.
func ProjectAwareDecorator(gr schema.GroupResource, inner generic.StorageDecorator, bootstrapper *NamespaceBootstrapper, fences ...*etcdshared.CommitFence) generic.StorageDecorator {
	if gr.Group == "" && gr.Resource == "namespaces" {
		bootstrapper = nil
	}
	return func(
		cfg *storagebackend.ConfigForResource,
		resourcePrefix string,
		keyFunc func(obj runtime.Object) (string, error),
		newFunc func() runtime.Object,
		newListFunc func() runtime.Object,
		getAttrs storage.AttrFunc,
		triggerFn storage.IndexerFuncs, // <— changed type
		indexers *cache.Indexers, // <— from client-go/tools/cache
	) (storage.Interface, factory.DestroyFunc, error) {

		// The root registration path is explicit: project children cannot replace it.
		rootInner := inner
		if len(fences) > 0 && fences[0] != nil {
			rootInner = etcdshared.StorageWithSharedCacher(etcdshared.FenceOptions{Manager: fences[0], Root: true})
		}
		// Build default child (no project in ctx).
		defS, defDestroy, err := rootInner(cfg, resourcePrefix, keyFunc, newFunc, newListFunc, getAttrs, triggerFn, indexers)
		if err != nil {
			return nil, nil, err
		}

		mux := &projectMux{
			inner:        inner,
			cfg:          *cfg, // copy
			bootstrapper: bootstrapper,
			args: decoratorArgs{
				resourceGroup:  gr.Group,    // "" means core
				resourceKind:   gr.Resource, // plural
				resourcePrefix: resourcePrefix,

				keyFunc:     keyFunc,
				newFunc:     newFunc,
				newListFunc: newListFunc,
				getAttrs:    getAttrs,
				triggerFn:   triggerFn,
				indexers:    indexers,
			},
			children: map[string]*child{
				"": {s: defS, destroy: defDestroy},
			},
			versioner: defS.Versioner(),
		}
		if len(fences) > 0 {
			mux.commitFence = fences[0]
		}
		return mux, mux.destroyAll, nil
	}
}
