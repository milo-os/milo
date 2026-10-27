package discovery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/authentication/user"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/component-base/metrics/testutil"
	clocktesting "k8s.io/utils/clock/testing"

	"go.miloapis.com/milo/pkg/request"
	"go.miloapis.com/milo/pkg/server/filters"
)

var (
	rolesGR  = schema.GroupResource{Group: "rbac.authorization.k8s.io", Resource: "roles"}
	leasesGR = schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}
	widgetGR = schema.GroupResource{Group: "example.com", Resource: "widgets"}
)

// requestVerbs are the verbs the request info resolver gives a resource request.
var requestVerbs = map[string]struct{ method, path string }{
	"get":              {http.MethodGet, "/namespaces/default/%s/x"},
	"list":             {http.MethodGet, "/namespaces/default/%s"},
	"watch":            {http.MethodGet, "/namespaces/default/%s?watch=true"},
	"create":           {http.MethodPost, "/namespaces/default/%s"},
	"update":           {http.MethodPut, "/namespaces/default/%s/x"},
	"patch":            {http.MethodPatch, "/namespaces/default/%s/x"},
	"delete":           {http.MethodDelete, "/namespaces/default/%s/x"},
	"deletecollection": {http.MethodDelete, "/namespaces/default/%s"},
}

// syncedRegistry declares roles Platform-only and leases Platform and Project, as Milo's core policy does.
func syncedRegistry() *Registry {
	r := newRegistry()
	r.upsertFromPolicy("core", policySpec{rules: []policyRule{
		{group: rolesGR.Group, resources: []string{rolesGR.Resource}, contexts: []string{"Platform"}},
		{group: leasesGR.Group, resources: []string{leasesGR.Resource}, contexts: []string{"Platform", "Project"}},
	}})
	r.hasInit, r.hasPolicyInit = true, true
	return r
}

// serve sends one request through the counter, with the request info the resolver gives its path, in the context
// inContext sets. It reports whether the next handler served it.
func serve(t *testing.T, filter func(http.Handler) http.Handler, inContext func(context.Context) context.Context,
	method, path, userName string) bool {
	t.Helper()
	served := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusTeapot)
	})
	req := httptest.NewRequest(method, path, nil)
	resolver := &genericapirequest.RequestInfoFactory{
		APIPrefixes:          sets.NewString("api", "apis"),
		GrouplessAPIPrefixes: sets.NewString("api"),
	}
	info, err := resolver.NewRequestInfo(req)
	if err != nil {
		t.Fatalf("resolve %s %s: %v", method, path, err)
	}
	ctx := genericapirequest.WithRequestInfo(inContext(req.Context()), info)
	ctx = genericapirequest.WithUser(ctx, &user.DefaultInfo{Name: userName})
	rec := httptest.NewRecorder()
	filter(next).ServeHTTP(rec, req.WithContext(ctx))
	if served && rec.Code != http.StatusTeapot {
		t.Errorf("%s %s: the response is %d, not the next handler's own", method, path, rec.Code)
	}
	return served
}

func inProject(ctx context.Context) context.Context { return request.WithProject(ctx, "p1") }
func inOrganization(ctx context.Context) context.Context {
	return request.WithOrganization(ctx, "o1")
}
func inUser(ctx context.Context) context.Context {
	return genericapirequest.WithValue(ctx, filters.UserIDContextKey, "u1")
}
func atPlatform(ctx context.Context) context.Context { return ctx }

func apiPath(gr schema.GroupResource, verbPath string) string {
	return "/apis/" + gr.Group + "/v1" + verbPath
}

// wantCounted fails unless the counter holds exactly these series, each "verb group resource", at these values.
func wantCounted(t *testing.T, want map[string]float64) {
	t.Helper()
	var lines []string
	for series, value := range want {
		f := strings.Fields(series)
		lines = append(lines, fmt.Sprintf("projectstorage_unserved_requests_total{group=%q,resource=%q,verb=%q} %v",
			f[1], f[2], f[0], value))
	}
	sort.Strings(lines)
	expected := ""
	if len(lines) > 0 {
		expected = "# HELP projectstorage_unserved_requests_total [ALPHA] " + unservedRequestsHelp + "\n" +
			"# TYPE projectstorage_unserved_requests_total counter\n" + strings.Join(lines, "\n") + "\n"
	}
	if err := testutil.CollectAndCompare(unservedRequests, strings.NewReader(expected),
		"projectstorage_unserved_requests_total"); err != nil {
		t.Error(err)
	}
}

