package projectstorage

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// tenantCodec wraps a runtime.Codec to pair with tenantTransformer. On Decode
// it parses the etcd-key header that the transformer prepended, decodes the
// inner bytes normally, then records (object UID → tenant) in the per-cacher
// side channel so tenantAwareKeyFunc can look it up. Nothing is written onto
// the object itself: tenant identity lives entirely off-object, never visible
// to admission/audit/webhooks or API clients.
type tenantCodec struct {
	runtime.Codec
	tm *tenantMap
}

func (c *tenantCodec) Decode(data []byte, defaults *schema.GroupVersionKind, into runtime.Object) (runtime.Object, *schema.GroupVersionKind, error) {
	key, objectData, ok := splitDataIntoKeyObject(data)
	if !ok {
		return c.Codec.Decode(data, defaults, into)
	}
	obj, gvk, err := c.Codec.Decode(objectData, defaults, into)
	if err != nil {
		return obj, gvk, err
	}
	if accessor, err := meta.Accessor(obj); err == nil {
		c.tm.record(accessor.GetUID(), key)
	}
	return obj, gvk, nil
}

func (c *tenantCodec) EncodeNondeterministic(o runtime.Object, w io.Writer) error {
	if enc, ok := c.Codec.(runtime.NondeterministicEncoder); ok {
		return enc.EncodeNondeterministic(o, w)
	}

	return c.Encode(o, w)
}

func (c *tenantCodec) EncodeWithAllocator(obj runtime.Object, w io.Writer, memAlloc runtime.MemoryAllocator) error {
	if enc, ok := c.Codec.(runtime.EncoderWithAllocator); ok {
		return enc.EncodeWithAllocator(obj, w, memAlloc)
	}

	return c.Encode(obj, w)
}

// optional encoder extensions
var (
	_ runtime.NondeterministicEncoder = (*tenantCodec)(nil)
	_ runtime.EncoderWithAllocator    = (*tenantCodec)(nil)
)

// Segment markers in storage keys for tenant isolation. Every key produced by
// projectKeyRewriter has the shape "<resourcePrefix>/<segment>/<suffix>"
const (
	tenantSegment = "/clusters/"
	rootSegment   = "/root"
)

// scopePrefix returns the storage-key prefix that bounds a single scope:
// "<prefix>/clusters/<tenantID>/" for a tenant, "<prefix>/root/" otherwise.
// Used both to compute the expected prefix of a paginated continue token and
// as the building block for namespacedKeyForPrefix.
func scopePrefix(prefix, tenantID string) string {
	if tenantID != "" {
		return prefix + tenantSegment + tenantID + "/"
	}
	return prefix + rootSegment + "/"
}

// namespacedKeyForPrefix injects the scope segment into a storage key.
// Pre-condition: key starts with prefix.
func namespacedKeyForPrefix(key, prefix, tenantID string) string {
	// suffix begins with '/', so drop the trailing '/' from scopePrefix.
	scoped := scopePrefix(prefix, tenantID)
	return scoped[:len(scoped)-1] + key[len(prefix):]
}

// Framing discriminator prepended to bytes flowing from storage to codec.
// \x7f (DEL) is invalid as the first byte of JSON (not in the value-start set)
// and decodes as protobuf wire type 7, which is reserved.
var tenantHeaderMagic = []byte{0x7f, 'm', 'i', 'l', 'o'}

// Layout: magic | keyLen(u32be) | key(keyLen) | body(rest)
func prependTenantHeader(key []byte, body []byte) []byte {
	out := make([]byte, 0, len(tenantHeaderMagic)+4+len(key)+len(body))
	out = append(out, tenantHeaderMagic...)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(key)))
	out = append(out, lenBuf[:]...)
	out = append(out, key...)
	out = append(out, body...)
	return out
}

// splitDataIntoKeyObject cuts data into it's storage key and the body
// representing the object. Refer to prependTenantHeader for expected layout.
func splitDataIntoKeyObject(data []byte) (key string, body []byte, ok bool) {
	if len(data) < len(tenantHeaderMagic)+4 {
		return "", nil, false
	}
	if !bytes.HasPrefix(data, tenantHeaderMagic) {
		return "", nil, false
	}
	rest := data[len(tenantHeaderMagic):]
	keyLen := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if uint32(len(rest)) < keyLen {
		return "", nil, false
	}
	return string(rest[:keyLen]), rest[keyLen:], true
}

// tenantFromStorageKey extracts tenant identifier from a storage key.
// Returns "" if the key has no tenant segment.
func tenantFromStorageKey(key string) string {
	_, after, ok := strings.Cut(key, tenantSegment)
	if !ok {
		return ""
	}
	if next := strings.IndexByte(after, '/'); next > 0 {
		return after[:next]
	}
	return after
}
