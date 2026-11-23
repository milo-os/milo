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
//
// The rules fail closed. A CRD whose declaration is invalid (see Declare),
// a CRD whose cached copy lags storage, and every resource before the CRD
// cache syncs, is refused to every caller outside system:masters, rather
// than served without its rule.
package access

import (
	"context"
	"fmt"
	"sync"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// State says how to treat a request for a resource.
type State int

const (
	// Unrestricted means the resource has no rule: serve it as usual.
	Unrestricted State = iota
	// Enforced means apply the lookup's Rule.
	Enforced
	// Refused means refuse every caller outside system:masters with the
	// lookup's Err: the resource's rule is invalid or can't be trusted yet.
	Refused
)

// Lookup is how a request for one resource must be treated.
type Lookup struct {
	State State
	// Rule is the rule to apply when State is Enforced.
	Rule Rule
	// Err is the error to answer with when State is Refused.
	Err *apierrors.StatusError
}

// Rules holds the rule of every annotated CRD.
//
// It reads CRDs from the same informer cache the CRD handler serves custom
// resources from, at request time. A custom resource is only served once its
// CRD is in that cache, and then its rule is too, so no resource is served
// before its rule is known, however recently the CRD was written.
type Rules struct {
	mu      sync.RWMutex
	crds    cache.Store
	synced  func() bool
	decls   map[string]cachedDeclaration // by CRD name
	invalid map[schema.GroupResource][]string
	stale   map[string]bool // CRDs whose cached copy lags storage, by name
}

// cachedDeclaration is a CRD's declaration, read from that exact object. The
// cache replaces the object on every change, so a pointer match means the
// declaration is current.
type cachedDeclaration struct {
	crd  *apiextensionsv1.CustomResourceDefinition
	decl Declaration
}

// NewRules returns rules that aren't attached to a CRD informer yet, and so
// refuse everything until Attach.
func NewRules() *Rules {
	return &Rules{decls: map[string]cachedDeclaration{}, invalid: map[schema.GroupResource][]string{}}
}

// Attach reads the rules from the CRD informer in factory: the one the CRD
// handler serves custom resources from. The caller starts the factory.
func (r *Rules) Attach(factory apiextensionsinformers.SharedInformerFactory) error {
	informer := factory.Apiextensions().V1().CustomResourceDefinitions().Informer()
	// The handler only reports invalid declarations; requests read the cache.
	handler, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { r.observe(obj) },
		UpdateFunc: func(_, obj any) { r.observe(obj) },
		DeleteFunc: func(obj any) { r.forget(obj) },
	})
	if err != nil {
		return fmt.Errorf("registering CRD event handler: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.crds = informer.GetStore()
	r.synced = func() bool { return informer.HasSynced() && handler.HasSynced() }
	return nil
}

// HasSynced reports whether every CRD that existed at startup has been read.
func (r *Rules) HasSynced() bool {
	r.mu.RLock()
	synced := r.synced
	r.mu.RUnlock()
	return synced != nil && synced()
}

// Check reports whether the rules have loaded: the milo-access-rules
// readiness check. It doesn't fail while CRDs are stale: a lag shared by
// every replica would take them all out of rotation, built-in resources
// included. Lookup refuses the lagging CRDs instead.
func (r *Rules) Check() error {
	if !r.HasSynced() {
		return fmt.Errorf("access rules not synced")
	}
	return nil
}

// Lookup returns how to treat a request for gr.
func (r *Rules) Lookup(gr schema.GroupResource) Lookup {
	if !r.HasSynced() {
		// Until every CRD is read, any resource may be one with a rule.
		return refused(apierrors.NewServiceUnavailable(
			"the API server is starting: access rules for custom resources aren't loaded yet"))
	}
	r.mu.RLock()
	store := r.crds
	r.mu.RUnlock()

	// A CRD's name is always <plural>.<group>.
	obj, exists, err := store.GetByKey(gr.Resource + "." + gr.Group)
	if err != nil {
		return refused(apierrors.NewServiceUnavailable(fmt.Sprintf("reading the access rules of %s: %v", gr, err)))
	}
	if !exists {
		// Not a custom resource, or one the CRD handler doesn't serve either.
		return Lookup{State: Unrestricted}
	}
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return refused(apierrors.NewServiceUnavailable(fmt.Sprintf("reading the access rules of %s: unexpected %T", gr, obj)))
	}
	r.mu.RLock()
	stale := r.stale[crd.Name]
	r.mu.RUnlock()
	if stale {
		return refused(apierrors.NewServiceUnavailable(fmt.Sprintf(
			"access rules for %s may be out of date: this API server's copy of CustomResourceDefinition %s "+
				"has not caught up with storage", gr, crd.Name)))
	}
	d := r.declaration(crd)
	if !d.Valid() {
		return refused(apierrors.NewServiceUnavailable(fmt.Sprintf(
			"%s can't be served: CustomResourceDefinition %s declares access rules Milo can't apply: %s. "+
				"Fix its annotations; until then only system:masters may use it", gr, crd.Name, d.Error())))
	}
	if d.Rule.IsZero() {
		return Lookup{State: Unrestricted}
	}
	return Lookup{State: Enforced, Rule: d.Rule}
}

