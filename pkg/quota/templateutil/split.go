package templateutil

import (
	"fmt"
	"strings"
)

// Segment represents a portion of a template string. If Expression is true, Value
// contains the CEL expression (without delimiters). Otherwise Value is literal text.
type Segment struct {
	Value      string
	Expression bool
}

// Split divides a template string into literal and CEL expression segments. Mixed
// literal/ expression content such as "{{ trigger.metadata.name }}-claim" is preserved
// in order. An error is returned for unmatched delimiters.
func Split(templateStr string) ([]Segment, error) {
	if templateStr == "" {
		return []Segment{{Value: "", Expression: false}}, nil
	}

	runes := []rune(templateStr)
	var segments []Segment

	literalStart := 0
	for i := 0; i < len(runes); {
		if runes[i] == '{' && i+1 < len(runes) && runes[i+1] == '{' {
			// Skip consecutive opening braces (e.g. {{{) which are not valid CEL delimiters.
			if i+2 < len(runes) && runes[i+2] == '{' {
				j := i + 2
				for j < len(runes) && runes[j] == '{' {
					j++
				}
				i = j
				continue
			}

			if literalStart < i {
				segments = append(segments, Segment{Value: string(runes[literalStart:i])})
			}

			end := findClosingDelimiter(runes, i+2)
			if end == -1 {
				return nil, fmt.Errorf("unmatched '{{' delimiter in template")
			}

			expr := strings.TrimSpace(string(runes[i+2 : end]))
			if expr != "" {
				segments = append(segments, Segment{Value: expr, Expression: true})
			}

			i = end + 2
			literalStart = i
			continue
		}

		i++
	}

	if literalStart < len(runes) {
		segments = append(segments, Segment{Value: string(runes[literalStart:])})
	}

	// If the original string was empty we returned earlier; ensure callers don't
	// receive nil slices for empty literals.
	if len(segments) == 0 {
		segments = append(segments, Segment{Value: ""})
	}

	return segments, nil
}

// findClosingDelimiter mirrors the validation parser behaviour by skipping string
// literals and returning the index of the character preceding the closing '}}'.
func findClosingDelimiter(runes []rune, start int) int {
	for i := start; i < len(runes)-1; i++ {
		if runes[i] == '"' || runes[i] == '\'' {
			i = skipString(runes, i)
			if i == -1 {
				return -1
			}
			continue
		}
		if runes[i] == '}' && runes[i+1] == '}' {
			return i
		}
	}
	return -1
}

func skipString(runes []rune, start int) int {
	if start >= len(runes) {
		return -1
	}

	delimiter := runes[start]
	for i := start + 1; i < len(runes); i++ {
		if runes[i] == delimiter {
			backslashCount := 0
			for j := i - 1; j >= start && runes[j] == '\\'; j-- {
				backslashCount++
			}
			if backslashCount%2 == 0 {
				return i
			}
		}
	}
	return -1
}

// ContainsExpression returns true if the template string contains at least one CEL expression.
// It returns an error if the template has malformed delimiters.
func ContainsExpression(templateStr string) (bool, error) {
	segments, err := Split(templateStr)
	if err != nil {
		return false, err
	}

	for _, segment := range segments {
		if segment.Expression {
			return true, nil
		}
	}

	return false, nil
}