// quietCallers is a caller log that never logs, for the counter's own tests.
func quietCallers() *callerLog {
	return newCallerLog(clocktesting.NewFakeClock(time.Now()), 16, unlimited(), unlimited(), logr.Discard())
}

func unlimited() *rate.Limiter { return rate.NewLimiter(rate.Inf, 1) }

// inUnnamedProject is a project path with no name, /projects//control-plane/, which storage serves as the platform.
func inUnnamedProject(ctx context.Context) context.Context {
	return request.WithProject(ctx, "")
}

// C1, C2, C6: every verb of a hidden type in a project counts once and is served; a served type never counts.
func TestUnservedRequestCounterCountsEveryVerbOfAHiddenTypeInAProject(t *testing.T) {
	unservedRequests.Reset()
	filter := newUnservedRequestCounter(syncedRegistry(), quietCallers())
	want := map[string]float64{}
	for verb, r := range requestVerbs {
		for _, gr := range []schema.GroupResource{rolesGR, leasesGR, widgetGR} {
			if !serve(t, filter, inProject, r.method, apiPath(gr, fmt.Sprintf(r.path, gr.Resource)), "alice") {
				t.Errorf("%s %s in a project was not served", verb, gr)
			}
		}
		want[verb+" "+rolesGR.Group+" "+rolesGR.Resource] = 1
	}
	wantCounted(t, want)
}

// C3: outside Project context, a hidden type counts nothing.
func TestUnservedRequestCounterCountsOnlyInAProject(t *testing.T) {
	unservedRequests.Reset()
	filter := newUnservedRequestCounter(syncedRegistry(), quietCallers())
	for name, inContext := range map[string]func(context.Context) context.Context{
		"platform": atPlatform, "organization": inOrganization, "user": inUser, "unnamed project": inUnnamedProject,
	} {
		if !serve(t, filter, inContext, http.MethodPost, apiPath(rolesGR, "/namespaces/default/roles"), "alice") {
			t.Errorf("a create of roles in %s context was not served", name)
		}
	}
	wantCounted(t, nil)
}

// C4: before the registry syncs, a CRD tagged Organization that a policy widens to Project would read as hidden.
func TestUnservedRequestCounterCountsNothingBeforeTheRegistrySyncs(t *testing.T) {
	unservedRequests.Reset()
	registry := newRegistry()
	registry.crd[widgetGR] = []ParentContext{ContextOrganization}
	registry.hasInit = true
	filter := newUnservedRequestCounter(registry, quietCallers())
	path := apiPath(widgetGR, "/namespaces/default/widgets")

	serve(t, filter, inProject, http.MethodPost, path, "alice")
	wantCounted(t, nil)

	registry.upsertFromPolicy("widen", policySpec{rules: []policyRule{
		{group: widgetGR.Group, resources: []string{widgetGR.Resource}, contexts: []string{"Organization", "Project"}},
	}})
	registry.hasPolicyInit = true
	serve(t, filter, inProject, http.MethodPost, path, "alice")
	wantCounted(t, nil)

	serve(t, filter, inProject, http.MethodPost, apiPath(rolesGR, "/namespaces/default/roles"), "alice")
	wantCounted(t, nil) // roles carry no declaration in this registry: served everywhere
	registry.RegisterStatic(rolesGR, ContextPlatform)
	serve(t, filter, inProject, http.MethodPost, apiPath(rolesGR, "/namespaces/default/roles"), "alice")
	wantCounted(t, map[string]float64{"create rbac.authorization.k8s.io roles": 1})
}

// C5: a subresource request counts under its type; a non-resource request counts nothing.
func TestUnservedRequestCounterLabelsTheTypeNotTheSubresource(t *testing.T) {
	unservedRequests.Reset()
	filter := newUnservedRequestCounter(syncedRegistry(), quietCallers())
	serve(t, filter, inProject, http.MethodPut, apiPath(rolesGR, "/namespaces/default/roles/x/status"), "alice")
	serve(t, filter, inProject, http.MethodGet, "/apis/rbac.authorization.k8s.io/v1", "alice")
	serve(t, filter, inProject, http.MethodGet, "/healthz", "alice")
	wantCounted(t, map[string]float64{"update rbac.authorization.k8s.io roles": 1})
}

// lines collects what a caller log writes, one string a line.
type lines []string

func (l *lines) logger() logr.Logger {
	return funcr.New(func(prefix, args string) { *l = append(*l, args) }, funcr.Options{})
}

