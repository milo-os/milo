package templateutil

import (
	"testing"
)

func TestContainsExpression(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
		wantErr  bool
	}{
		{
			name:     "literal only",
			input:    "literal-text",
			expected: false,
		},
		{
			name:     "simple expression",
			input:    "{{trigger.metadata.name}}",
			expected: true,
		},
		{
			name:     "mixed content",
			input:    "{{trigger.metadata.name}}-claim",
			expected: true,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "empty expression",
			input:    "{{}}",
			expected: false,
		},
		{
			name:     "whitespace only expression",
			input:    "{{   }}",
			expected: false,
		},
		{
			name:     "triple braces not treated as expression",
			input:    "{{{not-expr}}}",
			expected: false,
		},
		{
			name:     "single braces",
			input:    "{not-expr}",
			expected: false,
		},
		{
			name:     "multiple expressions with literal",
			input:    "{{expr1}}-{{expr2}}",
			expected: true,
		},
		{
			name:     "mixed literal and expression",
			input:    "prefix-{{expr}}-suffix",
			expected: true,
		},
		{
			name:    "unmatched braces",
			input:   "{{incomplete",
			wantErr: true,
		},
		{
			name:     "consecutive expressions",
			input:    "{{expr1}}{{expr2}}",
			expected: true,
		},
		{
			name:     "complex expression with strings",
			input:    `{{"value with } brace"}}`,
			expected: true,
		},
		{
			name:     "expression with escaped quotes",
			input:    `{{"string with \"escaped\" quotes"}}`,
			expected: true,
		},
		{
			name:     "real world mixed template",
			input:    "project-{{trigger.metadata.namespace}}-{{user.name}}-claim",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ContainsExpression(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ContainsExpression(%q) expected error but got none", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("ContainsExpression(%q) unexpected error: %v", tt.input, err)
				return
			}
			if result != tt.expected {
				t.Errorf("ContainsExpression(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
