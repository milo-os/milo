package cockroach

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/etcd3"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/apiserver/pkg/storage/value/encrypt/identity"
)

// store is one resource's view of a project's objects.
type store struct {
	b              *Backend
	project        string
	pathPrefix     string
	resourcePrefix string
	codec          runtime.Codec
	versioner      storage.Versioner
	decoder        etcd3.Decoder
	newFunc        func() runtime.Object
	transformer    value.Transformer
	// alive ends when the store is destroyed. A store outlives the request
	// that built it, so it is not derived from one.
	alive   context.Context
	destroy context.CancelFunc
}

var _ storage.Interface = (*store)(nil)

// Config describes one resource's store.
type Config struct {
	// Project scopes every key and owns the resource-version counter. It is
	// empty for shared resources.
	Project string
	// PathPrefix is the etcd key prefix the resource would have used. It is the
	// transformer's authenticated data, so values stay compatible with etcd's.
	PathPrefix     string
	ResourcePrefix string
	Codec          runtime.Codec
	Transformer    value.Transformer
	NewFunc        func() runtime.Object
}

func newStore(b *Backend, c Config) (*store, error) {
	if b == nil || c.Codec == nil || c.NewFunc == nil {
		return nil, errors.New("cockroach storage: a backend, a codec and an object constructor are required")
	}
	if c.ResourcePrefix == "" {
		return nil, errors.New("cockroach storage: resource prefix is required")
	}
	pathPrefix := path.Join("/", c.PathPrefix)
	if !strings.HasSuffix(pathPrefix, "/") {
		pathPrefix += "/"
	}
	transformer := c.Transformer
	if transformer == nil {
		transformer = identity.NewEncryptCheckTransformer()
	}
	versioner := storage.APIObjectVersioner{}
	s := &store{
		b:              b,
		project:        c.Project,
		pathPrefix:     pathPrefix,
		resourcePrefix: c.ResourcePrefix,
		codec:          c.Codec,
		versioner:      versioner,
		decoder:        etcd3.NewDefaultDecoder(c.Codec, versioner),
		newFunc:        c.NewFunc,
		transformer:    transformer,
	}
	s.alive, s.destroy = context.WithCancel(context.Background())
	return s, nil
}

// authData returns the etcd key of key, which is the transformer's
// authenticated data.
func (s *store) authData(key string) value.Context {
	return value.DefaultContext(s.pathPrefix + strings.TrimPrefix(key, "/"))
}

func (s *store) prepareKey(key string, recursive bool) (string, error) {
	return storage.PrepareKey(s.resourcePrefix, key, recursive)
}

func (s *store) Versioner() storage.Versioner { return s.versioner }

// readKey returns key's live row, or nil, and the project's current revision.
func (s *store) readKey(ctx context.Context, key string) (*row, int64, error) {
	start := time.Now()
	var current int64
	var live map[string]*row
	rows, err := s.b.pool.Query(ctx, keysSQL, s.project, []string{"", key})
	if err == nil {
		current, live, err = collectKeys(rows)
	}
	observeOperation("get", start, err)
	return live[key], current, err
}

func (s *store) Get(ctx context.Context, key string, opts storage.GetOptions, out runtime.Object) error {
	preparedKey, err := s.prepareKey(key, false)
	if err != nil {
		return err
	}
	live, current, err := s.readKey(ctx, preparedKey)
	if err != nil {
		return err
	}
	if err := s.validateMinimumResourceVersion(opts.ResourceVersion, uint64(current)); err != nil {
		return err
	}
	if live == nil {
		if opts.IgnoreNotFound {
			return runtime.SetZeroValue(out)
		}
		return storage.NewKeyNotFoundError(preparedKey, 0)
	}
	data, _, err := s.transformer.TransformFromStorage(ctx, live.value, s.authData(preparedKey))
	if err != nil {
		return storage.NewInternalError(err)
	}
	return s.decoder.Decode(data, out, live.rev)
}

func (s *store) GetCurrentResourceVersion(ctx context.Context) (uint64, error) {
	start := time.Now()
	rev, err := currentRevision(ctx, s.b.pool, s.project)
	observeOperation("revision", start, err)
	return uint64(rev), err
}

// GetList isn't supported yet.
func (s *store) GetList(context.Context, string, storage.ListOptions, runtime.Object) error {
	return errors.New("cockroach storage: lists are not supported yet")
}

// Stats isn't supported yet.
func (s *store) Stats(context.Context) (storage.Stats, error) {
	return storage.Stats{}, errors.New("cockroach storage: stats are not supported yet")
}

// EnableResourceSizeEstimation does nothing, because Stats reports object
// counts only.
func (s *store) EnableResourceSizeEstimation(storage.KeysFunc) error { return nil }

func (s *store) ReadinessCheck() error { return nil }

// Watch isn't supported yet.
func (s *store) Watch(context.Context, string, storage.ListOptions) (watch.Interface, error) {
	return nil, errors.New("cockroach storage: watches are not supported yet")
}

// RequestWatchProgress does nothing. Only the watch cache asks for progress,
// and nothing caches these stores.
func (s *store) RequestWatchProgress(context.Context) error { return nil }

// CompactRevision returns 0, since nothing replays past revisions yet.
func (s *store) CompactRevision() int64 { return 0 }

func (s *store) validateMinimumResourceVersion(minimumResourceVersion string, actualRevision uint64) error {
	if minimumResourceVersion == "" {
		return nil
	}
	minimumRV, err := s.versioner.ParseResourceVersion(minimumResourceVersion)
	if err != nil {
		return apierrors.NewBadRequest(fmt.Sprintf("invalid resource version: %v", err))
	}
	if minimumRV > actualRevision {
		return storage.NewTooLargeResourceVersionError(minimumRV, actualRevision, 0)
	}
	return nil
}
