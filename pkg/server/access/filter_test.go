package access

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	"k8s.io/apiserver/pkg/endpoints/request"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"

	milorequest "go.miloapis.com/milo/pkg/request"
	"go.miloapis.com/milo/pkg/server/filters"
)

const group = "feedback.miloapis.com"

var (
	votes = schema.GroupVersionResource{Group: group, Version: "v1alpha1", Resource: "votes"}
	posts = schema.GroupVersionResource{Group: group, Version: "v1alpha1", Resource: "posts"}
)

func object(kind, name string, fields map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": group + "/v1alpha1",
		"kind":       kind,
		"metadata":   map[string]any{"name": name},
	}}
	for k, v := range fields {
		u.Object[k] = v
	}
	return u
}

func newFilter(t *testing.T, canSeeHidden bool) *Filter {
	t.Helper()
	rules := NewRules()
	rules.Set(votes.GroupResource(), Rule{OwnerField: "spec.userRef.name"})
	rules.Set(posts.GroupResource(), Rule{HiddenField: "spec.hidden", HiddenSubresource: "hidden"})
	rules.synced = true
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{votes: "VoteList", posts: "PostList"},
		object("Vote", "mine", map[string]any{"spec": map[string]any{"userRef": map[string]any{"name": "u1"}}}),
		object("Vote", "theirs", map[string]any{"spec": map[string]any{"userRef": map[string]any{"name": "u2"}}}),
		object("Post", "shown", map[string]any{"spec": map[string]any{"hidden": false}}),
		object("Post", "hidden", map[string]any{"spec": map[string]any{"hidden": true}}),
	)
	return &Filter{
		Rules:  rules,
		Client: client,
		Authorizer: authorizer.AuthorizerFunc(func(_ context.Context, a authorizer.Attributes) (authorizer.Decision, string, error) {
			if canSeeHidden && a.GetResource() == "posts" && a.GetSubresource() == "hidden" && a.GetVerb() == "get" {
				return authorizer.DecisionAllow, "", nil
			}
			return authorizer.DecisionNoOpinion, "", nil
		}),
		Serializer: scheme.Codecs,
	}
}

type call struct {
	served   bool
	selector string
	infoSel  string
}

// serve sends a request through the filter and reports what reached the
// handler.
func serve(t *testing.T, f *Filter, gvr schema.GroupVersionResource, verb, name, selector string, ctxFn func(context.Context) context.Context) (call, int) {
	t.Helper()
	path := "/apis/" + gvr.Group + "/" + gvr.Version + "/" + gvr.Resource
	if name != "" {
		path += "/" + name
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if selector != "" {
		req.URL.RawQuery = "fieldSelector=" + selector
	}
	ctx := request.WithRequestInfo(req.Context(), &request.RequestInfo{
		IsResourceRequest: true, Verb: verb, APIGroup: gvr.Group, APIVersion: gvr.Version,
		Resource: gvr.Resource, Name: name,
	})
	ctx = request.WithUser(ctx, &user.DefaultInfo{Name: "someone"})
	if ctxFn != nil {
		ctx = ctxFn(ctx)
	}
	req = req.WithContext(ctx)

	var got call
	rec := httptest.NewRecorder()
	f.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.served = true
		got.selector = r.URL.Query().Get("fieldSelector")
		if info, ok := request.RequestInfoFrom(r.Context()); ok {
			got.infoSel = info.FieldSelector
		}
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return got, rec.Code
}

func asUser(id string) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		return context.WithValue(ctx, filters.UserIDContextKey, id) //nolint:staticcheck // the key Milo's filters use
	}
}

func TestOwnerListIsLimitedToTheUser(t *testing.T) {
	f := newFilter(t, false)
	got, code := serve(t, f, votes, "list", "", "spec.userRef.name=u2,spec.postRef.name!=p1", asUser("u1"))
	if code != http.StatusOK || !got.served {
		t.Fatalf("code %d served %v", code, got.served)
	}
	want := "spec.postRef.name!=p1,spec.userRef.name=u1"
	if got.selector != want || got.infoSel != want {
		t.Errorf("selector = %q (request info %q), want %q", got.selector, got.infoSel, want)
	}

	got, _ = serve(t, f, votes, "watch", "", "", asUser("u1"))
	if got.selector != "spec.userRef.name=u1" {
		t.Errorf("watch selector = %q", got.selector)
	}
}

