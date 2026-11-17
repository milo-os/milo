package access

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// Annotations a CRD sets to limit which of its objects callers see.
const (
	annotationPrefix = "iam.miloapis.com/"

	OwnerFieldAnnotation        = "iam.miloapis.com/owner-field"
	HiddenFieldAnnotation       = "iam.miloapis.com/hidden-field"
	HiddenSubresourceAnnotation = "iam.miloapis.com/hidden-subresource"

	// DefaultHiddenSubresource is the subresource whose get permission lets a
	// caller see hidden objects, when the CRD doesn't name one.
	DefaultHiddenSubresource = "hidden"
)

// Reasons a declaration is invalid. They label the milo_access_rule_invalid
// metric, so they stay short and stable.
const (
	ReasonBlank              = "blank_annotation"
	ReasonUnknownAnnotation  = "unknown_annotation"
	ReasonInvalidPath        = "invalid_field_path"
	ReasonNotSelectable      = "not_selectable"
	ReasonOwnerUnderStatus   = "owner_under_status"
	ReasonOwnerInScalePath   = "owner_in_scale_path"
	ReasonSubresourceNoField = "subresource_without_hidden_field"
)

// Rule is what a CRD's annotations ask for. A zero field means no rule.
type Rule struct {
	// OwnerField is the field selector path of the owner's user ID.
	OwnerField string
	// HiddenField is the field selector path of a boolean that hides the
	// object.
	HiddenField string
	// HiddenSubresource is the subresource a caller needs get on to see
	// hidden objects.
	HiddenSubresource string
}

// IsZero reports whether the rule restricts nothing.
func (r Rule) IsZero() bool { return r.OwnerField == "" && r.HiddenField == "" }

// Problem is one reason a CRD's access-rule declaration can't be applied.
type Problem struct {
	// Annotation is the annotation at fault.
	Annotation string
	// Reason is one of the Reason constants.
	Reason string
	// Message says what is wrong and how to fix it.
	Message string
}

// Declaration is what a CRD's iam.miloapis.com/ annotations declare: a rule,
// or the problems that stop Milo applying it. Milo refuses requests for a
// resource whose declaration has problems rather than serve it without the
// rule its author meant.
type Declaration struct {
	Rule     Rule
	Problems []Problem
}

// Valid reports whether the declaration has no problems.
func (d Declaration) Valid() bool { return len(d.Problems) == 0 }

// Error describes the problems in one line.
func (d Declaration) Error() string {
	msgs := make([]string, len(d.Problems))
	for i, p := range d.Problems {
		msgs[i] = p.Message
	}
	return strings.Join(msgs, "; ")
}

