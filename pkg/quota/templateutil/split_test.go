package templateutil

import (
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    []Segment
		expectError bool
	}{
		{
			name:     "empty string",
			input:    "",
			expected: []Segment{{Value: "", Expression: false}},
		},
		{
			name:     "literal only",
			input:    "literal-text",
			expected: []Segment{{Value: "literal-text", Expression: false}},
		},
		{
			name:     "simple expression",
			input:    "{{trigger.metadata.name}}",
			expected: []Segment{{Value: "trigger.metadata.name", Expression: true}},
		},
		{
			name:  "mixed literal and expression",
			input: "{{trigger.metadata.name}}-claim",
			expected: []Segment{
				{Value: "trigger.metadata.name", Expression: true},
				{Value: "-claim", Expression: false},
			},
		},
		{
			name:  "literal prefix and expression",
			input: "prefix-{{trigger.metadata.name}}",
			expected: []Segment{
				{Value: "prefix-", Expression: false},
				{Value: "trigger.metadata.name", Expression: true},
			},
		},
		{
			name:  "multiple expressions with literals",
			input: "{{expr1}}-middle-{{expr2}}-suffix",
			expected: []Segment{
				{Value: "expr1", Expression: true},
				{Value: "-middle-", Expression: false},
				{Value: "expr2", Expression: true},
				{Value: "-suffix", Expression: false},
			},
		},
		{
			name:  "consecutive expressions",
			input: "{{expr1}}{{expr2}}",
			expected: []Segment{
				{Value: "expr1", Expression: true},
				{Value: "expr2", Expression: true},
			},
		},
		{
			name:     "empty expression",
			input:    "{{}}",
			expected: []Segment{{Value: "", Expression: false}},
		},
		{
			name:     "whitespace only expression",
			input:    "{{   }}",
			expected: []Segment{{Value: "", Expression: false}},
		},
		{
			name:     "expression with whitespace",
			input:    "{{  trigger.metadata.name  }}",
			expected: []Segment{{Value: "trigger.metadata.name", Expression: true}},
		},
		{
			name:     "expression with string literal containing braces",
			input:    `{{"value with } brace"}}`,
			expected: []Segment{{Value: `"value with } brace"`, Expression: true}},
		},
		{
			name:     "expression with escaped quotes",
			input:    `{{"string with \"escaped\" quotes"}}`,
			expected: []Segment{{Value: `"string with \"escaped\" quotes"`, Expression: true}},
		},
		{
			name:     "expression with single quotes",
			input:    "{{trigger.metadata.labels['environment']}}",
			expected: []Segment{{Value: "trigger.metadata.labels['environment']", Expression: true}},
		},
		{
			name:     "expression with both quote types",
			input:    `{{trigger.metadata.labels["key"] + trigger.annotations['other']}}`,
			expected: []Segment{{Value: `trigger.metadata.labels["key"] + trigger.annotations['other']`, Expression: true}},
		},
		{
			name:     "single brace not treated as expression",
			input:    "{not an expression}",
			expected: []Segment{{Value: "{not an expression}", Expression: false}},
		},
		{
			name:     "triple braces ignored",
			input:    "{{{not valid}}}",
			expected: []Segment{{Value: "{{{not valid}}}", Expression: false}},
		},
		{
			name:  "mixed triple braces and valid expression",
			input: "{{{invalid}}}{{valid}}",
			expected: []Segment{
				{Value: "{{{invalid}}}", Expression: false},
				{Value: "valid", Expression: true},
			},
		},
		{
			name:        "unmatched opening delimiter",
			input:       "{{incomplete",
			expectError: true,
		},
		{
			name:        "unmatched delimiter with closing single brace",
			input:       "{{expr}",
			expectError: true,
		},
		{
			name:     "complex CEL expression",
			input:    "{{trigger.spec.resources[0].limits['memory'] > '1Gi' ? 'large' : 'small'}}",
			expected: []Segment{{Value: "trigger.spec.resources[0].limits['memory'] > '1Gi' ? 'large' : 'small'", Expression: true}},
		},
		{
			name:     "expression with nested function calls",
			input:    "{{has(trigger.spec, 'nested.field') ? 'yes' : 'no'}}",
			expected: []Segment{{Value: "has(trigger.spec, 'nested.field') ? 'yes' : 'no'", Expression: true}},
		},
		{
			name:     "expression with unicode characters",
			input:    "{{trigger.metadata.labels['环境'] + 'ñoño'}}",
			expected: []Segment{{Value: "trigger.metadata.labels['环境'] + 'ñoño'", Expression: true}},
		},
		{
			name:  "real world mixed template example",
			input: "project-{{trigger.metadata.namespace}}-{{user.name}}-claim",
			expected: []Segment{
				{Value: "project-", Expression: false},
				{Value: "trigger.metadata.namespace", Expression: true},
				{Value: "-", Expression: false},
				{Value: "user.name", Expression: true},
				{Value: "-claim", Expression: false},
			},
		},
		{
			name:     "expression with literal braces in string",
			input:    `{{"{{literal}}"}}`,
			expected: []Segment{{Value: `"{{literal}}"`, Expression: true}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Split(tt.input)
			if tt.expectError {
				if err == nil {
					t.Errorf("Split(%q) expected error but got none", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("Split(%q) unexpected error: %v", tt.input, err)
				return
			}
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Split(%q) = %+v, want %+v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSplitStringLiteralHandling(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    []Segment
		expectError bool
	}{
		{
			name:        "expression with unclosed string literal",
			input:       `{{"unclosed string}}`,
			expectError: true,
		},
		{
			name:     "expression with nested quotes and braces",
			input:    `{{"outer \"inner with }\" brace"}}`,
			expected: []Segment{{Value: `"outer \"inner with }\" brace"`, Expression: true}},
		},
		{
			name:     "expression with mixed quotes",
			input:    `{{'single' + "double"}}`,
			expected: []Segment{{Value: `'single' + "double"`, Expression: true}},
		},
		{
			name:     "expression with escaped backslashes",
			input:    `{{"path\\with\\backslashes"}}`,
			expected: []Segment{{Value: `"path\\with\\backslashes"`, Expression: true}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Split(tt.input)
			if tt.expectError {
				if err == nil {
					t.Errorf("Split(%q) expected error but got none", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("Split(%q) unexpected error: %v", tt.input, err)
				return
			}
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Split(%q) = %+v, want %+v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSplitEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []Segment
	}{
		{
			name:     "only braces",
			input:    "{{}}",
			expected: []Segment{{Value: "", Expression: false}},
		},
		{
			name:     "multiple empty expressions",
			input:    "{{}}{{}}",
			expected: []Segment{{Value: "", Expression: false}},
		},
		{
			name:  "empty expression between literals",
			input: "start{{}}end",
			expected: []Segment{
				{Value: "start", Expression: false},
				{Value: "end", Expression: false},
			},
		},
		{
			name:     "four braces not treated as two expressions",
			input:    "{{{{",
			expected: []Segment{{Value: "{{{{", Expression: false}},
		},
		{
			name:     "five braces mixed pattern",
			input:    "{{{{{",
			expected: []Segment{{Value: "{{{{{", Expression: false}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Split(tt.input)
			if err != nil {
				t.Errorf("Split(%q) unexpected error: %v", tt.input, err)
				return
			}
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Split(%q) = %+v, want %+v", tt.input, result, tt.expected)
			}
		})
	}
}
