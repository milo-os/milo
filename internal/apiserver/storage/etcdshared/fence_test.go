package etcdshared

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/projectfence"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
)

func fenceObject() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "test.example/v1", "kind": "Thing", "metadata": map[string]any{"name": "one", "namespace": "default", "uid": "object-uid", "labels": map[string]any{"apps.miloapis.com/project-uid": "project-uid", "apps.miloapis.com/app-uid": "app-uid"}}}}
}
func TestOwnershipEnrollment(t *testing.T) {
	require.False(t, nativeObjectIdentity(&unstructured.Unstructured{}).protected, "absent old SSA object has no ownership")
	for _, field := range []string{"labels", "annotations"} {
		for _, key := range []string{"apps.miloapis.com/project-uid", "apps.miloapis.com/app-uid"} {
			for _, value := range []any{"", nil, 123, "uid"} {
				obj := fenceObject()
				obj.Object["metadata"].(map[string]any)["labels"] = map[string]any{}
				obj.Object["metadata"].(map[string]any)[field] = map[string]any{key: value}
				require.True(t, nativeObjectIdentity(obj).protected, "reserved marker presence enrolls even malformed values")
			}
		}
	}
	for _, metadata := range []any{nil, "bad", []any{}} {
		require.True(t, nativeObjectIdentity(&unstructured.Unstructured{Object: map[string]any{"metadata": metadata}}).protected)
	}
}

func proofFor(obj *unstructured.Unstructured) projectfence.Proof {
	return projectfence.Proof{ProjectName: "project-a", ProjectUID: "project-uid", AppUID: "app-uid", Resource: schema.GroupResource{Group: "test.example", Resource: "things"}, Kind: "Thing", Name: obj.GetName(), Namespace: obj.GetNamespace(), UID: string(obj.GetUID())}
}
func testFencedStorage() *fencedStorage {
	c := storagebackend.ConfigForResource{Config: storagebackend.Config{Prefix: "/projects/project-a"}, GroupResource: schema.GroupResource{Group: "test.example", Resource: "things"}}
	m := NewCommitFence()
	m.root = &rootProjectStore{transport: transportKey(c.Transport), cluster: 42, prefix: "/root", key: func(obj runtime.Object) (string, error) {
		return "/projects/" + obj.(*unstructured.Unstructured).GetName(), nil
	}}
	return &fencedStorage{manager: m, project: "project-a", config: c, resourcePrefix: "/things"}
}
func TestCommitProofBindsActualStoreAndIdentity(t *testing.T) {
	obj := fenceObject()
	for _, name := range []string{"valid", "no-frame", "no-proof", "wrong-project", "root-store", "wrong-app", "wrong-uid", "wrong-kind", "wrong-resource", "no-root", "invalid-root", "different-backend"} {
		t.Run(name, func(t *testing.T) {
			s := testFencedStorage()
			ctx := projectfence.WithContext(context.Background())
			p := proofFor(obj)
			switch name {
			case "no-frame":
				ctx = context.Background()
			case "wrong-project":
				s.project = "other"
			case "root-store":
				s.project = ""
			case "wrong-app":
				p.AppUID = "other"
			case "wrong-uid":
				p.UID = "replacement"
			case "wrong-kind":
				p.Kind = "Other"
			case "wrong-resource":
				p.Resource.Resource = "other"
			case "no-root":
				s.manager.root = nil
			case "invalid-root":
				s.manager.invalid = true
			case "different-backend":
				s.manager.root.transport = "other"
			}
			if name != "no-proof" && name != "no-frame" {
				require.True(t, projectfence.Record(ctx, p, "7"))
			}
			err := s.bind(ctx, "/things/default/one", nativeObjectIdentity(obj))
			if name == "valid" {
				require.NoError(t, err)
				_, key, ok := projectfence.Bound(ctx)
				require.True(t, ok)
				require.NotEmpty(t, key)
			} else {
				require.True(t, apierrors.IsConflict(err), "%v", err)
			}
		})
	}
}

type updateFixture struct {
	storage.Interface
	old    runtime.Object
	called bool
}

func (f *updateFixture) GuaranteedUpdate(ctx context.Context, key string, out runtime.Object, ignore bool, pre *storage.Preconditions, fn storage.UpdateFunc, suggestion runtime.Object) error {
	f.called = true
	_, _, err := fn(f.old, storage.ResponseMeta{})
	return err
}
func TestMissingProofCannotSkipOldOwnership(t *testing.T) {
	old := fenceObject()
	next := old.DeepCopy()
	next.SetLabels(nil)
	inner := &updateFixture{old: old}
	s := testFencedStorage()
	s.Interface = inner
	err := s.GuaranteedUpdate(context.Background(), "/things/default/one", next, false, nil, func(runtime.Object, storage.ResponseMeta) (runtime.Object, *uint64, error) { return next, nil, nil }, nil)
	require.True(t, apierrors.IsConflict(err))
	require.True(t, inner.called)
	// Ordinary create-on-update remains possible when both versions are unmarked.
	inner.old = &unstructured.Unstructured{}
	err = s.GuaranteedUpdate(context.Background(), "/things/default/one", next, false, nil, func(runtime.Object, storage.ResponseMeta) (runtime.Object, *uint64, error) { return next, nil, nil }, nil)
	require.NoError(t, err)
}
