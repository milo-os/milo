package etcdshared

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage/etcd3"
)

// CRD storage codecs repair malformed ObjectMeta. The capability must inspect
// decrypted plaintext before that repair can discard a reservation. This view
// only supports bounded JSON Project records; public API decoding is unchanged.
type reservationDecoder struct{ etcd3.Decoder }

const reservationRecordLimit = 2 << 20
const reservationJSONDepth = 64

func (d reservationDecoder) Decode(value []byte, out runtime.Object, rev int64) error {
	if err := strictReservationRecord(value); err != nil {
		return err
	}
	if err := d.Decoder.Decode(value, out, rev); err != nil {
		return ErrReservationBinding
	}
	return unchangedReservationDecode(value, out)
}
func (d reservationDecoder) DecodeListItem(ctx context.Context, value []byte, rev uint64, newItem func() runtime.Object) (runtime.Object, error) {
	if err := strictReservationRecord(value); err != nil {
		return nil, err
	}
	obj, err := d.Decoder.DecodeListItem(ctx, value, rev, newItem)
	if err != nil {
		return nil, ErrReservationBinding
	}
	if err := unchangedReservationDecode(value, obj); err != nil {
		return nil, err
	}
	return obj, nil
}
func strictReservationRecord(value []byte) error {
	if len(value) == 0 || len(value) > reservationRecordLimit || !utf8.Valid(value) {
		return ErrReservationBinding
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := reservationJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrReservationBinding
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(value, &object) != nil || object == nil {
		return ErrReservationBinding
	}
	for key := range object {
		if key != "metadata" && strings.EqualFold(key, "metadata") {
			return ErrReservationBinding
		}
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(object["metadata"], &metadata) != nil || metadata == nil {
		return ErrReservationBinding
	}
	// Unknown/case-aliased metadata must not be silently dropped or interpreted
	// as a canonical field by ObjectMeta's case-insensitive JSON decoder.
	allowed := map[string]bool{"name": true, "generateName": true, "namespace": true, "selfLink": true, "uid": true, "resourceVersion": true, "generation": true, "creationTimestamp": true, "deletionTimestamp": true, "deletionGracePeriodSeconds": true, "labels": true, "annotations": true, "ownerReferences": true, "finalizers": true, "managedFields": true}
	for key := range metadata {
		if !allowed[key] {
			return ErrReservationBinding
		}
	}
	for _, field := range []string{"labels", "annotations"} {
		raw, present := metadata[field]
		if !present {
			continue
		}
		var entries map[string]json.RawMessage
		if json.Unmarshal(raw, &entries) != nil || entries == nil {
			return ErrReservationBinding
		}
		for _, entry := range entries {
			trimmed := bytes.TrimSpace(entry)
			if len(trimmed) == 0 || trimmed[0] != '"' {
				return ErrReservationBinding
			}
			var text string
			if json.Unmarshal(entry, &text) != nil {
				return ErrReservationBinding
			}
		}
	}
	var typed metav1.ObjectMeta
	if json.Unmarshal(object["metadata"], &typed) != nil {
		return ErrReservationBinding
	}
	return nil
}
func reservationJSONValue(decoder *json.Decoder, depth int) error {
	if depth > reservationJSONDepth {
		return ErrReservationBinding
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrReservationBinding
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]struct{}{}
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return ErrReservationBinding
			}
			key, ok := token.(string)
			if !ok {
				return ErrReservationBinding
			}
			if _, exists := keys[key]; exists {
				return ErrReservationBinding
			}
			keys[key] = struct{}{}
			if err := reservationJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrReservationBinding
		}
	case '[':
		for decoder.More() {
			if err := reservationJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrReservationBinding
		}
	default:
		return ErrReservationBinding
	}
	return nil
}

// Reject defaulting, pruning or metadata repair during decoding. A metadata-only
// capability must not rewrite unrelated persisted fields as a side effect.
// Compare JSON numbers without float conversion, and exclude only the RV that
// etcd's versioner supplies from the key's modification revision.
func unchangedReservationDecode(value []byte, out runtime.Object) error {
	encoded, err := json.Marshal(out)
	if err != nil {
		return ErrReservationBinding
	}
	normalize := func(data []byte) (map[string]any, error) {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var object map[string]any
		if err := decoder.Decode(&object); err != nil {
			return nil, ErrReservationBinding
		}
		metadata, ok := object["metadata"].(map[string]any)
		if !ok {
			return nil, ErrReservationBinding
		}
		delete(metadata, "resourceVersion")
		return object, nil
	}
	before, err := normalize(value)
	if err != nil {
		return err
	}
	after, err := normalize(encoded)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, after) {
		return ErrReservationBinding
	}
	return nil
}
