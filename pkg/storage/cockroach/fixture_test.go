package cockroach

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	"sigs.k8s.io/yaml"

	"k8s.io/apimachinery/pkg/api/apitesting"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/apis/example"
	examplev1 "k8s.io/apiserver/pkg/apis/example/v1"
	storagetesting "k8s.io/apiserver/pkg/storage/testing"
	"k8s.io/apiserver/pkg/storage/value"
)

// The tests need a CockroachDB server:
//
//	MILO_COCKROACH_TEST_DSN set -> that server, such as
//	                               postgresql://root@localhost:26257/defaultdb?sslmode=disable
//	otherwise, Docker available -> one testcontainers server for the package
//	neither                     -> skip, or fail if MILO_COCKROACH_TEST_REQUIRE is set
//
// Either way the tests create and migrate a database of their own on it and
// drop it afterwards.
const (
	testDSNEnv     = "MILO_COCKROACH_TEST_DSN"
	testRequireEnv = "MILO_COCKROACH_TEST_REQUIRE"
	testImageEnv   = "MILO_COCKROACH_TEST_IMAGE"
	// testNoGroupCommitEnv runs the shared backend, and with it the upstream
	// suite, with GroupCommit off.
	testNoGroupCommitEnv = "MILO_COCKROACH_TEST_NO_GROUP_COMMIT"
)

// clusterHelmRelease deploys the CockroachDB the test environments use. Its
// chart version is the one place the version is pinned.
const clusterHelmRelease = "../../../config/dependencies/cockroachdb/cluster-helmrelease.yaml"

