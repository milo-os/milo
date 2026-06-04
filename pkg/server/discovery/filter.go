package discovery

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	apidiscoveryv2 "k8s.io/api/apidiscovery/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
)

// /apis/{group}/{version} — legacy per-group-version APIResourceList
var apisGroupVersionRE = regexp.MustCompile(`^/apis/([^/]+)/([^/]+)/?$`)

// DiscoveryContextFilter wraps the apiserver handler chain and filters
// discovery responses (/apis, /apis/{group}/{version}) so that only resources
// tagged for the caller's parent context are visible. Resources with no
// registration are visible everywhere (backwards-compatible).
//
// The filter only inspects responses; it never writes its own errors. If
// anything in the response is unexpected (non-JSON, non-200, malformed) the
// original bytes are passed through unchanged.
//
// IMPORTANT: This is a discovery hint, not an authorization boundary. A
// client that already knows a hidden resource exists can still POST/GET
// directly. A companion admission check is required to make this a hard
// boundary.
func DiscoveryContextFilter(next http.Handler, registry *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Only filter GETs against the discovery endpoints. Skip when:
		// - not a GET (mutations don't touch discovery)
		// - registry hasn't synced yet (fall open during startup)
		// - request is at Platform context (no parent prefix in URL) —
		//   controllers, admin tools, and internal clients operate here
		//   and must see all resources to function correctly
		if req.Method != http.MethodGet || !registry.HasSynced() || FromRequest(req.Context()) == ContextPlatform {
			next.ServeHTTP(w, req)
			return
		}

		path := req.URL.Path
		switch {
		case path == "/apis" || path == "/apis/":
			filterAPIIndex(w, req, next, registry)
		case apisGroupVersionRE.MatchString(path):
			filterAPIResourceList(w, req, next, registry)
		default:
			next.ServeHTTP(w, req)
		}
	})
}

// filterAPIResourceList filters the per-(group,version) discovery doc.
func filterAPIResourceList(w http.ResponseWriter, req *http.Request, next http.Handler, registry *Registry) {
	cw := newCaptureWriter(w)
	next.ServeHTTP(cw, req)

	if !shouldFilter(cw) {
		cw.flushUnchanged()
		return
	}

	var list metav1.APIResourceList
	if err := json.Unmarshal(cw.body.Bytes(), &list); err != nil {
		cw.flushUnchanged()
		return
	}

	parentCtx := FromRequest(req.Context())
	match := apisGroupVersionRE.FindStringSubmatch(req.URL.Path)
	group := match[1]

	kept := list.APIResources[:0]
	for _, r := range list.APIResources {
		// Subresources (e.g. "organizations/status") inherit their owner's
		// visibility — strip the suffix before lookup.
		base := r.Name
		if i := strings.IndexByte(base, '/'); i >= 0 {
			base = base[:i]
		}
		if registry.IsVisible(schema.GroupResource{Group: group, Resource: base}, parentCtx) {
			kept = append(kept, r)
		}
	}
	list.APIResources = kept

	cw.flushJSON(&list)
}

// filterAPIIndex filters /apis. Handles both the legacy APIGroupList and the
// modern aggregated APIGroupDiscoveryList based on Content-Type.
func filterAPIIndex(w http.ResponseWriter, req *http.Request, next http.Handler, registry *Registry) {
	cw := newCaptureWriter(w)
	next.ServeHTTP(cw, req)

	if !shouldFilter(cw) {
		cw.flushUnchanged()
		return
	}

	parentCtx := FromRequest(req.Context())

	// Detect aggregated discovery from the request Accept header. kubectl sends
	// Accept: application/json;...;as=APIGroupDiscoveryList and a conformant
	// server echoes it in Content-Type — but checking the request is more
	// reliable since Milo returns plain application/json in the response.
	if isAggregatedDiscovery(req.Header.Get("Accept")) {
		var list apidiscoveryv2.APIGroupDiscoveryList
		if err := json.Unmarshal(cw.body.Bytes(), &list); err != nil {
			cw.flushUnchanged()
			return
		}
		filterAggregatedGroups(&list, registry, parentCtx)
		cw.flushJSON(&list)
		return
	}

	// Legacy APIGroupList — we can't tell from the index alone which
	// resources live in each group, so we leave the group list intact. The
	// per-(group,version) request will be filtered by filterAPIResourceList.
	cw.flushUnchanged()
}

func filterAggregatedGroups(list *apidiscoveryv2.APIGroupDiscoveryList, registry *Registry, parentCtx ParentContext) {
	keptGroups := list.Items[:0]
	for _, group := range list.Items {
		keptVersions := group.Versions[:0]
		for _, version := range group.Versions {
			keptResources := version.Resources[:0]
			for _, r := range version.Resources {
				if registry.IsVisible(schema.GroupResource{Group: group.Name, Resource: r.Resource}, parentCtx) {
					keptResources = append(keptResources, r)
				}
			}
			if len(keptResources) > 0 {
				version.Resources = keptResources
				keptVersions = append(keptVersions, version)
			}
		}
		if len(keptVersions) > 0 {
			group.Versions = keptVersions
			keptGroups = append(keptGroups, group)
		}
	}
	list.Items = keptGroups
}

func isAggregatedDiscovery(ct string) bool {
	return strings.Contains(ct, "as=APIGroupDiscoveryList")
}

func shouldFilter(cw *captureWriter) bool {
	if cw.status != http.StatusOK && cw.status != 0 {
		return false
	}
	ct := cw.Header().Get("Content-Type")
	return strings.Contains(ct, "application/json")
}

// captureWriter buffers the downstream response so we can rewrite it.
type captureWriter struct {
	dst    http.ResponseWriter
	body   bytes.Buffer
	status int
}

func newCaptureWriter(dst http.ResponseWriter) *captureWriter {
	return &captureWriter{dst: dst}
}

func (c *captureWriter) Header() http.Header { return c.dst.Header() }

func (c *captureWriter) WriteHeader(s int) { c.status = s }

func (c *captureWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(p)
}

// flushUnchanged writes the captured response to the real ResponseWriter
// verbatim.
func (c *captureWriter) flushUnchanged() {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	c.dst.WriteHeader(c.status)
	if _, err := c.dst.Write(c.body.Bytes()); err != nil {
		klog.V(4).ErrorS(err, "discovery filter: writing passthrough response")
	}
}

// flushJSON serializes obj as JSON and writes it as the response, replacing
// any captured body. Headers other than Content-Length are preserved.
func (c *captureWriter) flushJSON(obj any) {
	out, err := json.Marshal(obj)
	if err != nil {
		klog.ErrorS(err, "discovery filter: re-encoding filtered response")
		c.flushUnchanged()
		return
	}
	c.dst.Header().Set("Content-Length", strconv.Itoa(len(out)))
	if c.status == 0 {
		c.status = http.StatusOK
	}
	c.dst.WriteHeader(c.status)
	if _, err := c.dst.Write(out); err != nil {
		klog.V(4).ErrorS(err, "discovery filter: writing filtered response")
	}
}
