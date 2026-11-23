package cockroach

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"k8s.io/klog/v2"
)

// RemoveChangefeed is the [ChangefeedSink] that cancels the job.
const RemoveChangefeed = "none"

// ChangefeedSink has [Migrate] keep one changefeed job writing the objects
// table's changes to sink, a kafka:// URI, for API servers with [KafkaFeed]
// to read. [RemoveChangefeed] cancels the job. When the sink or the job's
// options change, Migrate replaces the job with one that carries on from
// where the old one got to.
func ChangefeedSink(sink Sink) MigrateOption {
	return func(o *migrateOptions) { o.sink = string(sink) }
}

// ChangefeedSinkConfig sets the job's kafka_sink_config, a JSON object of
// producer settings such as Flush. RequiredAcks defaults to ALL, so a message
// counts as sent only once every replica of its partition has it.
func ChangefeedSinkConfig(config string) MigrateOption {
	return func(o *migrateOptions) { o.sinkConfig = config }
}

// changefeedJobName is the objects table's job in changefeed_jobs.
const changefeedJobName = "objects"

// changefeedOptions are the job's options, in the order they're hashed: the
// messages every changefeed sends the hub, plus on_error = 'pause', which
// keeps the job's place through an error, and gc_protect_expires_after,
// which cancels a job paused that long, so it can't hold back garbage
// collection on objects for days.
var changefeedOptions = append(slices.Clone(feedOptions),
	"on_error = 'pause'",
	"gc_protect_expires_after = '1h'",
)

// jobOptions returns the job's options with sinkConfig as its
// kafka_sink_config. The config is encoded with its keys sorted, so the same
// settings written differently don't replace the job.
func jobOptions(sinkConfig string) ([]string, error) {
	config := map[string]any{}
	if sinkConfig != "" {
		if err := json.Unmarshal([]byte(sinkConfig), &config); err != nil {
			return nil, fmt.Errorf("the changefeed's kafka_sink_config must be a JSON object: %w", err)
		}
	}
	if _, ok := config["RequiredAcks"]; !ok {
		config["RequiredAcks"] = "ALL"
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	return append(slices.Clone(changefeedOptions), "kafka_sink_config = '"+strings.ReplaceAll(string(encoded), "'", "''")+"'"), nil
}

// changefeedJob is the job changefeed_jobs records, as CockroachDB reports it.
type changefeedJob struct {
	id       int64
	specHash []byte
	// status is empty when CockroachDB no longer lists the job.
	status    string
	highWater sql.NullString
	// destination is the broker and topic the job writes to.
	destination string
	// coversFrom is the timestamp the job's topic holds every change after.
	coversFrom string
}

func (j changefeedJob) live() bool {
	switch j.status {
	case "running", "pending", "paused", "pause-requested":
		return true
	}
	return false
}

// reconcileChangefeed makes the recorded job match sink and sinkConfig.
func reconcileChangefeed(ctx context.Context, db *sql.DB, sink, sinkConfig string) error {
	job, err := loadSettledChangefeedJob(ctx, db)
	if err != nil {
		return err
	}
	var recorded int64
	if job != nil {
		recorded = job.id
	}
	// A job that a failed run created but never recorded feeds nobody.
	if err := cancelChangefeedsExcept(ctx, db, recorded); err != nil {
		return err
	}
	if sink == RemoveChangefeed {
		if job == nil {
			return nil
		}
		if err := cancelLive(ctx, db, job); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx, `DELETE FROM changefeed_jobs WHERE name = $1`, changefeedJobName)
		klog.InfoS("Removed the objects changefeed job", "job", job.id)
		return err
	}
	options, err := jobOptions(sinkConfig)
	if err != nil {
		return err
	}
	hash, err := changefeedSpecHash(sink, options)
	if err != nil {
		return err
	}
	destination, err := changefeedDestination(sink)
	if err != nil {
		return err
	}
	if job != nil && job.live() && bytes.Equal(job.specHash, hash) {
		if job.status == "paused" {
			if _, err := db.ExecContext(ctx, fmt.Sprintf("RESUME JOB %d", job.id)); err != nil {
				return fmt.Errorf("resume changefeed job %d: %w", job.id, err)
			}
		}
		klog.InfoS("Keeping the objects changefeed job", "job", job.id, "status", job.status)
		return checkOnlyChangefeed(ctx, db, job.id)
	}
	var cursor, coversFrom string
	if job != nil {
		if err := cancelLive(ctx, db, job); err != nil {
			return err
		}
		cursor, coversFrom = job.successor(destination)
	}
	created, err := createChangefeed(ctx, db, sink, options, cursor)
	if err != nil && cursor != "" {
		klog.ErrorS(err, "Cannot carry the objects changefeed on from its previous job; starting it from now, so API servers reading it start over from the current state", "previous", job.id)
		cursor, coversFrom = "", ""
		created, err = createChangefeed(ctx, db, sink, options, cursor)
	}
	if err != nil {
		return err
	}
	if coversFrom == "" {
		coversFrom = created.cursor
	}
	if _, err := db.ExecContext(ctx, `UPSERT INTO changefeed_jobs (name, job_id, spec_hash, destination, covers_from) VALUES ($1, $2, $3, $4, $5)`,
		changefeedJobName, created.id, hash, destination, coversFrom); err != nil {
		return fmt.Errorf("record changefeed job %d: %w", created.id, err)
	}
	klog.InfoS("Created the objects changefeed job", "job", created.id, "cursor", created.cursor, "coversFrom", coversFrom)
	return checkOnlyChangefeed(ctx, db, created.id)
}

