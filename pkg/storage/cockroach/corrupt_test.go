package cockroach

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/apis/example"
	"k8s.io/apiserver/pkg/storage"
	storagetesting "k8s.io/apiserver/pkg/storage/testing"
	"k8s.io/apiserver/pkg/storage/value"
)

// The upstream tests for objects that can't be read back, which etcd3 runs in
// its store_test.go. They switch on a failing transformer or codec to make a
// stored object unreadable, then check that lists report it as corrupt and
// that AllowUnsafeMalformedObjectDeletion lets a client delete it. The tests
// after them cover which read errors count as corrupt, and when a list stops
// collecting them.

func unsafeDeletion(c *Config) { c.AllowUnsafeMalformedObjectDeletion = true }

// decorated configures a store as Decorator does with the feature gate on.
func decorated(c *Config) { c.AllowUnsafeMalformedObjectDeletion, c.MarkCorrupt = true, true }

// failingTransformer fails every read while switched on.
type failingTransformer struct {
	value.Transformer
	fail atomic.Bool
}

func (f *failingTransformer) TransformFromStorage(ctx context.Context, data []byte, dataCtx value.Context) ([]byte, bool, error) {
	if f.fail.Load() {
		return nil, false, errors.New("synthetic error")
	}
	return f.Transformer.TransformFromStorage(ctx, data, dataCtx)
}

// failingCodec fails every decode while switched on.
type failingCodec struct {
	runtime.Codec
	fail atomic.Bool
}

func (f *failingCodec) Decode(data []byte, defaults *schema.GroupVersionKind, into runtime.Object) (runtime.Object, *schema.GroupVersionKind, error) {
	if f.fail.Load() {
		return nil, nil, errors.New("synthetic error")
	}
	return f.Codec.Decode(data, defaults, into)
}

// failingTransformerSetup is a store configured by mode whose transformer
// fails while the returned flag is set.
func failingTransformerSetup(t *testing.T, mode func(*Config)) (context.Context, testStore, *atomic.Bool) {
	transformer := &failingTransformer{Transformer: testConfig("").Transformer}
	ctx, s := testSetup(t, mode, func(c *Config) { c.Transformer = transformer })
	return ctx, s, &transformer.fail
}

func failingCodecSetup(t *testing.T) (context.Context, testStore, *atomic.Bool) {
	codec := &failingCodec{Codec: testCodec()}
	ctx, s := testSetup(t, unsafeDeletion, func(c *Config) { c.Codec = codec })
	return ctx, s, &codec.fail
}

// injectedCorrupt is the error the upstream tests inject for a corrupt object.
var injectedCorrupt = &corruptObjectError{err: errors.New("bits flipped")}

func TestDeleteExpectedTransformError(t *testing.T) {
	ctx, s, fail := failingTransformerSetup(t, unsafeDeletion)
	storagetesting.RunTestDeleteExpectedTransformOrDecodeError(ctx, t, s, fail.Store)
}

func TestDeleteExpectedDecodeError(t *testing.T) {
	ctx, s, fail := failingCodecSetup(t)
	storagetesting.RunTestDeleteExpectedTransformOrDecodeError(ctx, t, s, fail.Store)
}

func TestDeleteWithConflictAndMissingExpectedTransformOrDecodeError(t *testing.T) {
	ctx, s, fail := failingCodecSetup(t)
	storagetesting.RunTestDeleteWithConflictAndMissingExpectedTransformOrDecodeError(ctx, t, s, fail.Store)
}

func TestDeleteWithSuggestionAndMissingExpectedTransformOrDecodeError(t *testing.T) {
	ctx, s := testSetup(t, unsafeDeletion)
	storagetesting.RunTestDeleteWithSuggestionAndMissingExpectedTransformOrDecodeError(ctx, t, s)
}

func TestGetListWithErrorAggregation(t *testing.T) {
	ctx, s := testSetup(t, unsafeDeletion)
	storagetesting.RunTestGetListWithErrorAggregation(ctx, t, s, injectedCorrupt)
}

func TestGetListWithoutErrorAggregation(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestGetListWithoutErrorAggregation(ctx, t, s, injectedCorrupt)
}

func TestWatchWithUnsafeDelete(t *testing.T) {
	ctx, s := testSetup(t, unsafeDeletion)
	storagetesting.RunTestWatchWithUnsafeDelete(ctx, t, s, injectedCorrupt)
}

// An object the store can't read is corrupt everywhere. Get offers the
// unsafe delete, a list names the object, and only the unsafe delete removes
// it.
func TestUnreadableObjectsAreCorrupt(t *testing.T) {
	ctx, s, fail := failingTransformerSetup(t, decorated)
	createPod(ctx, t, s, "bad")
	fail.Store(true)

	require.True(t, storage.IsCorruptObject(s.Get(ctx, "/pods/test-ns/bad", storage.GetOptions{}, &example.Pod{})))
	err := s.GetList(ctx, "/pods/", storage.ListOptions{Predicate: storage.Everything, Recursive: true}, &example.PodList{})
	var status apierrors.APIStatus
	require.ErrorAs(t, err, &status)
	require.Equal(t, metav1.StatusReasonStoreReadError, status.Status().Reason)
	require.Len(t, status.Status().Details.Causes, 1)
	require.Equal(t, "/pods/test-ns/bad", status.Status().Details.Causes[0].Field)

	del := func(opts storage.DeleteOptions) error {
		return s.Delete(ctx, "/pods/test-ns/bad", &example.Pod{}, nil, storage.ValidateAllObjectFunc, nil, opts)
	}
	require.Error(t, del(storage.DeleteOptions{}), "an ordinary delete of an unreadable object")
	require.NoError(t, del(storage.DeleteOptions{ExpectTransformOrDecodeError: true}))
	fail.Store(false)
	require.True(t, storage.IsNotFound(s.Get(ctx, "/pods/test-ns/bad", storage.GetOptions{}, &example.Pod{})))
}

func corruptCauses(t *testing.T, l *listErrors) []metav1.StatusCause {
	t.Helper()
	var status apierrors.APIStatus
	require.ErrorAs(t, l.err(schema.GroupResource{Resource: "pods"}), &status)
	return status.Status().Details.Causes
}

func TestListErrorsStopAtLimit(t *testing.T) {
	l := listErrors{aggregate: true}
	for i := range maxCorruptObjects {
		require.Equal(t, i == maxCorruptObjects-1, l.add("/pods/bad", injectedCorrupt), "add %d", i+1)
	}
	causes := corruptCauses(t, &l)
	require.Len(t, causes, maxCorruptObjects+1)
	require.Equal(t, metav1.CauseTypeTooMany, causes[maxCorruptObjects].Type)
}

func TestListErrorsStopOnOtherErrors(t *testing.T) {
	failure := errors.New("read failed")
	l := listErrors{aggregate: true}
	require.True(t, l.add("/pods/bad", failure))
	require.ErrorIs(t, l.err(schema.GroupResource{}), failure)

	l = listErrors{aggregate: true}
	require.False(t, l.add("/pods/bad", injectedCorrupt))
	require.True(t, l.add("/pods/other", failure))
	causes := corruptCauses(t, &l)
	require.Len(t, causes, 2)
	require.Equal(t, "/pods/bad", causes[0].Field)
	require.Equal(t, failure.Error(), causes[1].Message)

	l = listErrors{}
	require.True(t, l.add("/pods/bad", injectedCorrupt), "without aggregation a corrupt object stops the list")
}
