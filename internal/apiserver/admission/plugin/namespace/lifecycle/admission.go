// pkg/apiserver/admission/plugin/namespace/lifecycle/admission.go
package lifecycle

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.miloapis.com/milo/pkg/request"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	utilcache "k8s.io/apimachinery/pkg/util/cache"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/admission/initializer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	rest "k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
)

const (
	PluginName           = "ProjectNamespaceLifecycle"
	forceLiveLookupTTL   = 30 * time.Second
	missingNamespaceWait = 50 * time.Millisecond
)

// Register registers this plugin factory into the provided registry.
func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(_ io.Reader) (admission.Interface, error) {
		return newLifecycleWithClock(
			sets.NewString(metav1.NamespaceDefault, metav1.NamespaceSystem, metav1.NamespacePublic),
			clock.RealClock{},
		)
	})
}

type Lifecycle struct {
	*admission.Handler

	// Root-cluster deps (upstream behavior)
	client          kubernetes.Interface
	namespaceLister informers.SharedInformerFactory // we hold factory to derive the lister
	ready           func() bool

	immortalNamespaces   sets.String
	forceLiveLookupCache *utilcache.LRUExpireCache // keys are "project/ns"

	// Tenant-aware live GETs
	loopbackCfg   *rest.Config // injected
	clientsByProj sync.Map     // map[string]*kubernetes.Clientset
}

var (
	_ = initializer.WantsExternalKubeInformerFactory(&Lifecycle{})
	_ = initializer.WantsExternalKubeClientSet(&Lifecycle{})
)

// --- admission.Interface ---