// successor returns where a job replacing j starts, and what its topic then
// covers, empty when that's only what follows the cursor. A job that carries
// on into the same topic covers what j's did; one that writes another topic
// covers only what follows its cursor. Before its first checkpoint, j has no
// high-water mark, and starting from what it covers only sends some changes
// again.
func (j *changefeedJob) successor(destination string) (cursor, coversFrom string) {
	cursor = cmp.Or(j.highWater.String, j.coversFrom)
	if j.destination == destination {
		coversFrom = j.coversFrom
	}
	return cursor, coversFrom
}

func cancelLive(ctx context.Context, db *sql.DB, job *changefeedJob) error {
	if !job.live() {
		return nil
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CANCEL JOB %d", job.id)); err != nil {
		return fmt.Errorf("cancel changefeed job %d: %w", job.id, err)
	}
	return nil
}

// settleTimeout bounds how long Migrate waits for a job that is pausing.
const settleTimeout = time.Minute

// loadSettledChangefeedJob returns the recorded job once it isn't pausing.
// CockroachDB neither resumes nor cancels a pausing job, and pausing can take
// minutes.
func loadSettledChangefeedJob(ctx context.Context, db *sql.DB) (*changefeedJob, error) {
	ctx, cancel := context.WithTimeout(ctx, settleTimeout)
	defer cancel()
	for {
		job, err := loadChangefeedJob(ctx, db)
		if err != nil || job == nil || job.status != "pause-requested" {
			return job, err
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return nil, fmt.Errorf("changefeed job %d is still pausing: run the storage migration again: %w", job.id, ctx.Err())
		}
	}
}

// loadChangefeedJob returns the recorded job, or nil when there is none.
func loadChangefeedJob(ctx context.Context, db *sql.DB) (*changefeedJob, error) {
	job := &changefeedJob{}
	err := db.QueryRowContext(ctx, `SELECT job_id, spec_hash, destination, covers_from FROM changefeed_jobs WHERE name = $1`, changefeedJobName).Scan(&job.id, &job.specHash, &job.destination, &job.coversFrom)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the changefeed job: %w", err)
	}
	err = db.QueryRowContext(ctx, `SELECT status, high_water_timestamp::STRING FROM [SHOW CHANGEFEED JOBS] WHERE job_id = $1`, job.id).Scan(&job.status, &job.highWater)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read changefeed job %d: %w", job.id, err)
	}
	return job, nil
}

// createdJob is a job and the timestamp it sends every change after.
type createdJob struct {
	id     int64
	cursor string
}

