package discovery

import (
	"context"
	"fmt"
	"sort"
	"sync"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// wildcardRule holds a policy rule where group or resource is "*".
type wildcardRule struct {
	policyName      string
	groupPattern    string
	resourcePattern string
	contexts        []ParentContext
}

// policyEntry stores contexts alongside the policy name that set them, used
// for exact GroupResource entries in the policy map.
type policyEntry struct {
	policyName string
	contexts   []ParentContext
}

// Registry is the source of truth for "which parent contexts is this resource
// visible in." It merges three inputs in descending precedence order:
//
//  1. Policy — DiscoveryContextPolicy objects watched live via a dynamic
//     informer. Highest precedence; overrides CRD annotations and static
//     registrations.
//
//  2. CRD annotations — for resources installed through the apiextensions API
//     (both Milo's bundled CRDs and any CRDs installed by external services
//     that build on Milo). Watched live via an informer.
//
//  3. Static registrations — for built-in/aggregated APIs (e.g. core/v1,
//     identity.miloapis.com sessions) that aren't backed by CRDs. Registered
//     once at apiserver startup with RegisterStatic.
//
// Resources with no registration in any source are treated as visible in all
// contexts, so existing CRDs and external CRDs that haven't adopted the marker
// continue to behave as before.
type Registry struct {
	mu              sync.RWMutex
	policy          map[schema.GroupResource]policyEntry
	policyWildcards []wildcardRule
	crd             map[schema.GroupResource][]ParentContext
	static          map[schema.GroupResource][]ParentContext
	hasInit         bool
	hasPolicyInit   bool
}

// NewRegistry creates an empty registry. Call RegisterStatic for any built-in
// APIs, then Run with an informer factory to populate the CRD-derived map.
func NewRegistry() *Registry {
	return &Registry{
		policy: map[schema.GroupResource]policyEntry{},
		crd:    map[schema.GroupResource][]ParentContext{},
		static: map[schema.GroupResource][]ParentContext{},
	}
}

// RegisterStatic records the parent contexts for a built-in or aggregated API
// that is not backed by a CRD. Safe to call before Run.
func (r *Registry) RegisterStatic(gr schema.GroupResource, contexts ...ParentContext) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(contexts) == 0 {
		delete(r.static, gr)
		return
	}
	r.static[gr] = append([]ParentContext(nil), contexts...)
}

// AllowedContexts returns the parent contexts a resource should be visible
// in, or nil if it should be visible everywhere. Precedence: policy exact
// match → policy wildcard match → crd → static.
func (r *Registry) AllowedContexts(gr schema.GroupResource) []ParentContext {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if entry, ok := r.policy[gr]; ok {
		return entry.contexts
	}

	if match := r.matchWildcard(gr); match != nil {
		return match
	}

	if v, ok := r.crd[gr]; ok {
		return v
	}

	if v, ok := r.static[gr]; ok {
		return v
	}

	return nil
}

// matchWildcard scans policyWildcards for a rule that covers gr. When multiple
// rules match, the one from the alphabetically first policy name wins.
// Must be called with r.mu held (at least RLock).
func (r *Registry) matchWildcard(gr schema.GroupResource) []ParentContext {
	var best *wildcardRule
	for i := range r.policyWildcards {
		rule := &r.policyWildcards[i]
		if !matchesPattern(rule.groupPattern, gr.Group) {
			continue
		}
		if !matchesPattern(rule.resourcePattern, gr.Resource) {
			continue
		}
		if best == nil || rule.policyName < best.policyName {
			best = rule
		}
	}
	if best == nil {
		return nil
	}
	return best.contexts
}

// IsVisible is a convenience wrapper combining AllowedContexts + Matches.
func (r *Registry) IsVisible(gr schema.GroupResource, current ParentContext) bool {
	return Matches(r.AllowedContexts(gr), current)
}

// HasSynced reports whether both the CRD informer and the policy informer have
// completed their initial list. Discovery filtering should fall open (visible)
// until this is true to avoid hiding resources during apiserver startup.
func (r *Registry) HasSynced() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hasInit && r.hasPolicyInit
}

