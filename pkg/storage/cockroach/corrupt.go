package cockroach

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/etcd3"
	"k8s.io/apiserver/pkg/storage/value"
)

// These mirror etcd3's unexported handling for
// AllowUnsafeMalformedObjectDeletion.

// maxCorruptObjects caps how many corrupt objects one list names.
const maxCorruptObjects = 100

var errTooManyCorrupt = errors.New("too many errors, the list is truncated")

// A corruptObjectError reports stored data that fails to transform or decode.
type corruptObjectError struct {
	err          error
	decodeFailed bool
	rev          int64
}

func (e *corruptObjectError) Unwrap() error { return e.err }

func (e *corruptObjectError) Error() string {
	what := "data from the storage is not transformable"
	if e.decodeFailed {
		what = "object not decodable"
	}
	return fmt.Sprintf("%s revision=%d: %v", what, e.rev, e.err)
}

func isCorrupt(err error) bool {
	_, ok := errors.AsType[*corruptObjectError](err)
	return ok
}

// corruptMarkingTransformer reports every failure to read stored data as a
// corrupt object.
type corruptMarkingTransformer struct {
	value.Transformer
}

func (t corruptMarkingTransformer) TransformFromStorage(ctx context.Context, data []byte, dataCtx value.Context) ([]byte, bool, error) {
	out, stale, err := t.Transformer.TransformFromStorage(ctx, data, dataCtx)
	if err != nil && !isCorrupt(err) {
		err = &corruptObjectError{err: err}
	}
	return out, stale, err
}

// corruptMarkingDecoder reports every decode failure as a corrupt object.
type corruptMarkingDecoder struct {
	etcd3.Decoder
}

func (d corruptMarkingDecoder) Decode(data []byte, out runtime.Object, rev int64) error {
	err := d.Decoder.Decode(data, out, rev)
	if err != nil && !isCorrupt(err) {
		err = &corruptObjectError{err: err, decodeFailed: true, rev: rev}
	}
	return err
}

func (d corruptMarkingDecoder) DecodeListItem(ctx context.Context, data []byte, rev uint64, newItemFunc func() runtime.Object) (runtime.Object, error) {
	obj, err := d.Decoder.DecodeListItem(ctx, data, rev, newItemFunc)
	if err != nil && !isCorrupt(err) {
		err = &corruptObjectError{err: err, decodeFailed: true, rev: int64(rev)}
	}
	return obj, err
}

// listErrors collects the objects a list fails to read. Without aggregate,
// the first failure stops the list. With it, the list's error names the
// corrupt objects, and any other failure stops the list.
type listErrors struct {
	aggregate bool
	corrupt   []*storage.StorageError
	abort     error
}

// add records err for key and reports whether the list must stop.
func (l *listErrors) add(key string, err error) bool {
	c, ok := errors.AsType[*corruptObjectError](err)
	if !l.aggregate || !ok {
		l.abort = err
		return true
	}
	l.corrupt = append(l.corrupt, storage.NewCorruptObjError(key, c))
	if len(l.corrupt) >= maxCorruptObjects {
		l.abort = errTooManyCorrupt
		return true
	}
	return false
}

// err returns the list's error. That is the failure that stopped the list, or
// one API status naming every corrupt object, as etcd3 reports them.
func (l *listErrors) err(gr schema.GroupResource) error {
	if len(l.corrupt) == 0 {
		return l.abort
	}
	var causes []metav1.StatusCause
	for _, err := range l.corrupt {
		causes = append(causes, metav1.StatusCause{
			Type:    metav1.CauseTypeUnexpectedServerResponse,
			Field:   err.Key,
			Message: err.Error(),
		})
	}
	switch {
	case errors.Is(l.abort, errTooManyCorrupt):
		causes = append(causes, metav1.StatusCause{Type: metav1.CauseTypeTooMany, Message: errTooManyCorrupt.Error()})
	case l.abort != nil:
		causes = append(causes, metav1.StatusCause{Type: metav1.CauseTypeUnexpectedServerResponse, Message: l.abort.Error()})
	}
	message := fmt.Sprintf("failed to read one or more %s from the storage: %v", gr.String(), l.corrupt[0])
	if len(l.corrupt) > 1 {
		message = fmt.Sprintf("%s (and %d more)", message, len(l.corrupt)-1)
	}
	return &apierrors.StatusError{ErrStatus: metav1.Status{
		Status: metav1.StatusFailure,
		Code:   http.StatusInternalServerError,
		Reason: metav1.StatusReasonStoreReadError,
		Details: &metav1.StatusDetails{
			Group:  gr.Group,
			Kind:   gr.Resource,
			Name:   "list",
			Causes: causes,
		},
		Message: message,
	}}
}

// corruptDeletedError returns the watch error for a delete whose object
// can't be read, which is how an unsafe delete looks to a watcher.
func corruptDeletedError(err error) error {
	return &apierrors.StatusError{ErrStatus: metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    http.StatusInternalServerError,
		Reason:  metav1.StatusReasonStoreReadError,
		Message: fmt.Sprintf("saw a DELETED event, but object data is corrupt - %v", strings.TrimSpace(err.Error())),
	}}
}
