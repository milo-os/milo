//go:build scale

package scale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"

	rmv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

const rmAPIBase = "/apis/resourcemanager.miloapis.com/v1alpha1"

const (
	workerCount = 24
	mib         = 1024 * 1024
)

func projectCount() int {
	if s := os.Getenv("SCALE_PROJECTS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return 20
}

// identity.miloapis.com is backed by Zitadel, which is not deployed in the
// scale test environment, and returns 503 on all requests.
var skipGroups = map[string]struct{}{"identity.miloapis.com": {}}

// --- snapshots ---

type memorySnapshot struct {
	heapInuse  float64
	sys        float64
	goroutines float64
}

func (m memorySnapshot) String() string {
	return fmt.Sprintf("heap=%.1fMiB sys=%.1fMiB goroutines=%.0f",
		m.heapInuse/mib, m.sys/mib, m.goroutines)
}

func (m memorySnapshot) sub(other memorySnapshot) memorySnapshot {
	return memorySnapshot{
		heapInuse:  m.heapInuse - other.heapInuse,
		sys:        m.sys - other.sys,
		goroutines: m.goroutines - other.goroutines,
	}
}

func (m memorySnapshot) div(n int) memorySnapshot {
	f := float64(n)
	return memorySnapshot{m.heapInuse / f, m.sys / f, m.goroutines / f}
}

type etcdSnapshot struct{ watchers float64 }

func (e etcdSnapshot) String() string                  { return fmt.Sprintf("watchers=%.0f", e.watchers) }
func (e etcdSnapshot) sub(o etcdSnapshot) etcdSnapshot { return etcdSnapshot{e.watchers - o.watchers} }
func (e etcdSnapshot) div(n int) etcdSnapshot          { return etcdSnapshot{e.watchers / float64(n)} }

type clusterSnapshot struct {
	apiserver memorySnapshot
	etcd      etcdSnapshot
}

func (c clusterSnapshot) String() string {
	return fmt.Sprintf("apiserver[%s] etcd[%s]", c.apiserver, c.etcd)
}

func (c clusterSnapshot) sub(other clusterSnapshot) clusterSnapshot {
	return clusterSnapshot{c.apiserver.sub(other.apiserver), c.etcd.sub(other.etcd)}
}

func (c clusterSnapshot) div(n int) clusterSnapshot {
	return clusterSnapshot{c.apiserver.div(n), c.etcd.div(n)}
}

type latencyReport struct {
	count, errors      int
	p50, p90, p99, max time.Duration
}

func (l latencyReport) String() string {
	return fmt.Sprintf("n=%d errors=%d p50=%s p90=%s p99=%s max=%s",
		l.count, l.errors,
		l.p50.Round(time.Millisecond), l.p90.Round(time.Millisecond),
		l.p99.Round(time.Millisecond), l.max.Round(time.Millisecond))
}

// --- test ---

func TestProjectControlPlaneOverhead(t *testing.T) {
	s := connect(t)
	etcd := portForward(t, "milo-system", "svc/etcd", 2379, "http", "")
	nProjects := projectCount()

	// Warn once if etcd watcher metric is unavailable so callers know whether
	// to trust the etcd watcher readings (which show 0 when the metric is absent).
	if _, err := sampleEtcd(context.Background(), etcd); err != nil {
		t.Logf("warning: etcd watcher metric unavailable — etcd readings will show 0 (%v)", err)
	}

	gvrs := mustDiscoverGVRs(t, s)
	t.Logf("resources: %d listable types", len(gvrs))

	globalPaths := buildGlobalPaths(gvrs)

	before := waitUntilStable(t, s, etcd, "before")
	t.Logf("latency before: %s", measureLatency(s, globalPaths))
	saveProfiles(t, s, nProjects, "before")

	runPrefix := fmt.Sprintf("scale-%d", time.Now().Unix())
	orgName := runPrefix
	projectIDs := makeProjectIDs(nProjects, runPrefix)

	t.Cleanup(func() {
		ctx := context.Background()
		for _, id := range projectIDs {
			if err := s.delete(ctx, rmAPIBase+"/projects/"+id); err != nil {
				t.Logf("cleanup: delete project %s: %v", id, err)
			}
		}
		if err := s.delete(ctx, rmAPIBase+"/organizations/"+orgName); err != nil {
			t.Logf("cleanup: delete org %s: %v", orgName, err)
		}
	})

	t.Logf("bootstrap: creating org=%s and %d projects", orgName, nProjects)
	createOrg(t, s, orgName)
	for _, id := range projectIDs {
		createProject(t, s, id, orgName)
	}
	bootstrapProjects(t, s, projectIDs, gvrs)

	after := waitUntilStable(t, s, etcd, "after")
	t.Logf("latency after:  %s", measureLatency(s, globalPaths))
	saveProfiles(t, s, nProjects, "after")

	delta := after.sub(before)
	t.Logf("results: projects=%d resources_per_project=%d", nProjects, len(gvrs))
	t.Logf("  before:      %s", before)
	t.Logf("  after:       %s", after)
	t.Logf("  per-project: %s", delta.div(nProjects))
}

// --- bootstrap ---

func makeProjectIDs(n int, prefix string) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("%s-p-%03d", prefix, i)
	}
	return ids
}