func callerLine(userName, verb string, gr schema.GroupResource) string {
	return fmt.Sprintf(`"level"=0 "msg"=%q "user"=%q "verb"=%q "group"=%q "resource"=%q "project"="p1"`,
		callerLogMessage, userName, verb, gr.Group, gr.Resource)
}

func wantLines(t *testing.T, got lines, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the caller log wrote\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// L1, L2: one line the first time a (user, verb, group, resource) is seen; a new user or a new verb logs again.
func TestCallerLogLogsEachCallerOnce(t *testing.T) {
	var got lines
	callers := newCallerLog(clocktesting.NewFakeClock(time.Now()), 16, unlimited(), unlimited(), got.logger())
	filter := newUnservedRequestCounter(syncedRegistry(), callers)
	create := apiPath(rolesGR, "/namespaces/default/roles")

	serve(t, filter, inProject, http.MethodPost, create, "alice")
	serve(t, filter, inProject, http.MethodPost, create, "alice")
	serve(t, filter, inProject, http.MethodPost, create, "bob")
	serve(t, filter, inProject, http.MethodGet, create, "alice")
	serve(t, filter, inProject, http.MethodPost, apiPath(leasesGR, "/namespaces/default/leases"), "alice")
	wantLines(t, got,
		callerLine("alice", "create", rolesGR),
		callerLine("bob", "create", rolesGR),
		callerLine("alice", "list", rolesGR))
}

// L3: the seen set holds a fixed number of keys and forgets the oldest; a key also expires, so an active caller logs
// again once in each period.
func TestCallerLogForgetsTheOldestAndTheExpired(t *testing.T) {
	var got lines
	clock := clocktesting.NewFakeClock(time.Now())
	callers := newCallerLog(clock, 2, unlimited(), unlimited(), got.logger())
	filter := newUnservedRequestCounter(syncedRegistry(), callers)
	create := apiPath(rolesGR, "/namespaces/default/roles")

	for _, name := range []string{"alice", "bob", "carol", "alice"} {
		serve(t, filter, inProject, http.MethodPost, create, name)
	}
	clock.Step(callerLogTTL + time.Second)
	serve(t, filter, inProject, http.MethodPost, create, "alice")
	wantLines(t, got,
		callerLine("alice", "create", rolesGR),
		callerLine("bob", "create", rolesGR),
		callerLine("carol", "create", rolesGR),
		callerLine("alice", "create", rolesGR), // evicted by carol
		callerLine("alice", "create", rolesGR)) // expired
}

// L4: a key the limiter refuses logs nothing and stays unseen, so its next request after a token returns logs it.
func TestCallerLogLogsALimitedKeyLater(t *testing.T) {
	var got lines
	limiter := rate.NewLimiter(rate.Limit(0), 1)
	callers := newCallerLog(clocktesting.NewFakeClock(time.Now()), 16, unlimited(), limiter, got.logger())
	filter := newUnservedRequestCounter(syncedRegistry(), callers)
	create := apiPath(rolesGR, "/namespaces/default/roles")

	serve(t, filter, inProject, http.MethodPost, create, "alice")
	serve(t, filter, inProject, http.MethodPost, create, "bob")
	wantLines(t, got, callerLine("alice", "create", rolesGR))

	limiter.SetLimit(rate.Inf)
	serve(t, filter, inProject, http.MethodPost, create, "bob")
	wantLines(t, got, callerLine("alice", "create", rolesGR), callerLine("bob", "create", rolesGR))
}

// L4: writes have a limiter of their own, so a write's caller is named while reads use up theirs: at a pod's start
// and each hour, the controller manager's watch keys alone fill the readers' burst.
func TestCallerLogLogsAWriteWhileReadsUseUpTheirLimiter(t *testing.T) {
	var got lines
	reads := rate.NewLimiter(rate.Limit(0), 1)
	callers := newCallerLog(clocktesting.NewFakeClock(time.Now()), 16, reads, rate.NewLimiter(rate.Limit(0), 1), got.logger())
	filter := newUnservedRequestCounter(syncedRegistry(), callers)
	list := apiPath(rolesGR, "/namespaces/default/roles")

	serve(t, filter, inProject, http.MethodGet, list, "controller-manager")
	serve(t, filter, inProject, http.MethodGet, list+"?watch=true", "controller-manager")
	serve(t, filter, inProject, http.MethodPost, list, "tenant")
	wantLines(t, got, callerLine("controller-manager", "list", rolesGR), callerLine("tenant", "create", rolesGR))
}

// C3, C5 behind the real project router: the path a client sends, not a context set by hand.
func TestUnservedRequestCounterBehindTheProjectRouter(t *testing.T) {
	unservedRequests.Reset()
	resolver := &genericapirequest.RequestInfoFactory{
		APIPrefixes:          sets.NewString("api", "apis"),
		GrouplessAPIPrefixes: sets.NewString("api"),
	}
	chain := filters.ProjectRouterWithRequestInfo(
		NewFilters(syncedRegistry(), false)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})), resolver)
	plane := "/apis/resourcemanager.miloapis.com/v1alpha1/projects/%s/control-plane"
	for _, path := range []string{
		fmt.Sprintf(plane, "p1") + apiPath(rolesGR, "/namespaces/default/roles/r1/status"),
		fmt.Sprintf(plane, "p1") + apiPath(leasesGR, "/namespaces/default/leases"),
		fmt.Sprintf(plane, "") + apiPath(rolesGR, "/namespaces/default/roles"),
		apiPath(rolesGR, "/namespaces/default/roles"),
	} {
		chain.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, path, nil))
	}
	wantCounted(t, map[string]float64{"update rbac.authorization.k8s.io roles": 1})
}

