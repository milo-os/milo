package projectstorage

import (
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// tenantMap is a mapping of object UID to tenantEntry for one cacher's
// lifetime.
//
// Populated by tenantCodec.Decode whenever an object is decoded from storage.
// Bounded by the cacher's live set (plus brief reconcile lag), not by total
// objects ever created.
type tenantMap struct{ m sync.Map }

// tenantEntry is the recorded side-channel state for a cached object.
// Entries carry a recordedAt timestamp so the periodic reconciler can skip
// in-flight items.
type tenantEntry struct {
	tenant     string
	storageKey string
	recordedAt time.Time
}

func (t *tenantMap) record(uid types.UID, storageKey string) {
	if uid == "" {
		return
	}
	t.m.Store(uid, tenantEntry{
		tenant:     tenantFromStorageKey(storageKey),
		storageKey: storageKey,
		recordedAt: time.Now(),
	})
}

func (t *tenantMap) lookup(uid types.UID) (tenantEntry, bool) {
	if uid == "" {
		return tenantEntry{}, false
	}
	if v, ok := t.m.Load(uid); ok {
		return v.(tenantEntry), true
	}
	return tenantEntry{}, false
}

func (t *tenantMap) forget(uid types.UID) {
	if uid == "" {
		return
	}
	t.m.Delete(uid)
}

func (t *tenantMap) retainPredicate(pred func(uid types.UID, entry tenantEntry) bool) {
	t.m.Range(func(k, v any) bool {
		uid := k.(types.UID)
		entry := v.(tenantEntry)
		if !pred(uid, entry) {
			t.m.Delete(uid)
		}
		return true
	})
}