// simplePath is a dotted path of plain field names, which both storage's
// field selectors and Field read the same way. Bracketed or quoted JSONPath
// steps aren't, so they're refused.
var simplePath = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$-]*(\.[A-Za-z_$][A-Za-z0-9_$-]*)*$`)

// Declare reads crd's access-rule declaration. Every iam.miloapis.com/
// annotation must be one Milo knows and must make sense, or the declaration
// is invalid:
//
//   - owner-field and hidden-field must name a simple dotted path that is a
//     selectableField of every served version, so storage can filter on it;
//   - the owner field can't be under status, nor overlap the scale
//     subresource's paths: writes to those subresources skip the owner check;
//   - hidden-subresource needs hidden-field;
//   - no annotation may be blank, and no other iam.miloapis.com/ key is
//     allowed, so a misspelt one can't silently turn a rule off.
func Declare(crd *apiextensionsv1.CustomResourceDefinition) Declaration {
	var d Declaration
	add := func(annotation, reason, format string, args ...any) {
		d.Problems = append(d.Problems, Problem{Annotation: annotation, Reason: reason, Message: fmt.Sprintf(format, args...)})
	}
	a := crd.Annotations

	field := func(key string) string {
		v, ok := a[key]
		if !ok {
			return ""
		}
		path := fieldPath(v)
		switch {
		case path == "":
			add(key, ReasonBlank, "%s is blank; set it to a field path such as spec.userRef.name, or remove it", key)
			return ""
		case !simplePath.MatchString(path):
			add(key, ReasonInvalidPath, "%s %q is not a dotted field path such as spec.userRef.name", key, v)
			return ""
		case !selectable(crd, path):
			add(key, ReasonNotSelectable, "%s %q is not a selectableField of every served version, so storage can't filter on it", key, path)
		}
		return path
	}
	d.Rule.OwnerField = field(OwnerFieldAnnotation)
	d.Rule.HiddenField = field(HiddenFieldAnnotation)

	if owner := d.Rule.OwnerField; owner != "" {
		if overlaps(owner, "status") {
			add(OwnerFieldAnnotation, ReasonOwnerUnderStatus,
				"%s %q is under status, which the status subresource writes without the owner check", OwnerFieldAnnotation, owner)
		}
		for _, p := range scalePaths(crd) {
			if overlaps(owner, p) {
				add(OwnerFieldAnnotation, ReasonOwnerInScalePath,
					"%s %q overlaps the scale subresource's path %q, which writes it without the owner check", OwnerFieldAnnotation, owner, p)
				break
			}
		}
	}

	if v, ok := a[HiddenSubresourceAnnotation]; ok {
		switch {
		case strings.TrimSpace(v) == "":
			add(HiddenSubresourceAnnotation, ReasonBlank, "%s is blank; set it to a subresource name, or remove it to use %q", HiddenSubresourceAnnotation, DefaultHiddenSubresource)
		case d.Rule.HiddenField == "" && a[HiddenFieldAnnotation] == "":
			add(HiddenSubresourceAnnotation, ReasonSubresourceNoField, "%s has no effect without %s", HiddenSubresourceAnnotation, HiddenFieldAnnotation)
		}
	}
	if d.Rule.HiddenField != "" {
		d.Rule.HiddenSubresource = strings.TrimSpace(a[HiddenSubresourceAnnotation])
		if d.Rule.HiddenSubresource == "" {
			d.Rule.HiddenSubresource = DefaultHiddenSubresource
		}
	}

	for k := range a {
		switch k {
		case OwnerFieldAnnotation, HiddenFieldAnnotation, HiddenSubresourceAnnotation:
		default:
			if strings.HasPrefix(k, annotationPrefix) {
				add(k, ReasonUnknownAnnotation, "%s is not an access rule annotation; known ones are %s, %s and %s",
					k, OwnerFieldAnnotation, HiddenFieldAnnotation, HiddenSubresourceAnnotation)
			}
		}
	}

	sort.Slice(d.Problems, func(i, j int) bool { return d.Problems[i].Message < d.Problems[j].Message })
	return d
}

// fieldPath normalises "spec.x" and ".spec.x" to "spec.x".
func fieldPath(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), ".")
}

// overlaps reports whether one dotted path is the other or lies under it.
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+".") || strings.HasPrefix(b, a+".")
}

// selectable reports whether field is a selectable field of every served
// version.
func selectable(crd *apiextensionsv1.CustomResourceDefinition, field string) bool {
	for _, v := range crd.Spec.Versions {
		if !v.Served {
			continue
		}
		found := false
		for _, sf := range v.SelectableFields {
			if fieldPath(sf.JSONPath) == field {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// scalePaths returns the paths the scale subresource of any version reads or
// writes.
func scalePaths(crd *apiextensionsv1.CustomResourceDefinition) []string {
	var paths []string
	for _, v := range crd.Spec.Versions {
		if v.Subresources == nil || v.Subresources.Scale == nil {
			continue
		}
		s := v.Subresources.Scale
		paths = append(paths, fieldPath(s.SpecReplicasPath), fieldPath(s.StatusReplicasPath))
		if s.LabelSelectorPath != nil {
			paths = append(paths, fieldPath(*s.LabelSelectorPath))
		}
	}
	return paths
}