func createOrg(t *testing.T, s *endpoint, name string) {
	t.Helper()
	org := rmv1alpha1.Organization{
		TypeMeta:   metav1.TypeMeta{APIVersion: "resourcemanager.miloapis.com/v1alpha1", Kind: "Organization"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       rmv1alpha1.OrganizationSpec{Type: "Standard"},
	}
	body, err := json.Marshal(org)
	if err != nil {
		t.Fatalf("marshal org: %v", err)
	}
	if _, err := s.post(context.Background(), rmAPIBase+"/organizations", body); err != nil {
		t.Fatalf("create org %s: %v", name, err)
	}
}

func createProject(t *testing.T, s *endpoint, name, orgName string) {
	t.Helper()
	proj := rmv1alpha1.Project{
		TypeMeta:   metav1.TypeMeta{APIVersion: "resourcemanager.miloapis.com/v1alpha1", Kind: "Project"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: rmv1alpha1.ProjectSpec{
			OwnerRef: rmv1alpha1.OwnerReference{Kind: "Organization", Name: orgName},
		},
	}
	body, err := json.Marshal(proj)
	if err != nil {
		t.Fatalf("marshal project: %v", err)
	}
	// Projects must be created via the org-scoped path so that
	// OrganizationContextHandler injects the required parent Extra fields
	// (iam.miloapis.com/parent-*) that the admission webhook enforces.
	path := fmt.Sprintf("/apis/resourcemanager.miloapis.com/v1alpha1/organizations/%s/control-plane%s/projects", orgName, rmAPIBase)
	if _, err := s.post(context.Background(), path, body); err != nil {
		t.Fatalf("create project %s: %v", name, err)
	}
}

func bootstrapProjects(t *testing.T, s *endpoint, projectIDs []string, gvrs []schema.GroupVersionResource) {
	t.Helper()
	total := int32(len(projectIDs) * len(gvrs))
	var done atomic.Int32

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(workerCount)

	for _, pid := range projectIDs {
		for _, gvr := range gvrs {
			eg.Go(func() error {
				if err := initProjectGVR(ctx, s, pid, gvr); err != nil {
					return fmt.Errorf("project=%s resource=%v: %w", pid, gvr, err)
				}
				if n := done.Add(1); total >= 10 && n%(total/10) == 0 {
					t.Logf("bootstrap: %d/%d", n, total)
				}
				return nil
			})
		}
	}
	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}
}

// initProjectGVR lists one resource in a project's control plane, triggering
// projectMux child creation for that (project, GVR) pair.
//
// 429/503 (watchcache reinit) are retried indefinitely.
// All other errors (including 500) are retried up to maxTransportRetries times.
func initProjectGVR(ctx context.Context, s *endpoint, projectID string, gvr schema.GroupVersionResource) error {
	const maxTransportRetries = 10
	base := "/apis/resourcemanager.miloapis.com/v1alpha1/projects/" + projectID + "/control-plane"
	backoff := 200 * time.Millisecond
	transportRetries := 0
	for {
		_, err := s.get(ctx, gvrPath(base, gvr))
		if err == nil {
			return nil
		}
		var se *httpStatusError
		if errors.As(err, &se) {
			switch se.code {
			case http.StatusServiceUnavailable, http.StatusTooManyRequests:
				transportRetries = 0 // watchcache reinit — retry indefinitely
			default:
				if transportRetries++; transportRetries > maxTransportRetries {
					return fmt.Errorf("listing %v: %w", gvr, err)
				}
			}
		} else {
			if transportRetries++; transportRetries > maxTransportRetries {
				return fmt.Errorf("listing %v: %w", gvr, err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff = min(backoff*2, 5*time.Second)
		}
	}
}

// --- measurement ---

// waitUntilStable polls until apiserver goroutines settle, returning a cluster
// snapshot. Stability is 3 consecutive samples within 100 goroutines of each
// other; times out after 8 minutes.
func waitUntilStable(t *testing.T, s *endpoint, etcd *endpoint, label string) clusterSnapshot {
	t.Helper()
	const (
		interval  = 10 * time.Second
		threshold = 100
		window    = 3
		timeout   = 8 * time.Minute
	)
	deadline := time.Now().Add(timeout)
	var prev float64
	streak := 0
	var snap clusterSnapshot
	for {
		var err error
		snap, err = sampleCluster(context.Background(), s, etcd)
		if err != nil {
			t.Logf("[%s] sample error: %v", label, err)
			time.Sleep(interval)
			continue
		}
		delta := snap.apiserver.goroutines - prev
		if delta < 0 {
			delta = -delta
		}
		t.Logf("[%s] Δgoroutines=%.0f %s", label, delta, snap)
		if prev > 0 && delta < threshold {
			if streak++; streak >= window {
				return snap
			}
		} else {
			streak = 0
		}
		prev = snap.apiserver.goroutines
		if time.Now().After(deadline) {
			t.Logf("[%s] goroutines still growing after %s — measurement may be premature", label, timeout)
			return snap
		}
		time.Sleep(interval)
	}
}

// measureLatency makes one sequential pass through paths and returns latency percentiles
func measureLatency(s *endpoint, paths []string) latencyReport {
	ctx := context.Background()
	var samples []time.Duration
	var errCount int
	for _, path := range paths {
		t0 := time.Now()
		_, err := s.get(ctx, path)
		if err != nil {
			errCount++
		} else {
			samples = append(samples, time.Since(t0))
		}
	}
	if len(samples) == 0 {
		return latencyReport{errors: errCount}
	}
	slices.Sort(samples)
	n := len(samples)
	return latencyReport{
		count:  n,
		errors: errCount,
		p50:    samples[(n-1)*50/100],
		p90:    samples[(n-1)*90/100],
		p99:    samples[(n-1)*99/100],
		max:    samples[n-1],
	}
}

// --- sampling ---

func sampleCluster(ctx context.Context, s *endpoint, etcd *endpoint) (clusterSnapshot, error) {
	api, err := sampleMemory(ctx, s)
	if err != nil {
		return clusterSnapshot{}, fmt.Errorf("apiserver: %w", err)
	}
	var e etcdSnapshot
	if etcd != nil {
		if v, err := sampleEtcd(ctx, etcd); err == nil {
			e = v
		}
	}
	return clusterSnapshot{apiserver: api, etcd: e}, nil
}

func sampleMemory(ctx context.Context, e *endpoint) (memorySnapshot, error) {
	text, err := e.get(ctx, "/metrics")
	if err != nil {
		return memorySnapshot{}, err
	}
	heap, err := promGauge(text, "go_memstats_heap_inuse_bytes")
	if err != nil {
		return memorySnapshot{}, err
	}
	sys, err := promGauge(text, "go_memstats_sys_bytes")
	if err != nil {
		return memorySnapshot{}, err
	}
	goroutines, err := promGauge(text, "go_goroutines")
	if err != nil {
		return memorySnapshot{}, err
	}
	return memorySnapshot{heapInuse: heap, sys: sys, goroutines: goroutines}, nil
}

func sampleEtcd(ctx context.Context, e *endpoint) (etcdSnapshot, error) {
	text, err := e.get(ctx, "/metrics")
	if err != nil {
		return etcdSnapshot{}, err
	}
	watchers, err := promGauge(text, "etcd_debugging_mvcc_watcher_total")
	if err != nil {
		return etcdSnapshot{}, err
	}
	return etcdSnapshot{watchers: watchers}, nil
}

// --- discovery ---

func mustDiscoverGVRs(t *testing.T, s *endpoint) []schema.GroupVersionResource {
	t.Helper()
	dc, err := discovery.NewDiscoveryClientForConfig(&rest.Config{
		Host:            s.url,
		BearerToken:     s.token,
		TLSClientConfig: rest.TLSClientConfig{Insecure: true},
	})
	if err != nil {
		t.Fatalf("discovery client: %v", err)
	}
	lists, err := dc.ServerPreferredResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		t.Fatalf("server preferred resources: %v", err)
	}
	var gvrs []schema.GroupVersionResource
	for _, list := range lists {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		if _, skip := skipGroups[gv.Group]; skip {
			continue
		}
		for _, r := range list.APIResources {
			if !strings.Contains(r.Name, "/") && slices.Contains(r.Verbs, "list") {
				gvrs = append(gvrs, gv.WithResource(r.Name))
			}
		}
	}
	return gvrs
}

func buildGlobalPaths(gvrs []schema.GroupVersionResource) []string {
	paths := make([]string, len(gvrs))
	for i, gvr := range gvrs {
		paths[i] = gvrPath("", gvr)
	}
	return paths
}

func gvrPath(base string, gvr schema.GroupVersionResource) string {
	if gvr.Group == "" {
		return fmt.Sprintf("%s/api/%s/%s", base, gvr.Version, gvr.Resource)
	}
	return fmt.Sprintf("%s/apis/%s/%s/%s", base, gvr.Group, gvr.Version, gvr.Resource)
}

// --- profiles ---

func saveProfiles(t *testing.T, s *endpoint, nProjects int, label string) {
	t.Helper()
	outDir := filepath.Join(repoRoot(t), "dev", "profiles")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("profiles mkdir: %v", err)
	}
	for _, p := range []struct{ path, name string }{
		{"/debug/pprof/heap?gc=1", fmt.Sprintf("heap-%dp-%s.pb.gz", nProjects, label)},
		{"/debug/pprof/goroutine?debug=1", fmt.Sprintf("goroutine-%dp-%s.txt", nProjects, label)},
	} {
		body, err := s.get(context.Background(), p.path)
		if err != nil {
			t.Logf("profiles: fetch %s: %v", p.name, err)
			continue
		}
		dst := filepath.Join(outDir, p.name)
		if err := os.WriteFile(dst, []byte(body), 0o644); err != nil {
			t.Logf("profiles: write %s: %v", p.name, err)
			continue
		}
		t.Logf("profiles: saved %s", dst)
	}
}

// --- helpers ---

func promGauge(text, metric string) (float64, error) {
	for _, line := range strings.Split(text, "\n") {
		if val, ok := strings.CutPrefix(line, metric+" "); ok {
			return strconv.ParseFloat(strings.TrimSpace(val), 64)
		}
	}
	return 0, fmt.Errorf("metric %s not found", metric)
}
