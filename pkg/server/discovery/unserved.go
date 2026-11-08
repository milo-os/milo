package discovery

import (
	"context"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilcache "k8s.io/apimachinery/pkg/util/cache"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	"go.miloapis.com/milo/pkg/request"
)

const unservedRequestsHelp = "Resource requests in a project for a type the project does not serve, by verb, group and resource. " +
	"A type that a wildcard rule hides counts under the rule's patterns"

// unservedRequests counts what the garbage collector would not see once it watches only the types a project serves
// (milo#834): a project's object of a type the project does not serve.
var unservedRequests = k8smetrics.NewCounterVec(
	&k8smetrics.CounterOpts{
		Name:           "projectstorage_unserved_requests_total",
		Help:           unservedRequestsHelp,
		StabilityLevel: k8smetrics.ALPHA,
	},
	[]string{"verb", "group", "resource"},
)

func init() {
	legacyregistry.MustRegister(unservedRequests)
}

// The caller log's bounds. The seen set is an LRUExpireCache, as NamespaceLifecycle's forceLiveLookupCache is
// (k8s.io/apiserver v0.37.1); the lines pass token-bucket limiters (golang.org/x/time/rate), one for writes and one for
// the rest, so a burst of readers never takes a writer's line.
const (
	callerLogSize  = 1024      // (user, verb, group, resource) keys a process remembers
	callerLogTTL   = time.Hour // so a caller that is still active is named in every hour of the log
	callerLogEvery = time.Second
	callerLogBurst = 10

	callerLogMessage = "A project requested a type it does not serve"
)

// newUnservedRequestCounter returns a filter that counts each resource request in a project for a type the registry
// does not serve in Project context, and logs each new caller of one. It serves every request as before.
func newUnservedRequestCounter(registry *Registry, callers *callerLog) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if u, ok := unservedInProject(req.Context(), registry); ok {
				unservedRequests.WithLabelValues(u.info.Verb, u.declared.Group, u.declared.Resource).Inc()
				callers.record(req.Context(), u.info, u.project)
			}
			next.ServeHTTP(w, req)
		})
	}
}

// unservedRequest is a request the counter counts: its info and project, which the caller log names, and the
// declaration that hides its type, which labels the count. A wildcard rule's patterns stand for every name it matches,
// so a name the request chose never becomes a label.
type unservedRequest struct {
	info     *genericapirequest.RequestInfo
	project  string
	declared schema.GroupResource
}

// unservedInProject returns the request when it requests, in a project, a type the project does not serve. A project
// is named, as storage routes it: an empty name is the platform's. It answers false until the registry syncs: before
// the policies are read, a type a policy widens to Project reads as hidden.
func unservedInProject(ctx context.Context, registry *Registry) (unservedRequest, bool) {
	info, ok := genericapirequest.RequestInfoFrom(ctx)
	if !ok || !info.IsResourceRequest {
		return unservedRequest{}, false
	}
	project, ok := request.ProjectID(ctx)
	if !ok || project == "" || !registry.HasSynced() {
		return unservedRequest{}, false
	}
	contexts, declared := registry.declaration(schema.GroupResource{Group: info.APIGroup, Resource: info.Resource})
	return unservedRequest{info: info, project: project, declared: declared}, !Matches(contexts, ContextProject)
}

// callerLog logs the first unserved request of each (user, verb, group, resource), so a caller can be found by name:
// the counter has no user label, and the audit policy logs no watch.
type callerLog struct {
	seen          *utilcache.LRUExpireCache
	reads, writes *rate.Limiter
	logger        logr.Logger
}

type callerKey struct {
	user, verb string
	gr         schema.GroupResource
}

// newProcessCallerLog returns the caller log a process keeps, with the bounds above.
func newProcessCallerLog() *callerLog {
	limiter := func() *rate.Limiter { return rate.NewLimiter(rate.Every(callerLogEvery), callerLogBurst) }
	return newCallerLog(clock.RealClock{}, callerLogSize, limiter(), limiter(), klog.Background())
}

func newCallerLog(clock utilcache.Clock, size int, reads, writes *rate.Limiter, logger logr.Logger) *callerLog {
	return &callerLog{seen: utilcache.NewLRUExpireCacheWithClock(size, clock), reads: reads, writes: writes,
		logger: logger}
}

// record logs the request's caller unless the log has seen it. Two concurrent first requests of a key may both log,
// as NamespaceLifecycle's cache may look up twice: a line more, never a line less.
func (c *callerLog) record(ctx context.Context, info *genericapirequest.RequestInfo, project string) {
	key := callerKey{verb: info.Verb, gr: schema.GroupResource{Group: info.APIGroup, Resource: info.Resource}}
	if u, ok := genericapirequest.UserFrom(ctx); ok {
		key.user = u.GetName()
	}
	if _, seen := c.seen.Get(key); seen {
		return
	}
	// A key the limiter refuses stays unseen, so its next request logs it.
	if !c.limiterFor(info.Verb).Allow() {
		return
	}
	c.seen.Add(key, struct{}{}, callerLogTTL)
	c.logger.Info(callerLogMessage, "user", key.user, "verb", key.verb, "group", key.gr.Group,
		"resource", key.gr.Resource, "project", project)
}

// limiterFor returns the limiter a verb's lines pass: writes have their own.
func (c *callerLog) limiterFor(verb string) *rate.Limiter {
	if request.IsWrite(verb) {
		return c.writes
	}
	return c.reads
}
