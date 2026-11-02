package cockroach

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/client-go/tools/cache"
)

// ProjectFunc maps a storage config's key prefix to the Project that owns its
// objects, or "" for shared resources. An error fails the store's
// construction.
type ProjectFunc func(prefix string) (string, error)

// Decorator returns a storage decorator whose stores keep their objects in b,
// under the project that projectFor picks from each config's prefix. The
// stores keep no cache, so every read goes to the database.
func Decorator(b *Backend, projectFor ProjectFunc) generic.StorageDecorator {
	return func(
		cfg *storagebackend.ConfigForResource,
		resourcePrefix string,
		_ func(obj runtime.Object) (string, error),
		newFunc func() runtime.Object,
		_ func() runtime.Object,
		_ storage.AttrFunc,
		_ storage.IndexerFuncs,
		_ *cache.Indexers,
	) (storage.Interface, factory.DestroyFunc, error) {
		project, err := projectFor(cfg.Prefix)
		if err != nil {
			return nil, nil, err
		}
		s, err := newStore(b, Config{
			Project:        project,
			PathPrefix:     cfg.Prefix,
			ResourcePrefix: resourcePrefix,
			Codec:          cfg.Codec,
			Transformer:    cfg.Transformer,
			NewFunc:        newFunc,
		})
		if err != nil {
			return nil, nil, err
		}
		return s, factory.DestroyFunc(s.destroy), nil
	}
}
