package projectstorage

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	generic "k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	storagebackend "k8s.io/apiserver/pkg/storage/storagebackend"
	factory "k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

// ProjectAwareDecorator builds a single shared storage per resource and
// configures the storage stack to be tenant-aware via an off-object side
// channel scoped to the cacher's lifetime:
//
//   - Transformer is wrapped to expose the etcd key (carried via dataCtx) to
//     the codec by prepending a small header to the bytes flowing up from etcd.
//   - Codec is wrapped to parse the header and record (object UID → tenant)
//     in the per-cacher tenantMap. Nothing is written onto the object itself.
//   - The cacher's keyFunc is wrapped to look up tenant by UID and produce
//     in-memory btree keys that include the tenant segment, aligning the
//     watchCache's indexing with the tenant-prefixed etcd keys.
//   - A per-cacher deletion tracker subscribes to the cacher's own Watch,
//     resumes from Bookmark-supplied RVs across reconnects, and periodically
//     reconciles tenantMap against the cacher's live set. Lifecycle is bound
//     to the storage's DestroyFunc, so destroying the cacher releases all
//     tenantMap entries en masse.
//
// The outer storage.Interface wrapper (projectKeyRewriter) rewrites incoming
// keys to include the tenant prefix based on request.ProjectID(ctx) and
// validates pagination continue tokens against the requester's tenant subtree.
func ProjectAwareDecorator(gr schema.GroupResource, inner generic.StorageDecorator) generic.StorageDecorator {
	return func(
		cfg *storagebackend.ConfigForResource,
		resourcePrefix string,
		keyFunc func(obj runtime.Object) (string, error),
		newFunc func() runtime.Object,
		newListFunc func() runtime.Object,
		getAttrs storage.AttrFunc,
		triggerFn storage.IndexerFuncs,
		indexers *cache.Indexers,
	) (storage.Interface, factory.DestroyFunc, error) {
		tm := &tenantMap{}

		cfg.Transformer = &tenantTransformer{inner: cfg.Transformer}
		cfg.Codec = &tenantCodec{Codec: cfg.Codec, tm: tm}

		s, destroy, err := inner(cfg, resourcePrefix, tenantAwareKeyFunc(resourcePrefix, keyFunc, tm, gr), newFunc, newListFunc, getAttrs, triggerFn, indexers)
		if err != nil {
			return nil, nil, err
		}

		stopTracker := startDeletionTracker(s, resourcePrefix, gr, tm, newListFunc)
		composedDestroy := func() {
			stopTracker()
			destroy()
		}

		return &projectKeyRewriter{
			inner:          s,
			resourcePrefix: resourcePrefix,
			groupResource:  gr,
		}, composedDestroy, nil
	}
}
