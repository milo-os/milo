// Package pendingwire is a source-pinned pure wire check, not source eligibility
// or authorization. Apps 71d7bf5 internal/appcontract is the conformance contract.
package pendingwire

import (
	"encoding/json"
	"errors"
	"k8s.io/apimachinery/pkg/util/validation"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxInputsBytes = 256 << 10

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var errSpec = errors.New("invalid pending App spec")

func Identity(s string) bool {
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && strings.TrimSpace(s) == s && strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}
func exact(m map[string]interface{}, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// ValidateSpec returns the complete canonical spec digest. Exact JSON inputs
// remain a string: no float conversion, API reads, or template assertion occurs.
func ValidateSpec(spec map[string]interface{}, projectUID string) (string, error) {
	if !Identity(projectUID) || !exact(spec, "templateRef", "inputsJSON", "inputsDigest", "uninstallPolicy") {
		return "", errSpec
	}
	ref, ok := spec["templateRef"].(map[string]interface{})
	if !ok || !exact(ref, "projectRevision") {
		return "", errSpec
	}
	pin, ok := ref["projectRevision"].(map[string]interface{})
	if !ok || !exact(pin, "projectUID", "templateName", "templateUID", "revisionName", "revisionUID", "digest") {
		return "", errSpec
	}
	for k, v := range pin {
		s, ok := v.(string)
		if !ok || !Identity(s) {
			return "", errSpec
		}
		switch k {
		case "projectUID":
			if s != projectUID {
				return "", errSpec
			}
		case "templateName", "revisionName":
			if len(validation.IsDNS1123Subdomain(s)) != 0 {
				return "", errSpec
			}
		case "digest":
			if !digestRE.MatchString(s) {
				return "", errSpec
			}
		}
	}
	inputs, ok := spec["inputsJSON"].(string)
	if !ok || len(inputs) > MaxInputsBytes {
		return "", errSpec
	}
	digest, ok := spec["inputsDigest"].(string)
	if !ok || !digestRE.MatchString(digest) {
		return "", errSpec
	}
	policy, ok := spec["uninstallPolicy"].(string)
	if !ok || (policy != "Delete" && policy != "Retain") {
		return "", errSpec
	}
	parsed, err := Parse([]byte(inputs))
	if err != nil || string(parsed.CanonicalJSON()) != inputs || parsed.Digest() != digest {
		return "", errSpec
	}
	// All fields and bounds were checked before allocation/canonicalization.
	raw, err := json.Marshal(spec)
	if err != nil {
		return "", errSpec
	}
	complete, err := Parse(raw)
	if err != nil {
		return "", errSpec
	}
	return complete.Digest(), nil
}
