package projectstorage

import (
	"context"

	"k8s.io/apiserver/pkg/storage/value"
)

// tenantTransformer wraps a value.Transformer so the etcd key (which the
// storage layer makes available via dataCtx.AuthenticatedData) survives past
// the codec boundary. On reads we prepend a header carrying the key; the
// paired tenantCodec parses that header before delegating to the inner codec.
type tenantTransformer struct {
	inner value.Transformer
}

func (t *tenantTransformer) TransformFromStorage(ctx context.Context, data []byte, dataCtx value.Context) ([]byte, bool, error) {
	var (
		plaintext []byte
		stale     bool
		err       error
	)
	if t.inner != nil {
		plaintext, stale, err = t.inner.TransformFromStorage(ctx, data, dataCtx)
		if err != nil {
			return nil, stale, err
		}
	} else {
		plaintext = data
	}
	return prependTenantHeader(dataCtx.AuthenticatedData(), plaintext), stale, nil
}

func (t *tenantTransformer) TransformToStorage(ctx context.Context, data []byte, dataCtx value.Context) ([]byte, error) {
	if _, body, ok := splitDataIntoKeyObject(data); ok {
		data = body
	}
	if t.inner == nil {
		return data, nil
	}
	return t.inner.TransformToStorage(ctx, data, dataCtx)
}
