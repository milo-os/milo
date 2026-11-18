package projectstorage

import (
	"time"

	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	generic "k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	storagebackend "k8s.io/apiserver/pkg/storage/storagebackend"
	factory "k8s.io/apiserver/pkg/storage/storagebackend/factory"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"
)

// ProjectAwareDecorator builds per-project storage isolation using etcd prefix separation.
// Every request into a project's control plane looks its Project up in projects. A child whose
// Project leaves the view, or that no request and no watch used for idle, is destroyed after drain;
// an idle of 0 keeps a child until its Project goes. When bootstrapper is non-nil, it ensures the
// milo-system namespace exists in project control planes.
func ProjectAwareDecorator(gr schema.GroupResource, inner generic.StorageDecorator, projects ProjectLookup,
	bootstrapper *NamespaceBootstrapper, drain, idle time.Duration) generic.StorageDecorator {
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

		// Build default child (no project in ctx).
		defS, defDestroy, err := inner(cfg, resourcePrefix, keyFunc, newFunc, newListFunc, getAttrs, triggerFn, indexers)
		if err != nil {
			return nil, nil, err
		}

		clk := clock.RealClock{}
		mux := &projectMux{
			inner:        inner,
			cfg:          *cfg, // copy
			projects:     projects,
			bootstrapper: bootstrapper,
			refusalLog:   rate.NewLimiter(rate.Every(refusalLogInterval), 1),
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
				"": {s: defS, destroy: defDestroy, clock: clk},
			},
			versioner:   defS.Versioner(),
			drain:       drain,
			idleTimeout: idle,
			clock:       clk,
		}
		mux.removeListener = projects.AddListener(mux)
		registerMux(gr, mux)
		mux.startIdleSweep()
		return mux, mux.destroyAll, nil
	}
}
