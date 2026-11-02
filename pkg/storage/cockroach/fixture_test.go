package cockroach

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	"sigs.k8s.io/yaml"
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
			backend, backendErr = Open(ctx, testDBDSN)
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