// testImage is the CockroachDB image the test environments deploy, unless
// MILO_COCKROACH_TEST_IMAGE names another. A cockroachdb-chart release has the
// version of the CockroachDB it deploys.
func testImage() (string, error) {
	if img := os.Getenv(testImageEnv); img != "" {
		return img, nil
	}
	data, err := os.ReadFile(clusterHelmRelease)
	if err != nil {
		return "", err
	}
	var hr struct {
		Spec struct {
			Chart struct {
				Spec struct {
					Chart   string `json:"chart"`
					Version string `json:"version"`
				} `json:"spec"`
			} `json:"chart"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(data, &hr); err != nil {
		return "", fmt.Errorf("read %s: %w", clusterHelmRelease, err)
	}
	if hr.Spec.Chart.Spec.Chart != "cockroachdb-chart" || hr.Spec.Chart.Spec.Version == "" {
		return "", fmt.Errorf("%s does not pin a cockroachdb-chart version", clusterHelmRelease)
	}
	return "cockroachdb/cockroach:v" + hr.Spec.Chart.Spec.Version, nil
}

var (
	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme)
)

func init() {
	metav1.AddToGroupVersion(scheme, metav1.SchemeGroupVersion)
	utilruntime.Must(example.AddToScheme(scheme))
	utilruntime.Must(examplev1.AddToScheme(scheme))
}

// The package's database, set up once by TestMain.
var (
	backend    *Backend
	backendErr error
	baseDSN    string // the server; empty when there is none
	testDBDSN  string
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	baseDSN = os.Getenv(testDSNEnv)
	if baseDSN == "" {
		var stop func()
		baseDSN, stop = startCockroach(ctx)
		defer stop()
	}
	if baseDSN == "" {
		os.Exit(m.Run())
	}
	var drop func()
	testDBDSN, drop, backendErr = createDatabase(ctx, baseDSN, fmt.Sprintf("milo_storage_test_%d", time.Now().UnixNano()))
	if backendErr == nil {
		if backendErr = Migrate(ctx, testDBDSN); backendErr == nil {
			opts := []Option{testOptions()}
			if os.Getenv(testNoGroupCommitEnv) != "" {
				opts = append(opts, GroupCommit(false))
			}
			backend, backendErr = Open(ctx, testDBDSN, opts...)
		}
	}
	code := m.Run()
	if backend != nil {
		backend.Close()
	}
	if drop != nil {
		drop()
	}
	if code != 0 {
		os.Exit(code)
	}
}

func testOptions() Option {
	return func(o *options) {
		// A request for a revision the feed has not delivered waits for it, so
		// no test depends on how fast the feed is. Tests that kill the feed
		// wait out its retry.
		o.hub.awaitTimeout = time.Hour
		o.feedRetry = 50 * time.Millisecond
		o.hub.tickInterval = 50 * time.Millisecond
		// Admission and sweeping have tests of their own and stay out of the
		// way of the rest, so tests sweep only when they mean to.
		o.projectWrites, o.transactions = 1<<16, 1<<16
		o.sweepInterval = time.Hour
	}
}

// startCockroach starts a single-node server in Docker and returns its DSN and
// a function that removes it. The DSN is empty when Docker is not available.
func startCockroach(ctx context.Context) (string, func()) {
	img, err := testImage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cockroach storage tests: %v\n", err)
		return "", func() {}
	}
	c, err := cockroachdb.Run(ctx, img, cockroachdb.WithInsecure())
	if err != nil {
		fmt.Fprintf(os.Stderr, "cockroach storage tests: no %s and no Docker server (%v)\n", testDSNEnv, err)
		return "", func() {}
	}
	stop := func() { _ = testcontainers.TerminateContainer(c) }
	// The module's ConnectionString names a registered database/sql config, not a URL.
	host, err := c.Host(ctx)
	if err == nil {
		port, perr := c.MappedPort(ctx, "26257/tcp")
		if perr == nil {
			return fmt.Sprintf("postgresql://root@%s:%s/defaultdb?sslmode=disable", host, port.Port()), stop
		}
		err = perr
	}
	stop()
	fmt.Fprintf(os.Stderr, "cockroach storage tests: %v\n", err)
	return "", func() {}
}

// createDatabase creates database name on the server at base and returns its
// DSN and a function that drops it.
func createDatabase(ctx context.Context, base, name string) (string, func(), error) {
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		return "", nil, err
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return "", nil, err
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", nil, err
	}
	u.Path = "/" + name
	drop := func() {
		conn, err := pgx.Connect(context.Background(), base)
		if err != nil {
			return
		}
		defer conn.Close(context.Background())
		_, _ = conn.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" CASCADE")
	}
	return u.String(), drop, nil
}

func testBackend(t *testing.T) *Backend {
	t.Helper()
	if baseDSN == "" {
		if os.Getenv(testRequireEnv) != "" {
			t.Fatalf("no CockroachDB: %s is not set and Docker is not available, and %s requires these tests to run", testDSNEnv, testRequireEnv)
		}
		t.Skipf("no CockroachDB: set %s or make Docker available to run the storage tests", testDSNEnv)
	}
	require.NoError(t, backendErr)
	return backend
}

// testStore is a store in a project of its own, so tests can share a database,
// whose transformer the upstream tests can replace while it runs.
type testStore struct {
	*store
	swap *swappableTransformer
}

func (s testStore) UpdatePrefixTransformer(modifier storagetesting.PrefixTransformerModifier) func() {
	return s.UpdateTransformer(func(original value.Transformer) value.Transformer {
		// The modifier gets a copy, as in etcd3's tests, so its changes end with revert.
		transformer := *original.(*storagetesting.PrefixTransformer)
		return modifier(&transformer)
	})
}

func (s testStore) UpdateTransformer(modifier storagetesting.TransformerModifier) func() {
	s.swap.mu.Lock()
	defer s.swap.mu.Unlock()
	original := s.swap.current
	s.swap.current = modifier(original)
	return func() {
		s.swap.mu.Lock()
		defer s.swap.mu.Unlock()
		s.swap.current = original
	}
}

// swappableTransformer delegates to a transformer that can change.
type swappableTransformer struct {
	mu      sync.RWMutex
	current value.Transformer
}

func (t *swappableTransformer) TransformFromStorage(ctx context.Context, data []byte, dataCtx value.Context) ([]byte, bool, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.current.TransformFromStorage(ctx, data, dataCtx)
}

func (t *swappableTransformer) TransformToStorage(ctx context.Context, data []byte, dataCtx value.Context) ([]byte, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.current.TransformToStorage(ctx, data, dataCtx)
}

func testCodec() runtime.Codec { return apitesting.TestCodec(codecs, examplev1.SchemeGroupVersion) }

func testConfig(project string) Config {
	return Config{
		Project:        project,
		ResourcePrefix: "/pods/",
		GroupResource:  schema.GroupResource{Resource: "pods"},
		Codec:          testCodec(),
		Transformer:    storagetesting.NewPrefixTransformer([]byte("test!"), false),
		NewFunc:        func() runtime.Object { return &example.Pod{} },
	}
}

func newTestStore(t *testing.T, b *Backend, c Config) testStore {
	t.Helper()
	swap := &swappableTransformer{current: c.Transformer}
	c.Transformer = swap
	s, err := newStore(b, c)
	require.NoError(t, err)
	return testStore{store: s, swap: swap}
}

// testSetup is a store on the shared backend, in a project of its own, with
// the test's changes to its config.
func testSetup(t *testing.T, changes ...func(*Config)) (context.Context, testStore) {
	t.Helper()
	c := testConfig(fmt.Sprintf("test-%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano()))
	for _, change := range changes {
		change(&c)
	}
	return t.Context(), newTestStore(t, testBackend(t), c)
}

// otherReplica is s's project on a backend of its own, as another API server
// replica would see it.
func otherReplica(t *testing.T, s testStore, opts ...Option) testStore {
	t.Helper()
	b, err := Open(t.Context(), testDBDSN, append([]Option{testOptions()}, opts...)...)
	require.NoError(t, err)
	t.Cleanup(b.Close)
	// Writes before the feed's baseline would predate everything it knows.
	require.NoError(t, b.hub.waitReady(t.Context()))
	return newTestStore(t, b, testConfig(s.project))
}

// sweeping is s's project on a replica that sweeps often, for tests that wait
// out a TTL.
func sweeping(t *testing.T, s testStore) testStore {
	t.Helper()
	return otherReplica(t, s, func(o *options) { o.sweepInterval = 100 * time.Millisecond })
}

func createPod(ctx context.Context, t *testing.T, s testStore, name string) *example.Pod {
	t.Helper()
	pod := &example.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"}}
	out := &example.Pod{}
	require.NoError(t, s.Create(ctx, "/pods/test-ns/"+name, pod, out, 0))
	return out
}

// nextEvent returns w's next event. It fails the test if none arrives within
// wait.ForeverTestTimeout, as upstream's storage tests do, so an event the
// hub never releases shows as a failure rather than a hung run.
func nextEvent(t *testing.T, w watch.Interface) watch.Event {
	t.Helper()
	select {
	case e, ok := <-w.ResultChan():
		require.True(t, ok, "watch closed")
		return e
	case <-time.After(wait.ForeverTestTimeout):
		t.Fatalf("no watch event within %s", wait.ForeverTestTimeout)
		return watch.Event{}
	}
}

// checkStorageInvariants verifies that a key's stored value went through the
// transformer.
func checkStorageInvariants(s testStore) storagetesting.KeyValidation {
	return func(ctx context.Context, t *testing.T, key string) {
		var v []byte
		require.NoError(t, s.b.pool.QueryRow(ctx, `SELECT value FROM objects WHERE project = $1 AND key = $2`, s.project, key).Scan(&v))
		require.True(t, strings.HasPrefix(string(v), "test!"), "value of %q was not transformed: %q", key, v)
	}
}

// increaseRV advances the project's revision by writing a key outside the
// resource, as etcd's tests do. Every revision belongs to a write, which the
// hub relies on.
func increaseRV(s testStore) storagetesting.IncreaseRVFunc {
	return func(ctx context.Context, t *testing.T) int64 {
		res, err := s.b.committer.commit(ctx, s.project, createWrite(fmt.Sprintf("/increase-rv/%d", time.Now().UnixNano()), []byte("rv"), 0))
		require.NoError(t, err)
		require.True(t, res.written)
		return res.rev
	}
}

// compaction stands in for etcd compaction. It forgets the recent events
// below rv and the commit times up to rv, and advances the revision as etcd's
// compaction write does. A revision reads as of the next one's commit time
// too, so rv stays readable and nothing before it does.
func compaction(s testStore) storagetesting.Compaction {
	return func(ctx context.Context, t *testing.T, resourceVersion string) {
		rv, err := s.versioner.ParseResourceVersion(resourceVersion)
		require.NoError(t, err)
		h := s.b.hub
		_, err = h.waitReleased(ctx, s.project, int64(rv))
		require.NoError(t, err)
		h.mu.Lock()
		ring := &h.projectLocked(s.project).seq.log
		ring.drop(max(int(int64(rv)-ring.oldest()), 0))
		h.mu.Unlock()
		for rev := range int64(rv) + 1 {
			s.b.revTS.forget(s.project, rev)
		}
		increaseRV(s)(ctx, t)
	}
}

func parseRV(t *testing.T, rv string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(rv, 10, 64)
	require.NoError(t, err)
	return n
}

func newPod(name string) *example.Pod {
	return &example.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"}}
}