func TestOwnerRuleOnlyAppliesInTheUserContext(t *testing.T) {
	f := newFilter(t, false)
	got, _ := serve(t, f, votes, "list", "", "spec.postRef.name=p1", nil)
	if got.selector != "spec.postRef.name=p1" {
		t.Errorf("platform-context selector changed to %q", got.selector)
	}
	if _, code := serve(t, f, votes, "get", "theirs", "", nil); code != http.StatusOK {
		t.Errorf("platform-context get = %d", code)
	}
}

func TestOwnerNamedRequests(t *testing.T) {
	f := newFilter(t, false)
	for _, tc := range []struct {
		verb, name string
		want       int
	}{
		{"get", "mine", http.StatusOK},
		{"get", "theirs", http.StatusNotFound},
		{"delete", "theirs", http.StatusNotFound},
		{"patch", "theirs", http.StatusNotFound},
		{"update", "theirs", http.StatusNotFound},
		{"get", "missing", http.StatusOK}, // the handler answers NotFound itself
	} {
		got, code := serve(t, f, votes, tc.verb, tc.name, "", asUser("u1"))
		if code != tc.want || got.served != (tc.want == http.StatusOK) {
			t.Errorf("%s %s: code %d served %v, want %d", tc.verb, tc.name, code, got.served, tc.want)
		}
	}
}

func TestHiddenObjects(t *testing.T) {
	f := newFilter(t, false)
	got, _ := serve(t, f, posts, "list", "", "spec.hidden=true,metadata.name=x", nil)
	if want := "metadata.name=x,spec.hidden!=true"; got.selector != want {
		t.Errorf("selector = %q, want %q", got.selector, want)
	}
	if _, code := serve(t, f, posts, "get", "hidden", "", nil); code != http.StatusNotFound {
		t.Errorf("get hidden = %d, want 404", code)
	}
	if _, code := serve(t, f, posts, "get", "shown", "", asUser("u1")); code != http.StatusOK {
		t.Errorf("get shown = %d", code)
	}

	staff := newFilter(t, true)
	got, _ = serve(t, staff, posts, "list", "", "spec.hidden=true", nil)
	if got.selector != "spec.hidden=true" {
		t.Errorf("staff selector changed to %q", got.selector)
	}
	if _, code := serve(t, staff, posts, "get", "hidden", "", nil); code != http.StatusOK {
		t.Errorf("staff get hidden = %d", code)
	}
}

func TestPassThrough(t *testing.T) {
	f := newFilter(t, false)
	inProject := func(ctx context.Context) context.Context {
		return milorequest.WithProject(asUser("u1")(ctx), "p")
	}
	if got, _ := serve(t, f, votes, "list", "", "", inProject); got.selector != "" {
		t.Errorf("project-context selector changed to %q", got.selector)
	}
	other := schema.GroupVersionResource{Group: group, Version: "v1alpha1", Resource: "releases"}
	if got, _ := serve(t, f, other, "list", "", "", asUser("u1")); got.selector != "" {
		t.Errorf("unannotated selector changed to %q", got.selector)
	}
	if _, code := serve(t, f, votes, "create", "", "", asUser("u1")); code != http.StatusOK {
		t.Errorf("create = %d", code)
	}
}

func TestNotSyncedFailsClosedInTheUserContext(t *testing.T) {
	f := newFilter(t, false)
	f.Rules.synced = false
	if _, code := serve(t, f, votes, "list", "", "", asUser("u1")); code != http.StatusServiceUnavailable {
		t.Errorf("user context = %d, want 503", code)
	}
	if _, code := serve(t, f, votes, "list", "", "", nil); code != http.StatusOK {
		t.Errorf("platform context = %d", code)
	}
}

func TestBadSelectorIsRejected(t *testing.T) {
	f := newFilter(t, false)
	if _, code := serve(t, f, votes, "list", "", "a%3Db%3Dc", asUser("u1")); code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", code)
	}
}

func TestRuleFor(t *testing.T) {
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			OwnerFieldAnnotation:  ".spec.userRef.name",
			HiddenFieldAnnotation: "spec.hidden",
		}},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
			Name: "v1alpha1", Served: true,
			SelectableFields: []apiextensionsv1.SelectableField{{JSONPath: ".spec.userRef.name"}},
		}}},
	}
	got := RuleFor(crd)
	want := Rule{OwnerField: "spec.userRef.name", HiddenField: "spec.hidden", HiddenSubresource: DefaultHiddenSubresource}
	if got != want {
		t.Errorf("RuleFor = %+v, want %+v", got, want)
	}
	if !selectable(crd, "spec.userRef.name") || selectable(crd, "spec.hidden") {
		t.Error("selectable reports the wrong fields")
	}
	if !RuleFor(&apiextensionsv1.CustomResourceDefinition{}).IsZero() {
		t.Error("a CRD without annotations has a rule")
	}
}