func (l *Lifecycle) Admit(ctx context.Context, a admission.Attributes, _ admission.ObjectInterfaces) error {
	// Log first so we can see calls even if readiness blocks later.
	// --- tracing: top-level span for this admission call
	projectID, _ := request.ProjectID(ctx)
	attrs := []trace.SpanStartOption{
		trace.WithAttributes(
			attribute.String("op", string(a.GetOperation())),
			attribute.String("ns", a.GetNamespace()),
			attribute.String("gvk.group", a.GetKind().Group),
			attribute.String("gvk.version", a.GetKind().Version),
			attribute.String("gvk.kind", a.GetKind().Kind),
			attribute.String("resource", a.GetResource().Resource),
			attribute.String("user", a.GetUserInfo().GetName()),
			attribute.String("project.id", projectID),
		),
	}
	ctx, span := l.startSpan(ctx, "namespace.lifecycle.Admit", attrs...)
	defer span.End()
	klog.V(2).InfoS("ProjectNamespaceLifecycle.Admit",
		"projectID", projectID, "namespace", a.GetNamespace(), "operation", a.GetOperation(), "resource", a.GetResource())

	// Prevent deletion of immortal namespaces (root or project equally)
	if a.GetOperation() == admission.Delete &&
		a.GetKind().GroupKind() == v1.SchemeGroupVersion.WithKind("Namespace").GroupKind() &&
		l.immortalNamespaces.Has(a.GetName()) {
		err := fmt.Errorf("this namespace may not be deleted")
		span.RecordError(err)
		span.SetStatus(codes.Error, "delete forbidden for immortal namespace")
		return apierrors.NewForbidden(a.GetResource().GroupResource(), a.GetName(), err)
	}

	// Always allow non-namespaced resources (except Namespace itself)
	if len(a.GetNamespace()) == 0 &&
		a.GetKind().GroupKind() != v1.SchemeGroupVersion.WithKind("Namespace").GroupKind() {
		return nil
	}

	// Namespace objects themselves are always allowed; mark for force-live on delete
	if a.GetKind().GroupKind() == v1.SchemeGroupVersion.WithKind("Namespace").GroupKind() {
		if a.GetOperation() == admission.Delete {
			l.forceLiveLookupCache.Add(cacheKey(ctx, a.GetName()), true, forceLiveLookupTTL)
		}
		return nil
	}

	// Always allow delete of other resources
	if a.GetOperation() == admission.Delete {
		return nil
	}

	// Access review passthrough (do not leak namespace existence)
	if isAccessReview(a) {
		return nil
	}

	// Gate on readiness for ROOT only. Project path does live lookups and doesn't need the root lister cache.
	if projectID == "" && !l.WaitForReady() {
		err := fmt.Errorf("not yet ready to handle request")
		span.RecordError(err)
		span.SetStatus(codes.Error, "root informer not ready")
		return admission.NewForbidden(a, err)
	}

	clusterKey := cacheKey(ctx, a.GetNamespace())

	// === ROOT path: keep upstream behavior with informer + optional live ===
	if projectID == "" {
		// Try lister first (root) with child span
		var ns *v1.Namespace
		var exists bool
		var err error
		func() {
			_, sub := l.startSpan(ctx, "namespace.lifecycle.root.listerGet",
				trace.WithAttributes(
					attribute.String("ns", a.GetNamespace()),
					attribute.String("op", string(a.GetOperation())),
				))
			defer sub.End()
			ns, exists, err = l.getFromRootLister(a.GetNamespace(), a.GetOperation())
			sub.SetAttributes(attribute.Bool("exists", exists))
			if err != nil {
				sub.RecordError(err)
				sub.SetStatus(codes.Error, "lister get failed")
			}
		}()
		if err != nil {
			return err
		}

		// Force live if we suspect stale cache after root delete
		forceLive := false
		if exists {
			if _, ok := l.forceLiveLookupCache.Get(clusterKey); ok && ns.Status.Phase == v1.NamespaceActive {
				forceLive = true
			}
		}

		if !exists || forceLive {
			var n *v1.Namespace
			subCtx, sub := l.startSpan(ctx, "namespace.lifecycle.root.liveGet",
				trace.WithAttributes(
					attribute.String("ns", a.GetNamespace()),
					attribute.Bool("forceLive", forceLive),
					attribute.Bool("cache.exists", exists),
				))
			n, err := l.client.CoreV1().Namespaces().Get(subCtx, a.GetNamespace(), metav1.GetOptions{})
			switch {
			case apierrors.IsNotFound(err):
				sub.RecordError(err)
				sub.SetStatus(codes.Error, "root live get 404")
				sub.End()
				return err
			case err != nil:
				sub.RecordError(err)
				sub.SetStatus(codes.Error, "root live get error")
				sub.End()
				return apierrors.NewInternalError(err)
			default:
				sub.End()
				ns = n
			}
		}
		return l.enforceCreateNotInTerminating(ctx, a, ns)
	}

	// === PROJECT path: skip root lister; do a live lookup against project-scoped client ===

	// If create and namespace might be racing, wait a tick to improve success
	if a.GetOperation() == admission.Create {
		time.Sleep(missingNamespaceWait)
	}

	forceLive := false
	if _, ok := l.forceLiveLookupCache.Get(clusterKey); ok {
		// we only cache a hint; just force a live lookup below
		forceLive = true
		trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("forceLive.hint", true))

	}

	// Live GET against the project virtual cluster
	var nsClient *kubernetes.Clientset
	var errClient error
	func() {
		_, sub := l.startSpan(ctx, "namespace.lifecycle.project.client",
			trace.WithAttributes(attribute.String("project.id", projectID)))
		defer sub.End()
		nsClient, errClient = l.projectClient(projectID)
		if errClient != nil {
			sub.RecordError(errClient)
			sub.SetStatus(codes.Error, "project client init failed")
		} else {
			sub.SetAttributes(attribute.String("status", "ok"))
		}
	}()
	if errClient != nil {
		return apierrors.NewInternalError(fmt.Errorf("project client init failed: %w", errClient))
	}

	_, live := l.startSpan(ctx, "namespace.lifecycle.project.liveGet",
		trace.WithAttributes(
			attribute.String("ns", a.GetNamespace()),
			attribute.String("project.id", projectID),
			attribute.Bool("forceLive", forceLive),
		))
	n, err := nsClient.CoreV1().Namespaces().Get(ctx, a.GetNamespace(), metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		live.RecordError(err)
		live.SetStatus(codes.Error, "project live get 404")
		live.End()
		// Not found in this project cluster
		return err
	case err != nil:
		live.RecordError(err)
		live.SetStatus(codes.Error, "project live get error")
		live.End()
		return apierrors.NewInternalError(err)
	default:
		live.End()
		// got it
		if forceLive {
			klog.V(4).InfoS("Found namespace via project live lookup", "project", projectID, "namespace", klog.KRef("", a.GetNamespace()))
		}
		return l.enforceCreateNotInTerminating(ctx, a, n)
	}
}