// createChangefeed starts a job with options from cursor, or from now when
// cursor is empty. A job with a cursor scans nothing first.
func createChangefeed(ctx context.Context, db *sql.DB, sink string, options []string, cursor string) (createdJob, error) {
	if cursor == "" {
		if err := db.QueryRowContext(ctx, clusterTimeSQL).Scan(&cursor); err != nil {
			return createdJob{}, err
		}
	}
	stmt := fmt.Sprintf("CREATE CHANGEFEED FOR TABLE objects INTO '%s' WITH %s, cursor = '%s'",
		strings.ReplaceAll(sink, "'", "''"), strings.Join(options, ", "), cursor)
	job := createdJob{cursor: cursor}
	if err := db.QueryRowContext(ctx, stmt).Scan(&job.id); err != nil {
		return createdJob{}, fmt.Errorf("create the objects changefeed: %w", err)
	}
	return job, nil
}

// liveChangefeedsExcept lists the live jobs other than id that write this
// database's objects table to Kafka.
func liveChangefeedsExcept(ctx context.Context, db *sql.DB, id int64) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `SELECT job_id FROM [SHOW CHANGEFEED JOBS]
		WHERE job_id != $1 AND sink_uri LIKE 'kafka://%' AND current_database() || '.public.objects' = ANY (full_table_names)
		AND status IN ('running', 'pending', 'paused', 'pause-requested')`, id)
	if err != nil {
		return nil, fmt.Errorf("list changefeed jobs: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var other int64
		if err := rows.Scan(&other); err != nil {
			return nil, err
		}
		ids = append(ids, other)
	}
	return ids, rows.Err()
}

func cancelChangefeedsExcept(ctx context.Context, db *sql.DB, id int64) error {
	others, err := liveChangefeedsExcept(ctx, db, id)
	if err != nil {
		return err
	}
	for _, other := range others {
		klog.InfoS("Cancelling an objects changefeed job that isn't recorded", "job", other)
		if _, err := db.ExecContext(ctx, fmt.Sprintf("CANCEL JOB %d", other)); err != nil {
			return fmt.Errorf("cancel changefeed job %d: %w", other, err)
		}
	}
	return nil
}

// checkOnlyChangefeed returns an error when a live job other than id writes
// the objects table to Kafka, such as one a concurrent run created. A second
// job doubles the feed's cost and its messages.
func checkOnlyChangefeed(ctx context.Context, db *sql.DB, id int64) error {
	others, err := liveChangefeedsExcept(ctx, db, id)
	if err != nil {
		return err
	}
	if len(others) > 0 {
		return fmt.Errorf("changefeed jobs %v also write the objects table to Kafka besides job %d: cancel them", others, id)
	}
	return nil
}

// sinkConnectionParams are sink parameters that change how CockroachDB
// connects to the brokers, not where its messages go.
var sinkConnectionParams = []string{
	"ca_cert", "client_cert", "client_key", "tls_enabled", "insecure_tls_skip_verify",
	"sasl_enabled", "sasl_mechanism", "sasl_user", "sasl_password",
	"sasl_client_id", "sasl_client_secret", "sasl_token_url", "sasl_scopes", "sasl_grant_type", "sasl_aws_region", "sasl_aws_iam_role_arn", "sasl_aws_iam_session_name",
}

// changefeedDestination identifies where sink's messages go: its brokers and
// every parameter but the connection ones, so a parameter that renames the
// topic, such as topic_name or topic_prefix, counts too.
func changefeedDestination(sink string) (string, error) {
	u, err := url.Parse(sink)
	if err != nil {
		return "", fmt.Errorf("parse the changefeed sink: %w", err)
	}
	q := u.Query()
	for _, p := range sinkConnectionParams {
		q.Del(p)
	}
	return u.Host + "?" + q.Encode(), nil
}

// changefeedSpecHash hashes sink and the job's options. The sink's query
// parameters are sorted, so spelling the same sink differently doesn't
// replace the job.
func changefeedSpecHash(sink string, options []string) ([]byte, error) {
	u, err := url.Parse(sink)
	if err != nil {
		return nil, fmt.Errorf("parse the changefeed sink: %w", err)
	}
	if u.Scheme != "kafka" {
		return nil, fmt.Errorf("the changefeed sink must be a kafka:// URI, got scheme %q", u.Scheme)
	}
	u.RawQuery = u.Query().Encode()
	sum := sha256.Sum256([]byte(u.String() + "\x00" + strings.Join(options, "\x00") + "\x00"))
	return sum[:], nil
}