func refused(err *apierrors.StatusError) Lookup { return Lookup{State: Refused, Err: err} }

// declaration returns crd's declaration, read once per cached object.
func (r *Rules) declaration(crd *apiextensionsv1.CustomResourceDefinition) Declaration {
	r.mu.RLock()
	c, ok := r.decls[crd.Name]
	r.mu.RUnlock()
	if ok && c.crd == crd {
		return c.decl
	}
	d := Declare(crd)
	r.mu.Lock()
	r.decls[crd.Name] = cachedDeclaration{crd: crd, decl: d}
	r.mu.Unlock()
	return d
}

// observe reports a CRD's invalid declaration, as a metric and, when it
// changes, a log line.
func (r *Rules) observe(obj any) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return
	}
	d := r.declaration(crd)
	if r.report(groupResource(crd), d) {
		for _, p := range d.Problems {
			klog.ErrorS(nil, "CRD declares an access rule Milo can't apply; requests for it are refused",
				"crd", crd.Name, "annotation", p.Annotation, "reason", p.Reason, "problem", p.Message)
		}
	}
}

func (r *Rules) forget(obj any) {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return
	}
	r.mu.Lock()
	delete(r.decls, crd.Name)
	r.mu.Unlock()
	r.report(groupResource(crd), Declaration{})
}

// report sets the invalid-declaration metric for gr, and reports whether its
// problems changed.
func (r *Rules) report(gr schema.GroupResource, d Declaration) bool {
	var reasons []string
	for _, p := range d.Problems {
		reasons = append(reasons, p.Reason)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.invalid[gr]
	if equalStrings(prev, reasons) {
		return false
	}
	for _, reason := range prev {
		invalidDeclarations.DeleteLabelValues(gr.Group, gr.Resource, reason)
	}
	for _, reason := range reasons {
		invalidDeclarations.WithLabelValues(gr.Group, gr.Resource, reason).Set(1)
	}
	if len(reasons) == 0 {
		delete(r.invalid, gr)
	} else {
		r.invalid[gr] = reasons
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ListCRDVersions returns the resource version of every CRD in storage, by
// name.
type ListCRDVersions func(ctx context.Context) (map[string]string, error)

// Run checks every interval that the CRD cache keeps up with storage, until
// ctx is cancelled. When a CRD in storage differs from the cache and its
// cached copy hasn't moved after grace, that CRD is stale: its resources are
// refused until the cache catches up. Other resources are unaffected.
func (r *Rules) Run(ctx context.Context, list ListCRDVersions, interval, grace time.Duration) {
	logged := false
	var lagging map[string]string // name -> cached resource version when the lag was seen
	for {
		wait := interval
		if len(lagging) > 0 {
			wait = grace
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if !r.HasSynced() {
			continue
		}
		if !logged {
			klog.InfoS("Access rules synced")
			logged = true
		}
		lagging = r.checkStaleness(ctx, list, lagging)
	}
}

// checkStaleness compares the cache with storage once. lagging is what the
// previous check found; it returns what this one found.
func (r *Rules) checkStaleness(ctx context.Context, list ListCRDVersions, lagging map[string]string) map[string]string {
	live, err := list(ctx)
	if err != nil {
		// Without storage nothing is served anyway; keep the last verdict.
		klog.V(2).InfoS("Checking the CRD cache against storage failed", "err", err)
		return lagging
	}
	r.mu.RLock()
	store := r.crds
	r.mu.RUnlock()
	cached := map[string]string{}
	for _, obj := range store.List() {
		if crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition); ok {
			cached[crd.Name] = crd.ResourceVersion
		}
	}

	found := map[string]string{}
	for name, rv := range live {
		if cached[name] != rv {
			found[name] = cached[name]
		}
	}
	for name, rv := range cached {
		if _, ok := live[name]; !ok {
			found[name] = rv
		}
	}

	// A CRD that lagged last time and still has the same cached version
	// hasn't moved for a whole grace period.
	stuck := map[string]bool{}
	for name, rv := range found {
		if prev, ok := lagging[name]; ok && prev == rv {
			stuck[name] = true
		}
	}
	r.setStale(stuck)
	return found
}

// setStale records which CRDs are stale, logging each change.
func (r *Rules) setStale(stuck map[string]bool) {
	r.mu.Lock()
	was := r.stale
	r.stale = stuck
	r.mu.Unlock()
	staleCache.Set(float64(len(stuck)))
	for name := range stuck {
		if !was[name] {
			klog.ErrorS(nil, "Access rules for a CRD are stale; refusing its resources outside system:masters until the CRD cache catches up",
				"crd", name)
		}
	}
	for name := range was {
		if !stuck[name] {
			klog.InfoS("Access rules for a CRD caught up with storage", "crd", name)
		}
	}
}

func groupResource(crd *apiextensionsv1.CustomResourceDefinition) schema.GroupResource {
	return schema.GroupResource{Group: crd.Spec.Group, Resource: crd.Spec.Names.Plural}
}