func (l *Lifecycle) enforceCreateNotInTerminating(ctx context.Context, a admission.Attributes, ns *v1.Namespace) error {
	_, span := l.startSpan(ctx, "namespace.lifecycle.enforceCreateNotInTerminating",
		trace.WithAttributes(
			attribute.String("ns", a.GetNamespace()),
			attribute.String("op", string(a.GetOperation())),
			attribute.String("ns.phase", string(ns.Status.Phase)),
		))
	defer span.End()
	if a.GetOperation() != admission.Create {
		return nil
	}
	if ns.Status.Phase != v1.NamespaceTerminating {
		return nil
	}
	err := admission.NewForbidden(a, fmt.Errorf("unable to create new content in namespace %s because it is being terminated", a.GetNamespace()))
	if apierr, ok := err.(*apierrors.StatusError); ok {
		apierr.ErrStatus.Details.Causes = append(apierr.ErrStatus.Details.Causes, metav1.StatusCause{
			Type:    v1.NamespaceTerminatingCause,
			Message: fmt.Sprintf("namespace %s is being terminated", a.GetNamespace()),
			Field:   "metadata.namespace",
		})
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "create blocked: namespace terminating")
	return err
}

func (l *Lifecycle) getFromRootLister(ns string, op admission.Operation) (*v1.Namespace, bool, error) {
	// Use the lister wired to the root cluster
	lister := l.namespaceLister.Core().V1().Namespaces().Lister()
	exists := false
	n, err := lister.Get(ns)
	if err == nil {
		exists = true
	} else if !apierrors.IsNotFound(err) {
		return nil, false, apierrors.NewInternalError(err)
	}

	// If create and not seen yet, wait a bit and retry (upstream behavior)
	if !exists && op == admission.Create {
		time.Sleep(missingNamespaceWait)
		n2, err2 := lister.Get(ns)
		switch {
		case apierrors.IsNotFound(err2):
			// still not exists
		case err2 != nil:
			return nil, false, apierrors.NewInternalError(err2)
		default:
			n = n2
			exists = true
			klog.V(4).InfoS("Namespace existed in cache after waiting", "namespace", klog.KRef("", ns))
		}
	}
	return n, exists, nil
}

// --- Initializers (root deps) ---

func (l *Lifecycle) SetExternalKubeInformerFactory(f informers.SharedInformerFactory) {
	l.namespaceLister = f
	nsInf := f.Core().V1().Namespaces().Informer()
	l.ready = nsInf.HasSynced
}

func (l *Lifecycle) WaitForReady() bool {
	if l.ready == nil {
		return false
	}
	return l.ready()
}

func (l *Lifecycle) SetExternalKubeClientSet(client kubernetes.Interface) {
	l.client = client
}

func (l *Lifecycle) ValidateInitialization() error {
	if l.client == nil {
		return fmt.Errorf("missing client")
	}
	if l.namespaceLister == nil {
		return fmt.Errorf("missing namespace informer factory")
	}
	return nil
}

// --- Loopback config injection (custom) ---

// WantsLoopbackConfig lets our apiserver pass the loopback rest.Config.
type WantsLoopbackConfig interface {
	SetLoopbackConfig(*rest.Config)
}

func (l *Lifecycle) SetLoopbackConfig(cfg *rest.Config) {
	// Shallow copy; we’ll mutate WrapTransport per project
	c := rest.CopyConfig(cfg)
	l.loopbackCfg = c
}

// --- project client cache ---

