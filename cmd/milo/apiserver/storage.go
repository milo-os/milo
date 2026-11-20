package app

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/klog/v2"

	projectstorage "go.miloapis.com/milo/internal/apiserver/storage/project"
	"go.miloapis.com/milo/pkg/storage/cockroach"
)

const (
	storageEngineEtcd        = "etcd"
	storageEngineCockroachDB = "cockroachdb"
)

type storageOptions struct {
	engine        string
	dsn           string
	maxConns      int32
	groupCommit   bool
	projectWrites int
	transactions  int
	admissionWait time.Duration
	replayWindow  time.Duration
	replayEvents  int
	lagReadiness  time.Duration
	migrate       bool
}

func newStorageOptions() storageOptions {
	return storageOptions{
		engine:        storageEngineEtcd,
		maxConns:      64,
		groupCommit:   true,
		admissionWait: time.Second,
		replayWindow:  5 * time.Minute,
		replayEvents:  1024,
	}
}

func (o *storageOptions) addFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.engine, "storage-engine", o.engine, "Where the API server stores resources: etcd, or cockroachdb (experimental). Before switching to cockroachdb, apply its schema with \"milo storage migrate\" or --cockroachdb-migrate-on-start.")
	fs.StringVar(&o.dsn, "cockroachdb-dsn", o.dsn, "Connection string for the CockroachDB database, such as postgresql://milo@cockroachdb:26257/milo?sslmode=verify-full. Required with --storage-engine=cockroachdb.")
	fs.BoolVar(&o.migrate, "cockroachdb-migrate-on-start", o.migrate, "Apply pending schema migrations when the server starts, as \"milo storage migrate\" does. Servers starting together take turns. The database user then needs permission to change the schema and cluster settings.")
	fs.Int32Var(&o.maxConns, "cockroachdb-max-conns", o.maxConns, "Maximum number of connections the server opens to CockroachDB.")

	fs.BoolVar(&o.groupCommit, "cockroachdb-group-commit", o.groupCommit, "Commit concurrent writes to the same project in one transaction, which raises how fast a busy project can write. A write with nothing to batch commits right away. Turn this off to rule batching out while debugging.")
	fs.IntVar(&o.projectWrites, "cockroachdb-max-project-writes-in-flight", o.projectWrites, "Maximum number of writes to one project in progress at a time. Further writes wait up to --cockroachdb-write-admission-wait, then fail with 429 Too Many Requests. If unset, the limit is 256.")
	fs.IntVar(&o.transactions, "cockroachdb-max-transactions-in-flight", o.transactions, "Maximum number of write transactions in progress at a time on this server. With group commit, one transaction can carry many writes. Further transactions wait up to --cockroachdb-write-admission-wait, then fail with 429 Too Many Requests. Defaults to three quarters of --cockroachdb-max-conns, 48 with the default pool of 64.")
	fs.DurationVar(&o.admissionWait, "cockroachdb-write-admission-wait", o.admissionWait, "How long a write waits for a free slot under either in-flight limit before it fails with 429 Too Many Requests.")

	fs.DurationVar(&o.replayWindow, "cockroachdb-watch-replay-window", o.replayWindow, "How far back a watch can resume. A client asking for an older resource version gets 410 Gone and lists again, and reads at a past resource version reach back the same distance. Must stay under half the objects table's garbage-collection TTL (1 hour), so under 30 minutes. The server checks this when it starts.")
	fs.IntVar(&o.replayEvents, "cockroachdb-watch-replay-events", o.replayEvents, "Maximum number of recent events kept per project for resuming watches, within the replay window.")
	fs.DurationVar(&o.lagReadiness, "cockroachdb-watch-lag-readiness", o.lagReadiness, "Report the server not ready while watches lag further behind writes than this. Lag usually comes from the database, so this can take out every apiserver at once. If unset, readiness ignores lag, which still shows in the metrics.")
}

func (o storageOptions) validate() error {
	switch o.engine {
	case storageEngineEtcd:
		return nil
	case storageEngineCockroachDB:
		if o.dsn == "" {
			return fmt.Errorf("--cockroachdb-dsn is required with --storage-engine=%s", storageEngineCockroachDB)
		}
		if o.maxConns < 1 {
			return fmt.Errorf("--cockroachdb-max-conns must be at least 1, got %d", o.maxConns)
		}
		if o.projectWrites < 0 || o.transactions < 0 {
			return fmt.Errorf("CockroachDB write limits cannot be negative")
		}
		if o.admissionWait <= 0 || o.replayWindow <= 0 || o.replayEvents < 1 || o.lagReadiness < 0 {
			return fmt.Errorf("--cockroachdb-write-admission-wait, --cockroachdb-watch-replay-window and --cockroachdb-watch-replay-events must be positive, and --cockroachdb-watch-lag-readiness cannot be negative")
		}
		return nil
	default:
		return fmt.Errorf("--storage-engine must be %s or %s, got %q", storageEngineEtcd, storageEngineCockroachDB, o.engine)
	}
}

func (o storageOptions) withoutEtcdServersError(errs []error) []error {
	if o.engine != storageEngineCockroachDB {
		return errs
	}
	return slices.DeleteFunc(errs, func(err error) bool {
		return strings.Contains(err.Error(), "--etcd-servers must be specified")
	})
}

func (o storageOptions) open(ctx context.Context) (generic.StorageDecorator, func(), healthz.HealthChecker, error) {
	if o.engine != storageEngineCockroachDB {
		return nil, func() {}, nil, nil
	}
	opts := []cockroach.Option{
		cockroach.MaxConns(o.maxConns),
		cockroach.MaxProjectWritesInFlight(o.projectWrites),
		cockroach.MaxTransactionsInFlight(o.transactions),
		cockroach.AdmissionWait(o.admissionWait),
		cockroach.WatchReplay(o.replayWindow, o.replayEvents),
		cockroach.WatchLagReadiness(o.lagReadiness),
		cockroach.GroupCommit(o.groupCommit),
	}
	if o.migrate {
		opts = append(opts, cockroach.MigrateOnOpen())
	}
	backend, err := cockroach.Open(ctx, o.dsn, opts...)
	if err != nil {
		return nil, nil, nil, err
	}
	klog.InfoS("Storing resources in CockroachDB", "maxConns", o.maxConns, "groupCommit", o.groupCommit, "migrateOnStart", o.migrate,
		"replayWindow", o.replayWindow, "replayEvents", o.replayEvents, "lagReadiness", o.lagReadiness)
	check := healthz.NamedCheck("cockroachdb", func(r *http.Request) error {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := backend.Ping(ctx); err != nil {
			return err
		}
		return backend.WatchReady()
	})
	return cockroach.Decorator(backend, projectstorage.ProjectFromPrefix), backend.Close, check, nil
}
