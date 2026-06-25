package etcdshared

import (
	"sync"

	"k8s.io/klog/v2"

	"k8s.io/apimachinery/pkg/runtime"
	genericfeatures "k8s.io/apiserver/pkg/features"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	cacherstorage "k8s.io/apiserver/pkg/storage/cacher"
	"k8s.io/apiserver/pkg/storage/etcd3"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/apiserver/pkg/storage/value/encrypt/identity"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/tools/cache"
)

// newRawStorage builds an etcd3 store backed by the transport-keyed shared
// client. It is a copy of the unexported newETCD3Storage in the upstream
// factory package with one change: the client is acquired from the refcounted
// shared cache instead of dialing a fresh connection per (project x resource).
func newRawStorage(c storagebackend.ConfigForResource, newFunc, newListFunc func() runtime.Object, resourcePrefix string) (storage.Interface, factory.DestroyFunc, error) {
	compactor, stopCompactor, err := startCompactorOnce(c.Transport, c.CompactionInterval)
	if err != nil {
		return nil, nil, err
	}

	client, releaseClient, err := acquireClient(c.Transport, c.DBMetricPollInterval)
	if err != nil {
		stopCompactor()
		return nil, nil, err
	}

	transformer := c.Transformer
	if transformer == nil {
		transformer = identity.NewEncryptCheckTransformer()
	}

	versioner := storage.APIObjectVersioner{}
	decoder := etcd3.NewDefaultDecoder(c.Codec, versioner)

	if utilfeature.DefaultFeatureGate.Enabled(genericfeatures.AllowUnsafeMalformedObjectDeletion) {
		transformer = etcd3.WithCorruptObjErrorHandlingTransformer(transformer)
		decoder = etcd3.WithCorruptObjErrorHandlingDecoder(decoder)
	}
	store, err := etcd3.New(client, compactor, c.Codec, newFunc, newListFunc, c.Prefix, resourcePrefix, c.GroupResource, transformer, c.LeaseManagerConfig, decoder, versioner)
	if err != nil {
		stopCompactor()
		releaseClient()
		return nil, nil, err
	}
	var once sync.Once
	destroyFunc := func() {
		once.Do(func() {
			stopCompactor()
			store.Close()
			releaseClient()
		})
	}
	var st storage.Interface = store
	if utilfeature.DefaultFeatureGate.Enabled(genericfeatures.AllowUnsafeMalformedObjectDeletion) {
		st = etcd3.NewStoreWithUnsafeCorruptObjectDeletion(st, c.GroupResource)
	}
	return st, destroyFunc, nil
}

// StorageWithSharedCacher mirrors genericregistry.StorageWithCacher but builds
// the raw store from the transport-keyed shared etcd client. The cacher wrapping
// is identical to upstream and never touches the client.
func StorageWithSharedCacher() generic.StorageDecorator {
	return func(
		storageConfig *storagebackend.ConfigForResource,
		resourcePrefix string,
		keyFunc func(obj runtime.Object) (string, error),
		newFunc func() runtime.Object,
		newListFunc func() runtime.Object,
		getAttrsFunc storage.AttrFunc,
		triggerFuncs storage.IndexerFuncs,
		indexers *cache.Indexers) (storage.Interface, factory.DestroyFunc, error) {

		s, d, err := newRawStorage(*storageConfig, newFunc, newListFunc, resourcePrefix)
		if err != nil {
			return s, d, err
		}
		if klogV := klog.V(5); klogV.Enabled() {
			klogV.InfoS("Storage caching is enabled (shared etcd client)", "type", newFunc())
		}

		cacherConfig := cacherstorage.Config{
			Storage:             s,
			Versioner:           storage.APIObjectVersioner{},
			GroupResource:       storageConfig.GroupResource,
			EventsHistoryWindow: storageConfig.EventsHistoryWindow,
			ResourcePrefix:      resourcePrefix,
			KeyFunc:             keyFunc,
			NewFunc:             newFunc,
			NewListFunc:         newListFunc,
			GetAttrsFunc:        getAttrsFunc,
			IndexerFuncs:        triggerFuncs,
			Indexers:            indexers,
			Codec:               storageConfig.Codec,
		}
		cacher, err := cacherstorage.NewCacherFromConfig(cacherConfig)
		if err != nil {
			return nil, func() {}, err
		}
		delegator := cacherstorage.NewCacheDelegator(cacher, s)
		var once sync.Once
		destroyFunc := func() {
			once.Do(func() {
				delegator.Stop()
				cacher.Stop()
				d()
			})
		}

		return delegator, destroyFunc, nil
	}
}
