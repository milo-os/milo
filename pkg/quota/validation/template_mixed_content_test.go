package validation

import (
	"testing"

	"go.miloapis.com/milo/pkg/quota/templateutil"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestTemplateMixedContent(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		desc    string
	}{
		{
			name:    "mixed literal and expression valid",
			input:   "{{trigger.metadata.name}}-claim",
			wantErr: false,
			desc:    "expression followed by literal should be valid",
		},
		{
			name:    "literal prefix with expression",
			input:   "prefix-{{trigger.metadata.name}}",
			wantErr: false,
			desc:    "literal followed by expression should be valid",
		},
		{
			name:    "multiple expressions with literals",
			input:   "{{trigger.metadata.namespace}}-{{user.name}}-claim",
			wantErr: false,
			desc:    "multiple expressions interspersed with literals should be valid",
		},
		{
			name:    "pure literal kubernetes name",
			input:   "literal-resource-name",
			wantErr: false,
			desc:    "pure literal should validate as kubernetes name",
		},
		{
			name:    "pure literal invalid kubernetes name",
			input:   "INVALID_NAME",
			wantErr: true,
			desc:    "pure literal with invalid kubernetes name should fail",
		},
		{
			name:    "expression with invalid CEL",
			input:   "{{invalid..syntax}}-claim",
			wantErr: true,
			desc:    "mixed content with invalid CEL should fail",
		},
		{
			name:    "expression with disallowed variable",
			input:   "{{invalidvar.name}}-claim",
			wantErr: true,
			desc:    "mixed content with disallowed variable should fail",
		},
		{
			name:    "empty expression with literal",
			input:   "claim",
			wantErr: false,
			desc:    "empty expression with literal should be treated as pure literal",
		},
		{
			name:    "consecutive expressions",
			input:   "{{trigger.metadata.name}}{{user.name}}",
			wantErr: false,
			desc:    "consecutive expressions should be valid",
		},
		{
			name:    "literal that looks like triple braces",
			input:   "not-expr-literal",
			wantErr: false,
			desc:    "triple braces should be treated as literal text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateTemplateOrKubernetesName(tt.input, claimTemplateAllowedVariables, false, field.NewPath("test"))
			hasErr := len(errs) > 0
			if hasErr != tt.wantErr {
				t.Errorf("validateTemplateOrKubernetesName(%q) error = %v, wantErr %v", tt.input, hasErr, tt.wantErr)
				if hasErr {
					for _, err := range errs {
						t.Logf("Error: %v", err)
					}
				}
			}
		})
	}
}

func TestTemplateContainsExpression(t *testing.T) {
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := templateutil.ContainsExpression(tt.input)
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

func TestValidateTemplateOrGenerateName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		desc    string
	}{
		{
			name:    "valid literal generateName",
			input:   "prefix-",
			wantErr: false,
			desc:    "literal generateName ending with dash should be valid",
		},
		{
			name:    "valid template generateName",
			input:   "{{trigger.metadata.name}}-",
			wantErr: false,
			desc:    "template generateName should be valid",
		},
		{
			name:    "mixed template generateName",
			input:   "prefix-{{trigger.metadata.name}}-",
			wantErr: false,
			desc:    "mixed literal and template generateName should be valid",
		},
		{
			name:    "literal missing dash",
			input:   "prefix",
			wantErr: true,
			desc:    "literal generateName without dash should fail",
		},
		{
			name:    "literal empty prefix",
			input:   "-",
			wantErr: true,
			desc:    "generateName with only dash should fail",
		},
		{
			name:    "literal invalid dns prefix",
			input:   "INVALID-",
			wantErr: true,
			desc:    "generateName with invalid DNS prefix should fail",
		},
		{
			name:    "template with invalid CEL",
			input:   "{{invalid..syntax}}-",
			wantErr: true,
			desc:    "template generateName with invalid CEL should fail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateTemplateOrGenerateName(tt.input, claimTemplateAllowedVariables, false, field.NewPath("test"))
			hasErr := len(errs) > 0
			if hasErr != tt.wantErr {
				t.Errorf("validateTemplateOrGenerateName(%q) error = %v, wantErr %v", tt.input, hasErr, tt.wantErr)
				if hasErr {
					for _, err := range errs {
						t.Logf("Error: %v", err)
					}
				}
			}
		})
	}
}
