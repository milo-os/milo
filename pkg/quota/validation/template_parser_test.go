package validation

import (
	"reflect"
	"testing"

	"go.miloapis.com/milo/pkg/quota/templateutil"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestCELExpressionParsing(t *testing.T) {

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple expression",
			input:    "{{trigger.metadata.name}}",
			expected: []string{"trigger.metadata.name"},
		},
		{
			name:     "expression with string concatenation",
			input:    "{{trigger.metadata.name + '-suffix'}}",
			expected: []string{"trigger.metadata.name + '-suffix'"},
		},
		{
			name:     "multiple expressions",
			input:    "{{expr1}} and {{expr2}}",
			expected: []string{"expr1", "expr2"},
		},
		{
			name:     "expression with nested braces in strings",
			input:    `{{trigger.metadata.labels["key"] + "value with } brace"}}`,
			expected: []string{`trigger.metadata.labels["key"] + "value with } brace"`},
		},
		{
			name:     "expression with nested function calls",
			input:    "{{has(trigger.spec, 'nested.field') ? 'yes' : 'no'}}",
			expected: []string{"has(trigger.spec, 'nested.field') ? 'yes' : 'no'"},
		},
		{
			name:     "expression with map access",
			input:    "{{trigger.metadata.labels['environment']}}",
			expected: []string{"trigger.metadata.labels['environment']"},
		},
		{
			name:     "expression with both quote types",
			input:    `{{trigger.metadata.labels["key"] + trigger.annotations['other']}}`,
			expected: []string{`trigger.metadata.labels["key"] + trigger.annotations['other']`},
		},
		{
			name:     "expression with escaped quotes",
			input:    `{{"string with \"escaped\" quotes"}}`,
			expected: []string{`"string with \"escaped\" quotes"`},
		},
		{
			name:     "literal text with braces (not expressions)",
			input:    "This is {not an expression} and neither is {this}",
			expected: []string{},
		},
		{
			name:     "mixed literal and expressions",
			input:    "prefix {{expr1}} middle {not-expr} {{expr2}} suffix",
			expected: []string{"expr1", "expr2"},
		},
		{
			name:     "empty expression",
			input:    "{{}}",
			expected: []string{},
		},
		{
			name:     "whitespace-only expression",
			input:    "{{   }}",
			expected: []string{},
		},
		{
			name:     "expression with whitespace",
			input:    "{{  trigger.metadata.name  }}",
			expected: []string{"trigger.metadata.name"},
		},
		{
			name:     "malformed - missing closing brace",
			input:    "{{incomplete",
			expected: []string{},
		},
		{
			name:     "malformed - single closing brace",
			input:    "{{expr}",
			expected: []string{},
		},
		{
			name:     "expression with literal braces in strings",
			input:    `{{"{{literal}}"}}`,
			expected: []string{`"{{literal}}"`},
		},
		{
			name:     "complex nested expression",
			input:    "{{trigger.spec.resources[0].limits['memory'] > '1Gi' ? 'large' : 'small'}}",
			expected: []string{"trigger.spec.resources[0].limits['memory'] > '1Gi' ? 'large' : 'small'"},
		},
		{
			name:     "expression with array access",
			input:    "{{trigger.status.conditions[0].type}}",
			expected: []string{"trigger.status.conditions[0].type"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segments, err := templateutil.Split(tt.input)
			var result []string
			if err != nil {
				result = []string{}
			} else {
				for _, segment := range segments {
					if segment.Expression {
						result = append(result, segment.Value)
					}
				}
				if len(result) == 0 {
					result = []string{}
				}
			}
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("extractCELExpressions(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestValidateTemplateOrLiteral(t *testing.T) {
	// Helper to run validation and report whether errors were produced
	run := func(input string, allowEmpty bool) bool {
		errs := validateTemplateOrLiteral(input, claimTemplateAllowedVariables, allowEmpty, field.NewPath("field"))
		return len(errs) == 0
	}

	if !run("plain literal", false) {
		t.Fatalf("expected literal without templates to pass validation")
	}

	if run("", false) {
		t.Fatalf("expected empty literal to fail when allowEmpty=false")
	}

	if !run("", true) {
		t.Fatalf("expected empty literal to pass when allowEmpty=true")
	}

	if run("{{invalid..syntax}}", true) {
		t.Fatalf("expected invalid CEL expression to fail validation")
	}

	if !run("{{trigger.metadata.name}}", true) {
		t.Fatalf("expected valid CEL expression to pass validation")
	}
}

func TestCELExpressionParsingEdgeCases(t *testing.T) {

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "consecutive expressions",
			input:    "{{expr1}}{{expr2}}",
			expected: []string{"expr1", "expr2"},
		},
		{
			name:     "expression with complex string escaping",
			input:    `{{"test \"quote\" and 'apostrophe' and \\ backslash"}}`,
			expected: []string{`"test \"quote\" and 'apostrophe' and \\ backslash"`},
		},
		{
			name:     "expression with nested function and map access",
			input:    "{{size(trigger.metadata.labels) > 0 && has(trigger.metadata.labels, 'env')}}",
			expected: []string{"size(trigger.metadata.labels) > 0 && has(trigger.metadata.labels, 'env')"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "only literal text",
			input:    "no expressions here",
			expected: []string{},
		},
		{
			name:     "triple braces (not valid expression syntax)",
			input:    "{{{not valid}}}",
			expected: []string{},
		},
		{
			name:     "expression with unicode",
			input:    "{{trigger.metadata.labels['环境'] + 'ñoño'}}",
			expected: []string{"trigger.metadata.labels['环境'] + 'ñoño'"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segments, err := templateutil.Split(tt.input)
			var result []string
			if err != nil {
				result = []string{}
			} else {
				for _, segment := range segments {
					if segment.Expression {
						result = append(result, segment.Value)
					}
				}
				if len(result) == 0 {
					result = []string{}
				}
			}
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("extractCELExpressions(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestClaimTemplateValidatorCELParsing(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple expression with user variable",
			input:    "{{user.name + '-claim'}}",
			expected: []string{"user.name + '-claim'"},
		},
		{
			name:     "expression with requestInfo variable",
			input:    "{{requestInfo.verb + ':' + trigger.kind}}",
			expected: []string{"requestInfo.verb + ':' + trigger.kind"},
		},
		{
			name:     "multiple variables in one expression",
			input:    "{{trigger.metadata.name + '/' + user.name}}",
			expected: []string{"trigger.metadata.name + '/' + user.name"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segments, err := templateutil.Split(tt.input)
			var result []string
			if err != nil {
				result = []string{}
			} else {
				for _, segment := range segments {
					if segment.Expression {
						result = append(result, segment.Value)
					}
				}
				if len(result) == 0 {
					result = []string{}
				}
			}
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("extractCELExpressions(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
