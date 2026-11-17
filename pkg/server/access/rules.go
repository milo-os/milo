// Package access limits which objects of a resource a caller sees, for
// resources that ask for it with annotations on their CRD.
//
// Milo keeps resources a user owns in one collection for the whole
// platform: only projects get their own storage. Authorization decides
// whether a user may list a resource in their own context, but not which
// objects come back. A CRD closes that gap by naming the field that holds
// the owner, or the field that hides an object, and Milo applies it to
// every request for that resource:
//
//   - iam.miloapis.com/owner-field (e.g. "spec.userRef.name"): in the user
//     and organization contexts, lists and watches only return objects owned
//     by the user (see RequestOwner), and named requests for another user's
//     object answer NotFound. The AccessRuleOwnerEnforcement admission plugin
//     checks the field on writes.
//   - iam.miloapis.com/hidden-field (e.g. "spec.hidden"): lists and watches
//     leave out objects whose field is true, and named requests for them
//     answer NotFound, unless the caller may get the subresource named by
//     iam.miloapis.com/hidden-subresource ("hidden" by default).
//
// Both fields must be selectableFields of the CRD, so storage can filter on
// them. Rules apply to platform storage only; requests in a project context
// pass through unchanged.
package access

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
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

// Rules holds the rule of every annotated CRD, kept current from a CRD
// informer.
type Rules struct {
	mu     sync.RWMutex
	rules  map[schema.GroupResource]Rule
	synced bool
}

// NewRules returns an empty set of rules.
func NewRules() *Rules {
	return &Rules{rules: map[schema.GroupResource]Rule{}}
}

// Get returns the rule for gr, if it has one.
func (r *Rules) Get(gr schema.GroupResource) (Rule, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, ok := r.rules[gr]
	return rule, ok
}

// HasSynced reports whether every CRD that existed at startup has been read.
// Until then a resource's rule may be missing, so the server must not take
// traffic.
func (r *Rules) HasSynced() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.synced
}

// Set records gr's rule, or forgets it when the rule is zero.
func (r *Rules) Set(gr schema.GroupResource, rule Rule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rule.IsZero() {
		delete(r.rules, gr)
		return
	}
	r.rules[gr] = rule
}

// Run keeps the rules current from the CRD informer in factory until ctx is
// cancelled. The caller starts the factory.
func (r *Rules) Run(ctx context.Context, factory apiextensionsinformers.SharedInformerFactory) error {
	informer := factory.Apiextensions().V1().CustomResourceDefinitions().Informer()
	handler, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { r.upsert(obj) },
		UpdateFunc: func(_, obj any) { r.upsert(obj) },
		DeleteFunc: func(obj any) { r.remove(obj) },
	})
	if err != nil {
		return fmt.Errorf("registering CRD event handler: %w", err)
	}
	if !cache.WaitForCacheSync(ctx.Done(), handler.HasSynced) {
		return fmt.Errorf("CRD informer cache failed to sync")
	}
	r.mu.Lock()
	r.synced = true
	n := len(r.rules)
	r.mu.Unlock()
	klog.InfoS("Access rules synced", "resources", n)
	<-ctx.Done()
	return nil
}

func (r *Rules) upsert(obj any) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return
	}
	rule := RuleFor(crd)
	for _, msg := range AnnotationProblems(crd) {
		klog.ErrorS(nil, "Access rule annotation on CRD has no effect", "crd", crd.Name, "problem", msg)
	}
	for _, f := range []string{rule.OwnerField, rule.HiddenField} {
		if f != "" && !selectable(crd, f) {
			// Lists will fail with a field selector error, which is safe; named
			// requests are still checked.
			klog.ErrorS(nil, "Access rule field is not a selectable field of the CRD; lists of it will fail",
				"crd", crd.Name, "field", f)
		}
	}
	r.Set(groupResource(crd), rule)
}

func (r *Rules) remove(obj any) {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	if crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition); ok {
		r.Set(groupResource(crd), Rule{})
	}
}

// RuleFor reads a CRD's rule from its annotations.
func RuleFor(crd *apiextensionsv1.CustomResourceDefinition) Rule {
	a := crd.Annotations
	rule := Rule{
		OwnerField:  fieldPath(a[OwnerFieldAnnotation]),
		HiddenField: fieldPath(a[HiddenFieldAnnotation]),
	}
	if rule.HiddenField != "" {
		rule.HiddenSubresource = strings.TrimSpace(a[HiddenSubresourceAnnotation])
		if rule.HiddenSubresource == "" {
			rule.HiddenSubresource = DefaultHiddenSubresource
		}
	}
	return rule
}

// AnnotationProblems lists annotations on crd that look like access rules
// but don't make one: a rule annotation whose value is blank, or another
// iam.miloapis.com/ key, such as a misspelt one. Either way the CRD's objects
// aren't restricted the way its author meant.
func AnnotationProblems(crd *apiextensionsv1.CustomResourceDefinition) []string {
	var problems []string
	for k, v := range crd.Annotations {
		switch k {
		case OwnerFieldAnnotation, HiddenFieldAnnotation:
			if fieldPath(v) == "" {
				problems = append(problems, fmt.Sprintf("%s is blank, so the CRD has no such rule", k))
			}
		case HiddenSubresourceAnnotation:
			if fieldPath(crd.Annotations[HiddenFieldAnnotation]) == "" {
				problems = append(problems, fmt.Sprintf("%s has no effect without %s", k, HiddenFieldAnnotation))
			}
		default:
			if strings.HasPrefix(k, annotationPrefix) {
				problems = append(problems, fmt.Sprintf("%s is not an access rule annotation; known ones are %s, %s and %s",
					k, OwnerFieldAnnotation, HiddenFieldAnnotation, HiddenSubresourceAnnotation))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

// fieldPath normalises "spec.x" and ".spec.x" to "spec.x".
func fieldPath(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), ".")
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

func groupResource(crd *apiextensionsv1.CustomResourceDefinition) schema.GroupResource {
	return schema.GroupResource{Group: crd.Spec.Group, Resource: crd.Spec.Names.Plural}
}
