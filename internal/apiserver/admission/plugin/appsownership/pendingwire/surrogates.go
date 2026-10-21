// Source: Apps 71d7bf5ed3563bd44e8071867212a146fc6ecf14 internal/jsontext.
// Package jsontext provides lossless JSON string checks shared by strict parsers.
// Callers must still validate JSON grammar, byte/depth limits and duplicate keys.
package pendingwire

import "strconv"

// ValidEscapedSurrogates rejects lone UTF-16 surrogate escapes before
// encoding/json can replace them with U+FFFD. It scans JSON strings only, so
// escaped backslashes such as `\\uD800` remain ordinary literal text.
func ValidEscapedSurrogates(raw []byte) bool {
	for i := 0; i < len(raw); {
		if raw[i] != '"' {
			i++
			continue
		}
		i++
		for i < len(raw) {
			switch raw[i] {
			case '"':
				i++
				goto nextString
			case '\\':
				if i+1 >= len(raw) {
					return false
				}
				if raw[i+1] != 'u' {
					i += 2
					continue
				}
				unit, ok := escapedCodeUnit(raw, i)
				if !ok {
					return false
				}
				if unit >= 0xdc00 && unit <= 0xdfff {
					return false
				}
				if unit >= 0xd800 && unit <= 0xdbff {
					if i+12 > len(raw) || raw[i+6] != '\\' || raw[i+7] != 'u' {
						return false
					}
					low, ok := escapedCodeUnit(raw, i+6)
					if !ok || low < 0xdc00 || low > 0xdfff {
						return false
					}
					i += 12
					continue
				}
				i += 6
			default:
				i++
			}
		}
		return false
	nextString:
	}
	return true
}

func escapedCodeUnit(raw []byte, offset int) (uint16, bool) {
	if offset+6 > len(raw) || raw[offset] != '\\' || raw[offset+1] != 'u' {
		return 0, false
	}
	parsed, err := strconv.ParseUint(string(raw[offset+2:offset+6]), 16, 16)
	return uint16(parsed), err == nil
}
