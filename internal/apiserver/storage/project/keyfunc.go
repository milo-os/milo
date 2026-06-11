package projectstorage

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type keyFunc func(runtime.Object) (string, error)

// tenantAwareKeyFunc wraps the upstream-provided keyFunc so the cacher's
// in-memory btree keys match the etcd keys produced by projectKeyRewriter:
// "/clusters/<tenant>/" for project-scoped objects, "/root/" otherwise.
// Tenant lookup is by object UID against the per-cacher tenantMap, populated
// by tenantCodec.Decode using the etcd key.
func tenantAwareKeyFunc(resourcePrefix string, originalKeyFunc keyFunc, tm *tenantMap, gr schema.GroupResource) keyFunc {
	return func(obj runtime.Object) (string, error) {
		baseKey, err := originalKeyFunc(obj)
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(baseKey, resourcePrefix) {
			return baseKey, nil
		}
		suffix := baseKey[len(resourcePrefix):]

		var (
			entry tenantEntry
			found bool
		)
		if accessor, err := meta.Accessor(obj); err == nil {
			entry, found = tm.lookup(accessor.GetUID())
		}

		// Double check the resulting cache key matches the storage key recorded at decode time.
		// If it ever drifts, refuse the cache insert and surfaces the divergence as a
		// request failure rather than letting it corrupt isolation silently.
		var predicted string
		if entry.tenant != "" {
			predicted = resourcePrefix + tenantSegment + entry.tenant + suffix
		} else {
			predicted = resourcePrefix + rootSegment + suffix
		}

		if found && entry.storageKey != predicted {
			return "", fmt.Errorf(
				"tenant-storage: cache key diverged from storage key (group=%q resource=%q predicted=%q etcd=%q)",
				gr.Group, gr.Resource, predicted, entry.storageKey,
			)
		}

		return predicted, nil
	}
}