// Run starts watching CRDs from the supplied informer factory. It blocks
// until ctx is cancelled. Caller must invoke factory.Start(...) separately
// (or use the same factory for other consumers).
func (r *Registry) Run(ctx context.Context, factory apiextensionsinformers.SharedInformerFactory) error {
	informer := factory.Apiextensions().V1().CustomResourceDefinitions().Informer()

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { r.upsertFromObj(obj) },
		UpdateFunc: func(_, obj any) { r.upsertFromObj(obj) },
		DeleteFunc: func(obj any) { r.deleteFromObj(obj) },
	})
	if err != nil {
		return fmt.Errorf("registering CRD event handler: %w", err)
	}

	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("CRD informer cache failed to sync")
	}

	r.mu.Lock()
	r.hasInit = true
	r.mu.Unlock()

	klog.InfoS("Discovery context registry synced", "crdEntries", len(r.crd))
	<-ctx.Done()
	return nil
}

func (r *Registry) upsertFromObj(obj any) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return
	}
	gr := schema.GroupResource{Group: crd.Spec.Group, Resource: crd.Spec.Names.Plural}
	contexts := ParseContexts(crd.Annotations[ParentContextsAnnotation])

	r.mu.Lock()
	defer r.mu.Unlock()
	if contexts == nil {
		// Wildcard / unset — drop any prior entry so lookup falls through
		// to "visible everywhere".
		delete(r.crd, gr)
		return
	}
	r.crd[gr] = contexts
}

func (r *Registry) deleteFromObj(obj any) {
	var crd *apiextensionsv1.CustomResourceDefinition
	switch v := obj.(type) {
	case *apiextensionsv1.CustomResourceDefinition:
		crd = v
	case cache.DeletedFinalStateUnknown:
		crd, _ = v.Obj.(*apiextensionsv1.CustomResourceDefinition)
	}
	if crd == nil {
		return
	}
	gr := schema.GroupResource{Group: crd.Spec.Group, Resource: crd.Spec.Names.Plural}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.crd, gr)
}

// upsertFromPolicy replaces all entries contributed by policyName with the
// rules in spec. Existing entries from other policies are unaffected.
func (r *Registry) upsertFromPolicy(policyName string, spec policySpec) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Remove all prior contributions from this policy.
	for gr, entry := range r.policy {
		if entry.policyName == policyName {
			delete(r.policy, gr)
		}
	}
	filtered := r.policyWildcards[:0]
	for _, rule := range r.policyWildcards {
		if rule.policyName != policyName {
			filtered = append(filtered, rule)
		}
	}
	r.policyWildcards = filtered

	for _, rule := range spec.rules {
		contexts := make([]ParentContext, len(rule.contexts))
		for i, c := range rule.contexts {
			contexts[i] = ParentContext(c)
		}

		isGroupWild := rule.group == "*"
		for _, resource := range rule.resources {
			isResourceWild := resource == "*"
			if isGroupWild || isResourceWild {
				r.policyWildcards = append(r.policyWildcards, wildcardRule{
					policyName:      policyName,
					groupPattern:    rule.group,
					resourcePattern: resource,
					contexts:        contexts,
				})
			} else {
				gr := schema.GroupResource{Group: rule.group, Resource: resource}
				existing, ok := r.policy[gr]
				if !ok || policyName < existing.policyName {
					r.policy[gr] = policyEntry{policyName: policyName, contexts: contexts}
				}
			}
		}
	}

	// Keep wildcards deterministically sorted for stable conflict resolution.
	sort.Slice(r.policyWildcards, func(i, j int) bool {
		return r.policyWildcards[i].policyName < r.policyWildcards[j].policyName
	})
}

// deleteFromPolicy removes all entries contributed by policyName.
func (r *Registry) deleteFromPolicy(policyName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for gr, entry := range r.policy {
		if entry.policyName == policyName {
			delete(r.policy, gr)
		}
	}
	filtered := r.policyWildcards[:0]
	for _, rule := range r.policyWildcards {
		if rule.policyName != policyName {
			filtered = append(filtered, rule)
		}
	}
	r.policyWildcards = filtered
}

// policySpec is an internal representation of DiscoveryContextPolicySpec that
// avoids importing the API types package into registry.go.
type policySpec struct {
	rules []policyRule
}

type policyRule struct {
	group     string
	resources []string
	contexts  []string
}