// deepContext stands for a production request's context: the filters before this one each add a value.
func deepContext(ctx context.Context) context.Context {
	type key int
	for i := range 30 {
		ctx = context.WithValue(ctx, key(i), i)
	}
	return ctx
}

// The counter's cost on the request path: a served type in a project, a request at the platform, and a hidden type in
// a project once its caller is logged.
func BenchmarkUnservedRequestCounter(b *testing.B) {
	filter := newUnservedRequestCounter(syncedRegistry(), quietCallers())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, c := range []struct {
		name      string
		inContext func(context.Context) context.Context
		gr        schema.GroupResource
	}{
		{"served type in a project", inProject, leasesGR},
		{"platform", atPlatform, leasesGR},
		{"hidden type in a project", inProject, rolesGR},
	} {
		b.Run(c.name, func(b *testing.B) {
			req := httptest.NewRequest(http.MethodGet, apiPath(c.gr, "/namespaces/default/"+c.gr.Resource), nil)
			info := &genericapirequest.RequestInfo{IsResourceRequest: true, Verb: "list",
				APIGroup: c.gr.Group, APIVersion: "v1", Resource: c.gr.Resource}
			ctx := genericapirequest.WithRequestInfo(c.inContext(req.Context()), info)
			req = req.WithContext(deepContext(genericapirequest.WithUser(ctx, &user.DefaultInfo{Name: "alice"})))
			w := httptest.NewRecorder()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				filter.ServeHTTP(w, req)
			}
		})
	}
}

// W1, W2: the counter counts whatever the discovery gate says; the discovery filter acts only with its gate on.
func TestNewFiltersCountWhateverTheDiscoveryGateSays(t *testing.T) {
	discoveryDoc := `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"rbac.authorization.k8s.io/v1",` +
		`"resources":[{"name":"roles","namespaced":true,"kind":"Role","verbs":["get"]}]}`
	for _, filterDiscovery := range []bool{false, true} {
		t.Run(fmt.Sprintf("filterDiscovery=%v", filterDiscovery), func(t *testing.T) {
			unservedRequests.Reset()
			chain := NewFilters(syncedRegistry(), filterDiscovery)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(discoveryDoc))
			}))
			create := httptest.NewRequest(http.MethodPost, apiPath(rolesGR, "/namespaces/default/roles"), nil)
			info := &genericapirequest.RequestInfo{IsResourceRequest: true, Verb: "create",
				APIGroup: rolesGR.Group, APIVersion: "v1", Namespace: "default", Resource: rolesGR.Resource}
			chain.ServeHTTP(httptest.NewRecorder(),
				create.WithContext(genericapirequest.WithRequestInfo(inProject(create.Context()), info)))
			wantCounted(t, map[string]float64{"create rbac.authorization.k8s.io roles": 1})

			discover := httptest.NewRequest(http.MethodGet, "/apis/rbac.authorization.k8s.io/v1", nil)
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, discover.WithContext(inProject(discover.Context())))
			if listed := strings.Contains(rec.Body.String(), `"roles"`); listed == filterDiscovery {
				t.Errorf("project discovery lists roles: %v, want %v", listed, !filterDiscovery)
			}
		})
	}
}
