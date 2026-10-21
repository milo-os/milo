package etcdshared

import (
	"context"
	"fmt"
	"path"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/kubernetes"
	"go.miloapis.com/milo/internal/apiserver/projectfence"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
)

var rootProjectResource = schema.GroupResource{Group: "resourcemanager.miloapis.com", Resource: "projects"}

// CommitFence belongs to one API server configuration. Root registration comes
// exclusively from the actual root Project storage decorator, never a child.
type CommitFence struct {
	mu                 sync.RWMutex
	root               *rootProjectStore
	invalid            bool
	reservation        *rootReservationStore
	reservationInvalid bool
}
type rootProjectStore struct {
	transport     string
	cluster       uint64
	prefix, probe string
	key           func(runtime.Object) (string, error)
}
type FenceOptions struct {
	Manager *CommitFence
	Root    bool
	Project string
}

func NewCommitFence() *CommitFence { return &CommitFence{} }

func (m *CommitFence) registerRoot(c storagebackend.ConfigForResource, key func(runtime.Object) (string, error), kv clientv3.KV) error {
	probe, err := key(&unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "storage-fence-key-probe"}}})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := kv.Get(ctx, c.Prefix, clientv3.WithCountOnly())
	if err != nil {
		return fmt.Errorf("resolve root Project storage backend: %w", err)
	}
	descriptor := &rootProjectStore{transport: transportKey(c.Transport), cluster: response.Header.ClusterId, prefix: c.Prefix, probe: probe, key: key}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.root != nil && (m.root.transport != descriptor.transport || m.root.cluster != descriptor.cluster || m.root.prefix != descriptor.prefix || m.root.probe != descriptor.probe) {
		m.invalid = true
		return fmt.Errorf("conflicting root Project storage registration")
	}
	if m.invalid {
		return fmt.Errorf("root Project storage registration is invalid")
	}
	m.root = descriptor
	return nil
}
func (m *CommitFence) rootKey(project, transport string) (string, uint64, error) {
	if m == nil {
		return "", 0, fmt.Errorf("root Project storage resolver is missing")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.root == nil || m.invalid || m.root.transport != transport {
		return "", 0, fmt.Errorf("root Project storage backend is unavailable or unsupported")
	}
	key, err := m.root.key(&unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": project}}})
	if err != nil {
		return "", 0, err
	}
	return path.Join("/", m.root.prefix, key), m.root.cluster, nil
}

// fencedStorage examines authoritative decoded old/new native objects. A
// missing request frame is an error for protected mutations, not an opt-out.
type fencedStorage struct {
	storage.Interface
	manager        *CommitFence
	project        string
	config         storagebackend.ConfigForResource
	resourcePrefix string
}
type nativeIdentity struct {
	protected                                bool
	name, namespace, uid, project, app, kind string
}

func nativeObjectIdentity(obj runtime.Object) nativeIdentity {
	u, ok := obj.(runtime.Unstructured)
	if !ok {
		return nativeIdentity{}
	}
	content := u.UnstructuredContent()
	metadataValue, present := content["metadata"]
	if !present {
		// GuaranteedUpdate passes a zero-value object when SSA creates an
		// absent resource. There is no old ownership to protect in that case;
		// the resulting object is checked independently below.
		return nativeIdentity{}
	}
	metadata, ok := metadataValue.(map[string]any)
	if !ok {
		return nativeIdentity{protected: true}
	}
	protected := false
	for _, field := range []string{"labels", "annotations"} {
		raw, present := metadata[field]
		if !present || raw == nil {
			continue
		}
		values, ok := raw.(map[string]any)
		if !ok {
			protected = true
			continue
		}
		for _, key := range []string{"apps.miloapis.com/project-uid", "apps.miloapis.com/app-uid"} {
			if _, present := values[key]; present {
				protected = true
			}
		}
	}
	a, err := meta.Accessor(obj)
	if err != nil {
		return nativeIdentity{protected: true}
	}
	return nativeIdentity{protected: protected, name: a.GetName(), namespace: a.GetNamespace(), uid: string(a.GetUID()), project: a.GetLabels()["apps.miloapis.com/project-uid"], app: a.GetLabels()["apps.miloapis.com/app-uid"], kind: u.GetObjectKind().GroupVersionKind().Kind}
}
func fenceError(resource schema.GroupResource, name string) error {
	return apierrors.NewConflict(resource, name, fmt.Errorf("native mutation has no valid root Project commit fence; retry with fresh authorization"))
}
func (s *fencedStorage) bind(ctx context.Context, key string, identity nativeIdentity) error {
	if !identity.protected {
		return nil
	}
	proof, ok := projectfence.Read(ctx)
	if !ok || s.project == "" || proof.ProjectName != s.project || proof.Resource != s.config.GroupResource || proof.Name != identity.name || proof.Namespace != identity.namespace || proof.UID == "" || proof.UID != identity.uid || proof.Kind != identity.kind || proof.ProjectUID != identity.project || proof.AppUID != identity.app || proof.AppUID == "" {
		return fenceError(s.config.GroupResource, identity.name)
	}
	if _, _, err := s.manager.rootKey(proof.ProjectName, transportKey(s.config.Transport)); err != nil {
		return fenceError(s.config.GroupResource, identity.name)
	}
	prepared, err := storage.PrepareKey(s.resourcePrefix, key, false)
	if err != nil {
		return err
	}
	if !projectfence.Bind(ctx, path.Join("/", s.config.Prefix, prepared)) {
		return fenceError(s.config.GroupResource, identity.name)
	}
	return nil
}
func (s *fencedStorage) Create(ctx context.Context, key string, obj, out runtime.Object, ttl uint64) error {
	if nativeObjectIdentity(obj).protected {
		return fenceError(s.config.GroupResource, nativeObjectIdentity(obj).name)
	} // acquisition remains disabled
	return s.Interface.Create(ctx, key, obj, out, ttl)
}
func (s *fencedStorage) GuaranteedUpdate(ctx context.Context, key string, out runtime.Object, ignore bool, pre *storage.Preconditions, update storage.UpdateFunc, suggestion runtime.Object) error {
	return s.Interface.GuaranteedUpdate(ctx, key, out, ignore, pre, func(old runtime.Object, response storage.ResponseMeta) (runtime.Object, *uint64, error) {
		before := nativeObjectIdentity(old)
		next, ttl, err := update(old, response)
		if err != nil {
			return nil, nil, err
		}
		if err = s.bind(ctx, key, before); err != nil {
			return nil, nil, err
		}
		if err = s.bind(ctx, key, nativeObjectIdentity(next)); err != nil {
			return nil, nil, err
		}
		return next, ttl, nil
	}, suggestion)
}
func (s *fencedStorage) Delete(ctx context.Context, key string, out runtime.Object, pre *storage.Preconditions, validate storage.ValidateObjectFunc, suggestion runtime.Object, options storage.DeleteOptions) error {
	return s.Interface.Delete(ctx, key, out, pre, func(ctx context.Context, old runtime.Object) error {
		if err := validate(ctx, old); err != nil {
			return err
		}
		return s.bind(ctx, key, nativeObjectIdentity(old))
	}, suggestion, options)
}

// fencedClient preserves the upstream object CAS semantics and adds the root
// Project revision to that exact transaction, including implicit finalizer DELETE.
type fencedClient struct {
	kubernetes.Interface
	kv        clientv3.KV
	manager   *CommitFence
	transport string
}

func (c *fencedClient) predicate(ctx context.Context, key string) (projectfence.Proof, string, error) {
	p, target, ok := projectfence.Bound(ctx)
	if !ok {
		return projectfence.Proof{}, "", nil
	}
	if key != target {
		return p, "", fenceError(p.Resource, p.Name)
	}
	root, cluster, err := c.manager.rootKey(p.ProjectName, c.transport)
	if err != nil {
		return p, "", fenceError(p.Resource, p.Name)
	}
	// Fail before writing if endpoint topology resolves to another etcd cluster.
	// This check does not replace the transaction's root revision comparison.
	check, err := c.kv.Get(ctx, root, clientv3.WithCountOnly())
	if err != nil {
		return p, "", err
	}
	if check.Header.ClusterId != cluster {
		return p, "", fenceError(p.Resource, p.Name)
	}
	return p, root, nil
}
func (c *fencedClient) OptimisticPut(ctx context.Context, key string, value []byte, expected int64, options kubernetes.PutOptions) (kubernetes.PutResponse, error) {
	p, root, err := c.predicate(ctx, key)
	if err != nil {
		return kubernetes.PutResponse{}, err
	}
	if root == "" {
		return c.Interface.OptimisticPut(ctx, key, value, expected, options)
	}
	response, err := c.kv.Txn(ctx).If(clientv3.Compare(clientv3.ModRevision(key), "=", expected), clientv3.Compare(clientv3.ModRevision(root), "=", p.ProjectRevision)).Then(clientv3.OpPut(key, string(value), clientv3.WithLease(options.LeaseID))).Else(clientv3.OpGet(key), clientv3.OpGet(root)).Commit()
	if err != nil {
		return kubernetes.PutResponse{}, err
	}
	result := kubernetes.PutResponse{Succeeded: response.Succeeded, Revision: response.Header.Revision}
	if !response.Succeeded {
		if !validRootFailure(response, p.ProjectRevision) {
			return kubernetes.PutResponse{}, fenceError(p.Resource, p.Name)
		}
		if options.GetOnFailure {
			kvs := response.Responses[0].GetResponseRange().Kvs
			if len(kvs) > 0 {
				result.KV = kvs[0]
			}
		}
	}
	return result, nil
}
func (c *fencedClient) OptimisticDelete(ctx context.Context, key string, expected int64, options kubernetes.DeleteOptions) (kubernetes.DeleteResponse, error) {
	p, root, err := c.predicate(ctx, key)
	if err != nil {
		return kubernetes.DeleteResponse{}, err
	}
	if root == "" {
		return c.Interface.OptimisticDelete(ctx, key, expected, options)
	}
	response, err := c.kv.Txn(ctx).If(clientv3.Compare(clientv3.ModRevision(key), "=", expected), clientv3.Compare(clientv3.ModRevision(root), "=", p.ProjectRevision)).Then(clientv3.OpDelete(key)).Else(clientv3.OpGet(key), clientv3.OpGet(root)).Commit()
	if err != nil {
		return kubernetes.DeleteResponse{}, err
	}
	result := kubernetes.DeleteResponse{Succeeded: response.Succeeded, Revision: response.Header.Revision}
	if !response.Succeeded {
		if !validRootFailure(response, p.ProjectRevision) {
			return kubernetes.DeleteResponse{}, fenceError(p.Resource, p.Name)
		}
		if options.GetOnFailure {
			kvs := response.Responses[0].GetResponseRange().Kvs
			if len(kvs) > 0 {
				result.KV = kvs[0]
			}
		}
	}
	return result, nil
}
func validRootFailure(response *clientv3.TxnResponse, revision int64) bool {
	return len(response.Responses) == 2 && response.Responses[1].GetResponseRange() != nil && len(response.Responses[1].GetResponseRange().Kvs) == 1 && response.Responses[1].GetResponseRange().Kvs[0].ModRevision == revision
}

// compile-time guard against accidentally dropping upstream operations.
var _ kubernetes.Interface = (*fencedClient)(nil)
var _ storage.Interface = (*fencedStorage)(nil)