// inOrg puts the request in an organization context, made by a caller with
// the given user ID.
func inOrg(callerUID string) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		ctx = milorequest.WithOrganization(ctx, "o")
		return request.WithUser(ctx, &user.DefaultInfo{Name: "someone@example.com", UID: callerUID})
	}
}

func TestDeleteCollectionIsLimited(t *testing.T) {
	f := newFilter(t, false)
	got, _ := serve(t, f, votes, "deletecollection", "", "spec.postRef.name=p1", asUser("u1"))
	if want := "spec.postRef.name=p1,spec.userRef.name=u1"; got.selector != want || got.infoSel != want {
		t.Errorf("owner selector = %q (request info %q), want %q", got.selector, got.infoSel, want)
	}
	got, _ = serve(t, f, posts, "deletecollection", "", "", nil)
	if want := "spec.hidden!=true"; got.selector != want || got.infoSel != want {
		t.Errorf("hidden selector = %q (request info %q), want %q", got.selector, got.infoSel, want)
	}
}

func TestOwnerRuleAppliesInAnOrganizationContext(t *testing.T) {
	f := newFilter(t, false)
	got, _ := serve(t, f, votes, "list", "", "spec.userRef.name=u2", inOrg("u1"))
	if want := "spec.userRef.name=u1"; got.selector != want {
		t.Errorf("selector = %q, want %q", got.selector, want)
	}
	if _, code := serve(t, f, votes, "get", "theirs", "", inOrg("u1")); code != http.StatusNotFound {
		t.Errorf("get theirs = %d, want 404", code)
	}
	if _, code := serve(t, f, votes, "get", "mine", "", inOrg("u1")); code != http.StatusOK {
		t.Errorf("get mine = %d", code)
	}
	// A caller with no user ID can't own anything, and an empty owner
	// selector would match objects with no owner.
	if got, code := serve(t, f, votes, "list", "", "", inOrg("")); code != http.StatusForbidden || got.served {
		t.Errorf("no caller ID: code %d served %v, want 403", code, got.served)
	}
	f.Rules.synced = false
	if _, code := serve(t, f, votes, "list", "", "", inOrg("u1")); code != http.StatusServiceUnavailable {
		t.Errorf("not synced = %d, want 503", code)
	}
}

// TestLoopbackReadsAreNotNarrowed covers the named-request check's reliance
// on its own reads: allowsNamed treats NotFound as "doesn't exist", which is
// only safe if the loopback client sees hidden objects. Its identity is in
// system:masters, which the server's privileged-groups authorizer allows.
func TestLoopbackReadsAreNotNarrowed(t *testing.T) {
	f := newFilter(t, false)
	f.Authorizer = authorizerfactory.NewPrivilegedGroups(user.SystemPrivilegedGroup)
	loopback := func(ctx context.Context) context.Context {
		return request.WithUser(ctx, &user.DefaultInfo{Name: user.APIServerUser, Groups: []string{user.SystemPrivilegedGroup}})
	}
	if got, code := serve(t, f, posts, "get", "hidden", "", loopback); code != http.StatusOK || !got.served {
		t.Errorf("loopback get hidden: code %d served %v", code, got.served)
	}
	if got, _ := serve(t, f, posts, "list", "", "", loopback); got.selector != "" {
		t.Errorf("loopback list selector = %q", got.selector)
	}
	if got, _ := serve(t, f, votes, "list", "", "", loopback); got.selector != "" {
		t.Errorf("loopback vote list selector = %q", got.selector)
	}
}

func TestAnnotationProblems(t *testing.T) {
	crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		OwnerFieldAnnotation:           " ",
		HiddenSubresourceAnnotation:    "staff",
		"iam.miloapis.com/owner-feild": "spec.userRef.name",
		"example.com/unrelated":        "x",
	}}}
	got := AnnotationProblems(crd)
	if len(got) != 3 {
		t.Fatalf("problems = %q, want 3", got)
	}
	ok := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		OwnerFieldAnnotation: "spec.userRef.name", HiddenFieldAnnotation: "spec.hidden", HiddenSubresourceAnnotation: "staff",
	}}}
	if got := AnnotationProblems(ok); len(got) != 0 {
		t.Errorf("problems for valid annotations = %q", got)
	}
}
