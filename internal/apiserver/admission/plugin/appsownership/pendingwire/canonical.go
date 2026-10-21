// Parsing/canonicalization derived from Apps 71d7bf5ed3563bd44e8071867212a146fc6ecf14.
// Exact bounded JSON canonicalization shared by the source-pinned wire check.
package pendingwire

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// MaxDefinitionBytes limits the untrusted JSON document accepted by Parse.
	MaxDefinitionBytes = 1 << 20
	// MaxDepth bounds recursive JSON decoding and canonical encoding.
	MaxDepth = 64
	// MaxNodes bounds the number of JSON values accepted in one definition.
	MaxNodes = 100_000
	// MaxNumberChars and MaxNumberExponent bound exact decimal normalization.
	MaxNumberChars    = 256
	MaxNumberExponent = 1024
)

var (
	ErrInvalidJSON       = errors.New("invalid template definition JSON")
	ErrLimitExceeded     = errors.New("template definition exceeds parser limits")
	ErrDefinitionChanged = errors.New("template definition is immutable")
)

// Definition is an immutable canonical representation of one JSON template
// definition. Its internal bytes are never returned without copying.
type Definition struct {
	canonical []byte
	digest    string
}

// Parse validates strict JSON and creates a deterministic canonical digest.
// Duplicate keys, invalid UTF-8, trailing values, excessive nesting, and
// oversized documents are rejected. JSON numbers are normalized using exact
// decimal arithmetic represented as strings, never binary floating point.
func Parse(raw []byte) (Definition, error) {
	value, err := decodeJSON(raw)
	if err != nil {
		return Definition{}, err
	}
	canonical, err := encodeCanonical(value)
	if err != nil {
		if errors.Is(err, ErrLimitExceeded) {
			return Definition{}, err
		}
		return Definition{}, ErrInvalidJSON
	}
	if len(canonical) > MaxDefinitionBytes {
		return Definition{}, ErrLimitExceeded
	}
	sum := sha256.Sum256(canonical)
	return Definition{canonical: canonical, digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

func decodeJSON(raw []byte) (any, error) {
	if len(raw) == 0 || len(raw) > MaxDefinitionBytes {
		return nil, ErrLimitExceeded
	}
	if !utf8.Valid(raw) {
		return nil, ErrInvalidJSON
	}
	if !ValidEscapedSurrogates(raw) {
		return nil, ErrInvalidJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	count := 0
	value, err := decodeValue(decoder, 0, &count)
	if err != nil {
		if errors.Is(err, ErrLimitExceeded) {
			return nil, err
		}
		return nil, ErrInvalidJSON
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrInvalidJSON
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, ErrInvalidJSON
	}
	return value, nil
}

// CanonicalJSON returns a copy of the normalized definition. Callers must
// avoid exposing template values that may contain sensitive data; this package
// does not attempt generic credential detection.
func (d Definition) CanonicalJSON() []byte {
	return append([]byte(nil), d.canonical...)
}

// Digest returns the lowercase SHA-256 digest, or an empty string for an
// uninitialized Definition.
func (d Definition) Digest() string { return d.digest }

// Equal compares canonical bytes as well as digest. It does not rely on
// hash-collision resistance as a substitute for equality within this process.
func (d Definition) Equal(other Definition) bool {
	return d.digest != "" && d.digest == other.digest && bytes.Equal(d.canonical, other.canonical)
}

// RequireSame rejects any attempt to change a pinned immutable definition.
func (d Definition) RequireSame(candidate Definition) error {
	if !d.Equal(candidate) {
		return ErrDefinitionChanged
	}
	return nil
}

func decodeValue(decoder *json.Decoder, depth int, count *int) (any, error) {
	if depth > MaxDepth {
		return nil, ErrLimitExceeded
	}
	*count++
	if *count > MaxNodes {
		return nil, ErrLimitExceeded
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token := token.(type) {
	case json.Delim:
		switch token {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, ErrInvalidJSON
				}
				if _, exists := object[key]; exists {
					return nil, ErrInvalidJSON
				}
				value, err := decodeValue(decoder, depth+1, count)
				if err != nil {
					return nil, err
				}
				object[key] = value
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, ErrInvalidJSON
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for decoder.More() {
				value, err := decodeValue(decoder, depth+1, count)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, ErrInvalidJSON
			}
			return array, nil
		default:
			return nil, ErrInvalidJSON
		}
	case json.Number:
		if _, err := normalizeNumber(string(token)); err != nil {
			return nil, err
		}
		return token, nil
	case string, bool, nil:
		return token, nil
	default:
		return nil, ErrInvalidJSON
	}
}

func encodeCanonical(value any) ([]byte, error) {
	var output bytes.Buffer
	if err := writeCanonical(&output, value, 0); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeCanonical(output *bytes.Buffer, value any, depth int) error {
	if depth > MaxDepth {
		return ErrLimitExceeded
	}
	switch value := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteByte('{')
		for i, key := range keys {
			if i != 0 {
				output.WriteByte(',')
			}
			encodedKey, err := json.Marshal(key)
			if err != nil {
				return ErrInvalidJSON
			}
			output.Write(encodedKey)
			output.WriteByte(':')
			if err := writeCanonical(output, value[key], depth+1); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	case []any:
		output.WriteByte('[')
		for i, entry := range value {
			if i != 0 {
				output.WriteByte(',')
			}
			if err := writeCanonical(output, entry, depth+1); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case string:
		encoded, err := json.Marshal(value)
		if err != nil {
			return ErrInvalidJSON
		}
		output.Write(encoded)
	case json.Number:
		normalized, err := normalizeNumber(string(value))
		if err != nil {
			return err
		}
		output.WriteString(normalized)
	case bool:
		if value {
			output.WriteString("true")
		} else {
			output.WriteString("false")
		}
	case nil:
		output.WriteString("null")
	default:
		return ErrInvalidJSON
	}
	if output.Len() > MaxDefinitionBytes {
		return ErrLimitExceeded
	}
	return nil
}

// normalizeNumber converts an exact JSON decimal to coefficient e scale form.
// For example 1.00e2 and 100 become 1e2, while a large integer never passes
// through float64. The result is itself valid JSON numeric syntax.
func normalizeNumber(raw string) (string, error) {
	if len(raw) == 0 || len(raw) > MaxNumberChars {
		return "", ErrLimitExceeded
	}
	sign := ""
	if raw[0] == '-' {
		sign = "-"
		raw = raw[1:]
	}
	mantissa, exponentText, hasExponent := strings.Cut(raw, "e")
	if !hasExponent {
		mantissa, exponentText, hasExponent = strings.Cut(raw, "E")
	}
	exponent := int64(0)
	if hasExponent {
		parsed, err := strconv.ParseInt(exponentText, 10, 64)
		if err != nil || parsed < -MaxNumberExponent || parsed > MaxNumberExponent {
			return "", ErrLimitExceeded
		}
		exponent = parsed
	}
	whole, fraction, hasFraction := strings.Cut(mantissa, ".")
	if !hasFraction {
		fraction = ""
	}
	digits := whole + fraction
	scale := exponent - int64(len(fraction))
	first := 0
	for first < len(digits) && digits[first] == '0' {
		first++
	}
	if first == len(digits) {
		return "0", nil
	}
	digits = digits[first:]
	for len(digits) > 1 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
		scale++
	}
	limit := int64(MaxNumberExponent + MaxNumberChars)
	if scale < -limit || scale > limit {
		return "", ErrLimitExceeded
	}
	if scale == 0 {
		return sign + digits, nil
	}
	if scale > MaxNumberExponent {
		zeros := int(scale - MaxNumberExponent)
		canonical := digits + strings.Repeat("0", zeros) + "e" + strconv.Itoa(MaxNumberExponent)
		if len(canonical)+len(sign) > MaxNumberChars {
			return "", ErrLimitExceeded
		}
		return sign + canonical, nil
	}
	if scale < -MaxNumberExponent {
		shift := int(-MaxNumberExponent - scale)
		point := len(digits) - shift
		mantissa := ""
		if point > 0 {
			mantissa = digits[:point] + "." + digits[point:]
		} else {
			mantissa = "0." + strings.Repeat("0", -point) + digits
		}
		canonical := mantissa + "e-" + strconv.Itoa(MaxNumberExponent)
		if len(canonical)+len(sign) > MaxNumberChars {
			return "", ErrLimitExceeded
		}
		return sign + canonical, nil
	}
	canonical := sign + digits + "e" + strconv.FormatInt(scale, 10)
	if len(canonical) > MaxNumberChars {
		return "", ErrLimitExceeded
	}
	return canonical, nil
}
