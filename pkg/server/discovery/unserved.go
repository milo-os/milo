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

const unservedRequestsHelp = "Resource requests in a project for a type the project does not serve, by verb, group and resource"

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

// The caller log's bounds, in upstream's shapes (k8s.io/apiserver v0.37.1): an LRUExpireCache, as NamespaceLifecycle's
// forceLiveLookupCache is, and a rate.Limiter, as storagebackend's etcd3.go limits its own log.
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
			if info, ok := unservedInProject(req.Context(), registry); ok {
				unservedRequests.WithLabelValues(info.Verb, info.APIGroup, info.Resource).Inc()
				callers.record(req.Context(), info)
			}
			next.ServeHTTP(w, req)
		})
	}
}

// unservedInProject returns the request's info when it requests, in a project, a type the project does not serve. It
// answers false until the registry syncs: before the policies are read, a type a policy widens to Project reads as
// hidden.
func unservedInProject(ctx context.Context, registry *Registry) (*genericapirequest.RequestInfo, bool) {
	info, ok := genericapirequest.RequestInfoFrom(ctx)
	if !ok || !info.IsResourceRequest || FromRequest(ctx) != ContextProject || !registry.HasSynced() {
		return nil, false
	}
	return info, !registry.IsVisible(schema.GroupResource{Group: info.APIGroup, Resource: info.Resource}, ContextProject)
}

// callerLog logs the first unserved request of each (user, verb, group, resource), so a caller can be found by name:
// the counter has no user label, and the audit policy logs no watch.
type callerLog struct {
	seen    *utilcache.LRUExpireCache
	limiter *rate.Limiter
	logger  logr.Logger
}

type callerKey struct {
	user, verb string
	gr         schema.GroupResource
}

// newProcessCallerLog returns the caller log a process keeps, with the bounds above.
func newProcessCallerLog() *callerLog {
	limiter := rate.NewLimiter(rate.Every(callerLogEvery), callerLogBurst)
	return newCallerLog(clock.RealClock{}, callerLogSize, limiter, klog.Background())
}

func newCallerLog(clock utilcache.Clock, size int, limiter *rate.Limiter, logger logr.Logger) *callerLog {
	return &callerLog{seen: utilcache.NewLRUExpireCacheWithClock(size, clock), limiter: limiter, logger: logger}
}

// record logs the request's caller unless the log has seen it. Two concurrent first requests of a key may both log,
// as NamespaceLifecycle's cache may look up twice: a line more, never a line less.
func (c *callerLog) record(ctx context.Context, info *genericapirequest.RequestInfo) {
	key := callerKey{verb: info.Verb, gr: schema.GroupResource{Group: info.APIGroup, Resource: info.Resource}}
	if u, ok := genericapirequest.UserFrom(ctx); ok {
		key.user = u.GetName()
	}
	if _, seen := c.seen.Get(key); seen {
		return
	}
	// A key the limiter refuses stays unseen, so its next request logs it.
	if !c.limiter.Allow() {
		return
	}
	c.seen.Add(key, struct{}{}, callerLogTTL)
	project, _ := request.ProjectID(ctx)
	c.logger.Info(callerLogMessage, "user", key.user, "verb", key.verb, "group", key.gr.Group,
		"resource", key.gr.Resource, "project", project)
}
