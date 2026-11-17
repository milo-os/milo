package access

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"

	milorequest "go.miloapis.com/milo/pkg/request"
	"go.miloapis.com/milo/pkg/server/filters"
)

// Filter enforces the rules. It belongs inside authorization in the handler
// chain: it needs the authenticated user and only narrows what an
// authorized request returns.
type Filter struct {
	Rules      *Rules
	Authorizer authorizer.Authorizer
	// Client reads stored objects for named requests. It must bypass this
	// filter, which a loopback client with full access does.
	Client     dynamic.Interface
	Serializer runtime.NegotiatedSerializer
}

// Wrap returns next with the rules applied.
func (f *Filter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		info, ok := request.RequestInfoFrom(ctx)
		if !ok || !info.IsResourceRequest {
			next.ServeHTTP(w, req)
			return
		}
		if _, inProject := milorequest.ProjectID(ctx); inProject {
			next.ServeHTTP(w, req)
			return
		}
		gr := schema.GroupResource{Group: info.APIGroup, Resource: info.Resource}
		gv := schema.GroupVersion{Group: info.APIGroup, Version: info.APIVersion}
		userID, inUser := filters.UserID(ctx)

		if !f.Rules.HasSynced() {
			// Rules may be missing until the informer syncs; readiness keeps
			// traffic away, and user-context requests fail closed meanwhile.
			if inUser {
				responsewriters.ErrorNegotiated(apierrors.NewServiceUnavailable("the API server is starting"), f.Serializer, gv, w, req)
				return
			}
			next.ServeHTTP(w, req)
			return
		}
		rule, ok := f.Rules.Get(gr)
		if !ok {
			next.ServeHTTP(w, req)
			return
		}

		c := constraint{}
		if rule.OwnerField != "" && inUser {
			c.ownerField, c.owner = rule.OwnerField, userID
		}
		if rule.HiddenField != "" {
			if !f.canSeeHidden(ctx, info, rule) {
				c.hiddenField = rule.HiddenField
			}
		}
		if c.none() {
			next.ServeHTTP(w, req)
			return
		}

		switch {
		case info.Verb == "list" || info.Verb == "watch" || info.Verb == "deletecollection":
			sel, err := c.selector(req.URL.Query().Get("fieldSelector"))
			if err != nil {
				responsewriters.ErrorNegotiated(apierrors.NewBadRequest(err.Error()), f.Serializer, gv, w, req)
				return
			}
			q := req.URL.Query()
			q.Set("fieldSelector", sel)
			u := *req.URL
			u.RawQuery = q.Encode()
			req = req.Clone(ctx)
			req.URL = &u
			info.FieldSelector = sel
			req = req.WithContext(request.WithRequestInfo(ctx, info))
		case info.Name != "" && info.Verb != "create":
			allowed, err := f.allowsNamed(ctx, info, c)
			if err != nil {
				responsewriters.ErrorNegotiated(apierrors.NewInternalError(err), f.Serializer, gv, w, req)
				return
			}
			if !allowed {
				responsewriters.ErrorNegotiated(apierrors.NewNotFound(gr, info.Name), f.Serializer, gv, w, req)
				return
			}
		}
		next.ServeHTTP(w, req)
	})
}

// canSeeHidden reports whether the caller may get the rule's hidden
// subresource.
func (f *Filter) canSeeHidden(ctx context.Context, info *request.RequestInfo, rule Rule) bool {
	u, ok := request.UserFrom(ctx)
	if !ok {
		return false
	}
	decision, _, err := f.Authorizer.Authorize(ctx, authorizer.AttributesRecord{
		User:            u,
		Verb:            "get",
		APIGroup:        info.APIGroup,
		APIVersion:      info.APIVersion,
		Resource:        info.Resource,
		Subresource:     rule.HiddenSubresource,
		Namespace:       info.Namespace,
		ResourceRequest: true,
	})
	if err != nil && decision != authorizer.DecisionAllow {
		// An authorizer error with no decision denies, like the authorization
		// filter; log it so a broken webhook isn't silent.
		klog.ErrorS(err, "Checking access to hidden objects failed; treating them as hidden", "resource", info.Resource)
	}
	return decision == authorizer.DecisionAllow
}

// allowsNamed reads the stored object and checks it against c. An object
// that doesn't exist is allowed through, so the handler answers as usual.
func (f *Filter) allowsNamed(ctx context.Context, info *request.RequestInfo, c constraint) (bool, error) {
	gvr := schema.GroupVersionResource{Group: info.APIGroup, Version: info.APIVersion, Resource: info.Resource}
	var ri dynamic.ResourceInterface = f.Client.Resource(gvr)
	if info.Namespace != "" {
		ri = f.Client.Resource(gvr).Namespace(info.Namespace)
	}
	obj, err := ri.Get(ctx, info.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading %s %q to check access: %w", info.Resource, info.Name, err)
	}
	return c.allows(obj), nil
}

// constraint is what a rule asks of one request.
type constraint struct {
	ownerField, owner string
	hiddenField       string
}

func (c constraint) none() bool { return c.ownerField == "" && c.hiddenField == "" }

// selector returns the client's field selector with the constraint applied.
// Terms the client sent on the constrained fields are dropped, so a client
// can't widen what it sees; every other term keeps its operator.
func (c constraint) selector(clientSelector string) (string, error) {
	sel, err := fields.ParseSelector(clientSelector)
	if err != nil {
		return "", fmt.Errorf("invalid field selector: %w", err)
	}
	var terms []fields.Selector
	for _, r := range sel.Requirements() {
		if r.Field == c.ownerField || r.Field == c.hiddenField {
			continue
		}
		switch r.Operator {
		case selection.NotEquals:
			terms = append(terms, fields.OneTermNotEqualSelector(r.Field, r.Value))
		case selection.Equals, selection.DoubleEquals:
			terms = append(terms, fields.OneTermEqualSelector(r.Field, r.Value))
		default:
			return "", fmt.Errorf("unsupported field selector operator %q", r.Operator)
		}
	}
	if c.ownerField != "" {
		terms = append(terms, fields.OneTermEqualSelector(c.ownerField, c.owner))
	}
	if c.hiddenField != "" {
		terms = append(terms, fields.OneTermNotEqualSelector(c.hiddenField, "true"))
	}
	return fields.AndSelectors(terms...).String(), nil
}

// allows reports whether obj passes the constraint.
func (c constraint) allows(obj *unstructured.Unstructured) bool {
	if c.ownerField != "" {
		v, ok := field(obj, c.ownerField)
		if !ok || fmt.Sprint(v) != c.owner {
			return false
		}
	}
	if c.hiddenField != "" {
		if v, ok := field(obj, c.hiddenField); ok && (v == true || v == "true") {
			return false
		}
	}
	return true
}

func field(obj *unstructured.Unstructured, path string) (any, bool) {
	v, ok, err := unstructured.NestedFieldNoCopy(obj.Object, strings.Split(path, ".")...)
	if err != nil || !ok {
		return nil, false
	}
	return v, true
}