func (l *Lifecycle) projectClient(project string) (*kubernetes.Clientset, error) {
	if v, ok := l.clientsByProj.Load(project); ok {
		return v.(*kubernetes.Clientset), nil
	}
	if l.loopbackCfg == nil {
		return nil, fmt.Errorf("loopback config not injected")
	}
	// Build a client that prefixes every path with /projects/<id>/control-plane
	cfg := rest.CopyConfig(l.loopbackCfg)
	cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		return &pathPrefixRT{
			rt:     rt,
			prefix: "/apis/resourcemanager.miloapis.com/v1alpha1/projects/" + project + "/control-plane",
		}
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	actual, _ := l.clientsByProj.LoadOrStore(project, cs)
	return actual.(*kubernetes.Clientset), nil
}

// --- transport that injects the virtual cluster path prefix ---

type pathPrefixRT struct {
	rt     http.RoundTripper
	prefix string // e.g. /projects/abc/control-plane
}

func (p *pathPrefixRT) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	tracer := otel.Tracer("go.miloapis.com/milo/admission/namespace")
	ctx, span := tracer.Start(ctx, "namespace.lifecycle.http.prefix")
	defer span.End()

	// 1) defensive checks
	if p.rt == nil {
		err := fmt.Errorf("pathPrefixRT: nil RoundTripper")
		span.RecordError(err)
		span.SetStatus(codes.Error, "nil roundtripper")
		return nil, err
	}
	if req.URL == nil {
		err := fmt.Errorf("pathPrefixRT: nil URL")
		span.RecordError(err)
		span.SetStatus(codes.Error, "nil url")
		return nil, err
	}

	// 2) clone before mutating (correct way)
	r := req
	if !strings.HasPrefix(req.URL.Path, p.prefix+"/") && req.URL.Path != p.prefix {
		r = req.Clone(ctx)

		// Build new Path from decoded path…
		suffixPath := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasSuffix(p.prefix, "/") {
			r.URL.Path = p.prefix + suffixPath
		} else {
			r.URL.Path = p.prefix + "/" + suffixPath
		}

		// …and if the original had an encoded RawPath, preserve it too.
		if raw := req.URL.RawPath; raw != "" {
			suffixRaw := strings.TrimPrefix(raw, "/")
			if strings.HasSuffix(p.prefix, "/") {
				r.URL.RawPath = p.prefix + suffixRaw
			} else {
				r.URL.RawPath = p.prefix + "/" + suffixRaw
			}
		} else {
			// No special encoding to preserve.
			r.URL.RawPath = ""
		}

		// optional: trace the full path w/ query, without touching RequestURI
		full := r.URL.Path
		if q := r.URL.RawQuery; q != "" {
			full += "?" + q
		}
		span.SetAttributes(
			attribute.String("prefix", p.prefix),
			attribute.String("request.path", full),
		)
	}

	resp, err := p.rt.RoundTrip(r)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "roundtrip error")
	}
	if resp != nil {
		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	}
	return resp, err
}

// --- helpers ---

func cacheKey(ctx context.Context, ns string) string {
	if proj, ok := request.ProjectID(ctx); ok && proj != "" {
		return proj + "/" + ns
	}
	return "/" + ns // root
}

// access review passthrough (same as upstream)
var accessReviewResources = map[schema.GroupResource]bool{
	{Group: "authorization.k8s.io", Resource: "localsubjectaccessreviews"}: true,
}

func isAccessReview(a admission.Attributes) bool {
	return accessReviewResources[a.GetResource().GroupResource()]
}

func newLifecycleWithClock(immortalNamespaces sets.String, _ clock.Clock) (*Lifecycle, error) {
	return &Lifecycle{
		Handler:              admission.NewHandler(admission.Create, admission.Update, admission.Delete),
		immortalNamespaces:   immortalNamespaces,
		forceLiveLookupCache: utilcache.NewLRUExpireCache(100),
	}, nil
}

// --- tracing helper (same pattern as other plugin) ---
func (l *Lifecycle) startSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	tracer := trace.SpanFromContext(ctx).TracerProvider().Tracer("go.miloapis.com/milo/admission/namespace")
	return tracer.Start(ctx, name, opts...)
}
