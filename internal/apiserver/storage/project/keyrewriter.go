package projectstorage

import (
	"context"
	"fmt"
	"strings"

	"go.miloapis.com/milo/pkg/request"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

// projectKeyRewriter wraps a single shared storage.Interface and rewrites the
// storage key on every operation to inject a tenant segment after the resource
// prefix. Project-scoped requests get "/clusters/<projID>/"; everything else
// gets "/root/". Both root and tenant data live under disjoint prefixes so the
// cacher's btree prefix scans (List/Watch) cannot return cross-tenant items.
//
// Tenant identity does not appear on objects and is expected to be stored
// separately.
type projectKeyRewriter struct {
	inner          storage.Interface
	resourcePrefix string
	groupResource  schema.GroupResource
}

func (r *projectKeyRewriter) rewrite(ctx context.Context, key string) string {
	if !strings.HasPrefix(key, r.resourcePrefix) {
		return key
	}
	tenantID, _ := request.ProjectID(ctx)
	return namespacedKeyForPrefix(key, r.resourcePrefix, tenantID)
}

// validateContinue ensures a paginated list's continue token belongs within
// the requester's scope.
func (r *projectKeyRewriter) validateContinue(ctx context.Context, continueToken string) error {
	if continueToken == "" {
		return nil
	}
	fromKey, _, err := storage.DecodeContinue(continueToken, r.resourcePrefix)
	if err != nil {
		return apierrors.NewBadRequest(fmt.Sprintf("invalid continue token: %v", err))
	}

	tenantID, _ := request.ProjectID(ctx)
	if !strings.HasPrefix(fromKey, scopePrefix(r.resourcePrefix, tenantID)) {
		recordContinueTokenRejection(r.groupResource)
		return apierrors.NewBadRequest("continue token does not belong to the current scope")
	}
	return nil
}

func (r *projectKeyRewriter) Versioner() storage.Versioner { return r.inner.Versioner() }

func (r *projectKeyRewriter) Create(ctx context.Context, key string, obj, out runtime.Object, ttl uint64) error {
	return r.inner.Create(ctx, r.rewrite(ctx, key), obj, out, ttl)
}

func (r *projectKeyRewriter) Delete(ctx context.Context, key string, out runtime.Object,
	precond *storage.Preconditions, validateDeletion storage.ValidateObjectFunc,
	cachedExistingObject runtime.Object, opts storage.DeleteOptions) error {
	return r.inner.Delete(ctx, r.rewrite(ctx, key), out, precond, validateDeletion, cachedExistingObject, opts)
}

func (r *projectKeyRewriter) Watch(ctx context.Context, key string, opts storage.ListOptions) (watch.Interface, error) {
	return r.inner.Watch(ctx, r.rewrite(ctx, key), opts)
}

func (r *projectKeyRewriter) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	return r.inner.Get(ctx, r.rewrite(ctx, key), opts, objPtr)
}

func (r *projectKeyRewriter) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	if err := r.validateContinue(ctx, opts.Predicate.Continue); err != nil {
		return err
	}
	return r.inner.GetList(ctx, r.rewrite(ctx, key), opts, listObj)
}

func (r *projectKeyRewriter) GuaranteedUpdate(ctx context.Context, key string, out runtime.Object,
	ignoreNotFound bool, precond *storage.Preconditions, tryUpdate storage.UpdateFunc,
	suggestion runtime.Object) error {
	return r.inner.GuaranteedUpdate(ctx, r.rewrite(ctx, key), out, ignoreNotFound, precond, tryUpdate, suggestion)
}

func (r *projectKeyRewriter) RequestWatchProgress(ctx context.Context) error {
	return r.inner.RequestWatchProgress(ctx)
}
func (r *projectKeyRewriter) Stats(ctx context.Context) (storage.Stats, error) {
	return r.inner.Stats(ctx)
}
func (r *projectKeyRewriter) GetCurrentResourceVersion(ctx context.Context) (uint64, error) {
	return r.inner.GetCurrentResourceVersion(ctx)
}
func (r *projectKeyRewriter) EnableResourceSizeEstimation(fn storage.KeysFunc) error {
	return r.inner.EnableResourceSizeEstimation(fn)
}

func (r *projectKeyRewriter) ReadinessCheck() error  { return r.inner.ReadinessCheck() }
func (r *projectKeyRewriter) CompactRevision() int64 { return r.inner.CompactRevision() }
