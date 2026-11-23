package cockroach

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/apis/example"
	"k8s.io/apiserver/pkg/storage"
)

const (
	redpandaImage = "docker.redpanda.com/redpandadata/redpanda:v26.3.1"
	// redpandaListener is the broker's address from the CockroachDB container.
	redpandaListener = "redpanda:29092"
)

// The shared Kafka feed, set up by TestMain before any test writes, so its
// topic holds every write to the test database.
var (
	kafkaConfig KafkaConfig
	kafkaErr    error
)

// startKafka starts a broker on the test network, and a changefeed job that
// writes the test database to it.
func startKafka(ctx context.Context) func() {
	c, err := redpanda.Run(ctx, redpandaImage, network.WithNetwork([]string{"redpanda"}, testNetwork), redpanda.WithListener(redpandaListener))
	if err != nil {
		kafkaErr = fmt.Errorf("start Redpanda: %w", err)
		return func() {}
	}
	stop := func() { _ = testcontainers.TerminateContainer(c) }
	seed, err := c.KafkaSeedBroker(ctx)
	if err != nil {
		kafkaErr = err
		return stop
	}
	kafkaConfig = KafkaConfig{Brokers: []string{seed}, Topic: "objects"}
	if kafkaErr = createTopic(ctx, kafkaConfig, kafkaConfig.Topic, 3); kafkaErr == nil {
		kafkaErr = Migrate(ctx, testDBDSN, ChangefeedSink(testSink(kafkaConfig.Topic)))
	}
	return stop
}

func testSink(topic string) Sink {
	return Sink("kafka://" + redpandaListener + "?topic_name=" + topic)
}

func createTopic(ctx context.Context, c KafkaConfig, topic string, partitions int32) error {
	client, err := kgo.NewClient(kgo.SeedBrokers(c.Brokers...))
	if err != nil {
		return err
	}
	defer client.Close()
	_, err = kadm.NewClient(client).CreateTopic(ctx, partitions, 1, nil, topic)
	return err
}

// testKafka returns the shared Kafka feed's config, or skips the test when
// there is none.
func testKafka(t *testing.T) KafkaConfig {
	t.Helper()
	testBackend(t)
	if testNetwork == nil {
		t.Skipf("the Kafka tests need the CockroachDB server the tests start in Docker, not %s", testDSNEnv)
	}
	require.NoError(t, kafkaErr)
	return kafkaConfig
}

// kafkaReplica is s's project on a backend whose watches read the shared
// Kafka feed.
func kafkaReplica(t *testing.T, s testStore) testStore {
	t.Helper()
	return otherReplica(t, s, KafkaFeed(testKafka(t)))
}

// A replica reading Kafka sees another replica's writes in order, and reads
// exactly at their revisions from the timestamps in the messages.
func TestKafkaWatchAcrossReplicas(t *testing.T) {
	ctx, a := testSetup(t)
	b := kafkaReplica(t, a)
	w := watchFrom(ctx, t, b, createPod(ctx, t, a, "start").ResourceVersion)
	var want []string
	for i := range 20 {
		want = append(want, createPod(ctx, t, a, fmt.Sprintf("pod-%02d", i)).ResourceVersion)
	}
	for _, rv := range want {
		requireAdded(t, w, rv)
	}
	list := &example.PodList{}
	require.NoError(t, b.GetList(ctx, "/pods/", storage.ListOptions{
		ResourceVersion: want[9], ResourceVersionMatch: metav1.ResourceVersionMatchExact,
		Predicate: storage.Everything, Recursive: true,
	}, list))
	require.Len(t, list.Items, 11)
}

// Values reach watchers byte for byte through the Kafka messages.
func TestKafkaWatchBinaryValues(t *testing.T) {
	ctx, a := testSetup(t, func(c *Config) { c.Transformer = binaryTransformer{} })
	b := kafkaReplica(t, a)
	b.swap.current = binaryTransformer{}
	w := watchFrom(ctx, t, b, createPod(ctx, t, a, "start").ResourceVersion)
	requireAdded(t, w, createPod(ctx, t, a, "binary").ResourceVersion)
}

// A replica reading Kafka becomes ready once every partition has resolved a
// timestamp past its start.
func TestKafkaFeedCatchesUp(t *testing.T) {
	b, err := Open(t.Context(), testDBDSN, testOptions(), KafkaFeed(testKafka(t)))
	require.NoError(t, err)
	t.Cleanup(b.Close)
	require.Eventually(t, func() bool { return b.WatchReady() == nil }, 30*time.Second, 100*time.Millisecond)
}

// A feed asked for messages the topic has dropped is refused, rather than
// starting from what's left.
func TestKafkaRefusesTrimmedTopic(t *testing.T) {
	c := testKafka(t)
	ctx := t.Context()
	c.Topic = fmt.Sprintf("trimmed-%d", time.Now().UnixNano())
	require.NoError(t, createTopic(ctx, c, c.Topic, 3))
	client, err := kgo.NewClient(kgo.SeedBrokers(c.Brokers...), kgo.DefaultProduceTopic(c.Topic), kgo.RecordPartitioner(kgo.ManualPartitioner()))
	require.NoError(t, err)
	defer client.Close()
	before := hlc{wallNanos: time.Now().Add(-time.Minute).UnixNano()}
	for range 2 {
		require.NoError(t, client.ProduceSync(ctx, &kgo.Record{Partition: 0, Value: []byte(`{"resolved":"1.0"}`)}).FirstErr())
	}
	admin := kadm.NewClient(client)
	ends, err := admin.ListEndOffsets(ctx, c.Topic)
	require.NoError(t, err)
	trimmed := ends[c.Topic][0].Offset - 1
	deleted, err := admin.DeleteRecords(ctx, kadm.Offsets{c.Topic: {0: {At: trimmed}}})
	require.NoError(t, err)
	require.NoError(t, deleted.Error())
	// The broker reports the new start offset a moment after the delete.
	require.Eventually(t, func() bool {
		starts, err := admin.ListStartOffsets(ctx, c.Topic)
		return err == nil && starts[c.Topic][0].Offset == trimmed
	}, 10*time.Second, 50*time.Millisecond)

	_, err = kafka{config: c, pool: testBackend(t).pool}.offsets(ctx, admin, before)
	require.ErrorIs(t, err, errRetentionPassed)
	require.ErrorIs(t, err, errStartOver)
}

func TestKafkaStreamResolvesAtTheSlowestPartition(t *testing.T) {
	s := &kafkaStream{partitions: map[int32]bool{0: true, 1: true}, resolved: map[int32]hlc{}}
	ts := func(n int64) hlc { return hlc{wallNanos: n} }
	s.resolved[0] = ts(5)
	_, ok := s.lowest()
	require.False(t, ok, "partition 1 hasn't reported")
	s.resolved[1] = ts(3)
	got, ok := s.lowest()
	require.True(t, ok)
	require.Equal(t, ts(3), got)
	s.released = got
	s.resolved[1] = ts(9)
	got, ok = s.lowest()
	require.True(t, ok)
	require.Equal(t, ts(5), got)
}

func TestChangefeedSuccessor(t *testing.T) {
	j := &changefeedJob{destination: "broker:9093?topic_name=objects", coversFrom: "100.0000000000", highWater: sql.NullString{String: "200.0000000000", Valid: true}}
	cursor, covers := j.successor("broker:9093?topic_name=objects")
	require.Equal(t, "200.0000000000", cursor)
	require.Equal(t, "100.0000000000", covers, "the same topic keeps what it covers")
	cursor, covers = j.successor("broker:9093?topic_name=other")
	require.Equal(t, "200.0000000000", cursor)
	require.Empty(t, covers, "another topic covers only what follows the cursor")
	j.highWater = sql.NullString{}
	cursor, _ = j.successor("broker:9093?topic_name=objects")
	require.Equal(t, "100.0000000000", cursor, "with no high-water mark yet, start from what the job covers")

	same := func(a, b string) bool {
		da, err := changefeedDestination(a)
		require.NoError(t, err)
		db, err := changefeedDestination(b)
		require.NoError(t, err)
		return da == db
	}
	require.True(t, same("kafka://broker:9093?topic_name=t", "kafka://broker:9093?topic_name=t&tls_enabled=true&sasl_password=x"))
	require.False(t, same("kafka://broker:9093?topic_name=t", "kafka://broker:9093?topic_name=u"))
	require.False(t, same("kafka://broker:9093?topic_prefix=old-", "kafka://broker:9093?topic_prefix=new-"))
	require.False(t, same("kafka://broker:9093", "kafka://other:9093"))
}

// A partition with nothing from the cursor on starts at its end from before
// the lookup, not at a later end that skips what arrived in between.
func TestStartOffsetsStopAtTheEarlierEnd(t *testing.T) {
	listed := func(offsets ...int64) map[int32]kadm.ListedOffset {
		m := map[int32]kadm.ListedOffset{}
		for p, o := range offsets {
			m[int32(p)] = kadm.ListedOffset{Partition: int32(p), Offset: o}
		}
		return m
	}
	got, err := startOffsets(listed(3, 9, -1, 6), listed(5, 7, 4), listed(0, 0, 0, 0), hlc{})
	require.NoError(t, err)
	require.Equal(t, map[int32]kgo.Offset{
		0: kgo.NewOffset().At(3),
		1: kgo.NewOffset().At(7),
		2: kgo.NewOffset().At(4),
		3: kgo.NewOffset().AtStart(), // created during the lookup
	}, got)
}

func TestChangefeedSpecHash(t *testing.T) {
	a, err := changefeedSpecHash("kafka://broker:9093?topic_name=objects&tls_enabled=true", changefeedOptions)
	require.NoError(t, err)
	b, err := changefeedSpecHash("kafka://broker:9093?tls_enabled=true&topic_name=objects", changefeedOptions)
	require.NoError(t, err)
	require.Equal(t, a, b, "the same sink spelled differently")
	c, err := changefeedSpecHash("kafka://broker:9093?tls_enabled=true&topic_name=other", changefeedOptions)
	require.NoError(t, err)
	require.NotEqual(t, a, c)
	d, err := changefeedSpecHash("kafka://broker:9093?topic_name=objects&tls_enabled=true", append(slices.Clone(changefeedOptions), "x"))
	require.NoError(t, err)
	require.NotEqual(t, a, d, "other options")
	_, err = changefeedSpecHash("webhook-https://example.com", changefeedOptions)
	require.Error(t, err)
}

func TestJobOptionsSinkConfig(t *testing.T) {
	config := func(sinkConfig string) string {
		t.Helper()
		options, err := jobOptions(sinkConfig)
		require.NoError(t, err)
		return options[len(options)-1]
	}
	require.Equal(t, `kafka_sink_config = '{"RequiredAcks":"ALL"}'`, config(""))
	require.Equal(t, `kafka_sink_config = '{"Flush":{"Frequency":"2ms","Messages":500},"RequiredAcks":"ALL"}'`,
		config(`{ "Flush": {"Messages": 500, "Frequency": "2ms"} }`), "keys sorted, acks added")
	require.Equal(t, `kafka_sink_config = '{"RequiredAcks":"ONE"}'`, config(`{"RequiredAcks": "ONE"}`), "acks set explicitly")
	require.Equal(t, `kafka_sink_config = '{"ClientID":"o''brien","RequiredAcks":"ALL"}'`, config(`{"ClientID": "o'brien"}`), "quotes escaped")
	_, err := jobOptions(`["not", "an", "object"]`)
	require.Error(t, err)
}

// Migrate keeps one job per sink: the same sink keeps it, another replaces
// it, and none cancels it.
func TestMigrateManagesTheChangefeedJob(t *testing.T) {
	c := testKafka(t)
	ctx := t.Context()
	dsn, drop, err := createDatabase(ctx, baseDSN, fmt.Sprintf("milo_changefeed_test_%d", time.Now().UnixNano()))
	require.NoError(t, err)
	t.Cleanup(drop)
	topic := fmt.Sprintf("jobs-%d", time.Now().UnixNano())
	require.NoError(t, createTopic(ctx, c, topic, 3))
	db, err := openDB(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	job := func() *changefeedJob {
		j, err := loadChangefeedJob(ctx, db)
		require.NoError(t, err)
		return j
	}
	status := func(id int64) string {
		var s string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM [SHOW CHANGEFEED JOBS] WHERE job_id = $1`, id).Scan(&s))
		return s
	}

	require.NoError(t, Migrate(ctx, dsn, ChangefeedSink(testSink(topic))))
	first := job()
	require.NotNil(t, first)
	require.True(t, first.live(), first.status)

	require.NoError(t, Migrate(ctx, dsn, ChangefeedSink(testSink(topic)+"&")))
	require.Equal(t, first.id, job().id, "the same sink kept the job")

	require.NoError(t, Migrate(ctx, dsn))
	require.Equal(t, first.id, job().id, "no sink left the job alone")

	defaults, err := jobOptions("")
	require.NoError(t, err)
	stray, err := createChangefeed(ctx, db, string(testSink(topic)), defaults, "")
	require.NoError(t, err)
	require.ErrorContains(t, checkOnlyChangefeed(ctx, db, first.id), "also write the objects table")
	require.NoError(t, Migrate(ctx, dsn, ChangefeedSink(testSink(topic))), "a job a failed run left behind blocks nothing")
	require.Equal(t, first.id, job().id)
	require.Contains(t, []string{"canceled", "cancel-requested"}, status(stray.id))

	require.NoError(t, Migrate(ctx, dsn, ChangefeedSink(testSink(topic)+"&tls_enabled=false")))
	replaced := job()
	require.NotEqual(t, first.id, replaced.id, "another sink replaced the job")
	require.Contains(t, []string{"canceled", "cancel-requested"}, status(first.id))
	require.Equal(t, first.coversFrom, replaced.coversFrom, "a job carrying on into the same topic covers what the old one did")

	batching := ChangefeedSinkConfig(`{"Flush": {"Frequency": "2ms", "Messages": 500}}`)
	require.NoError(t, Migrate(ctx, dsn, ChangefeedSink(testSink(topic)+"&tls_enabled=false"), batching))
	batched := job()
	require.NotEqual(t, replaced.id, batched.id, "another sink config replaced the job")
	require.Equal(t, replaced.coversFrom, batched.coversFrom)
	require.NoError(t, Migrate(ctx, dsn, ChangefeedSink(testSink(topic)+"&tls_enabled=false"), ChangefeedSinkConfig(`{"Flush":{"Messages":500,"Frequency":"2ms"}}`)))
	require.Equal(t, batched.id, job().id, "the same config written differently kept the job")
	replaced = batched

	other := topic + "-other"
	require.NoError(t, createTopic(ctx, c, other, 3))
	require.NoError(t, Migrate(ctx, dsn, ChangefeedSink(testSink(other))))
	second := job()
	require.NotEqual(t, replaced.id, second.id)

	require.NoError(t, Migrate(ctx, dsn, ChangefeedSink(RemoveChangefeed)))
	require.Nil(t, job())
	require.Contains(t, []string{"canceled", "cancel-requested"}, status(second.id))

	require.NoError(t, Migrate(ctx, dsn, ChangefeedSink(testSink(other))))
	last := job()
	_, err = db.ExecContext(ctx, fmt.Sprintf("PAUSE JOB %d", last.id))
	require.NoError(t, err)
	// Pausing takes CockroachDB anything from seconds to minutes, so the
	// migration either finds the job paused and resumes it, or fails for a
	// retry.
	quick, cancelQuick := context.WithTimeout(ctx, 2*time.Second)
	err = Migrate(quick, dsn, ChangefeedSink(testSink(other)))
	cancelQuick()
	if err != nil {
		require.ErrorContains(t, err, "still pausing")
	} else {
		require.NotContains(t, []string{"paused", "pause-requested"}, status(last.id), "a pausing job was left paused")
	}
	require.Equal(t, last.id, job().id)
}

// A changefeed opened from a cursor delivers every write after it, as a feed
// that resumes after a drop needs.
func TestKafkaResumesFromCursor(t *testing.T) {
	c := testKafka(t)
	ctx, s := testSetup(t)
	mark := parseRV(t, createPod(ctx, t, s, "before").ResourceVersion)
	_, err := s.b.hub.waitReleased(ctx, s.project, mark)
	require.NoError(t, err)
	cursor, ok := s.b.revTS.get(s.project, mark)
	require.True(t, ok)
	want := map[int64]bool{}
	for i := range 10 {
		want[parseRV(t, createPod(ctx, t, s, fmt.Sprintf("after-%d", i)).ResourceVersion)] = true
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stream, err := kafka{config: c, pool: s.b.pool}.open(ctx, cursor)
	require.NoError(t, err)
	defer stream.close()
	for len(want) > 0 {
		msg, ok := stream.next()
		require.True(t, ok, "the changefeed ended with %d writes missing: %v", len(want), stream.err())
		if msg.After != nil && msg.After.Project == s.project && msg.After.Key != "" {
			delete(want, msg.After.RV)
		}
	}
}

func TestSinkRedactsSecrets(t *testing.T) {
	s := Sink("kafka://broker:9093?topic_name=objects&sasl_enabled=true&sasl_user=milo&sasl_password=hunter2&client_key=c2VjcmV0")
	require.NotContains(t, s.String(), "hunter2")
	require.NotContains(t, s.String(), "c2VjcmV0")
	require.Contains(t, s.String(), "topic_name=objects")
	require.Contains(t, s.String(), "sasl_user=milo")
	require.Equal(t, "none", Sink(RemoveChangefeed).String())
}

// kafkaDatabase creates a database whose changefeed job writes to a new topic
// with the given partitions, so a test can change either.
func kafkaDatabase(t *testing.T, partitions int32) (dsn string, c KafkaConfig) {
	t.Helper()
	c = testKafka(t)
	ctx := t.Context()
	dsn, drop, err := createDatabase(ctx, baseDSN, fmt.Sprintf("milo_kafka_test_%d", time.Now().UnixNano()))
	require.NoError(t, err)
	t.Cleanup(drop)
	c.Topic = fmt.Sprintf("objects-%d", time.Now().UnixNano())
	require.NoError(t, createTopic(ctx, c, c.Topic, partitions))
	require.NoError(t, Migrate(ctx, dsn, ChangefeedSink(testSink(c.Topic))))
	return dsn, c
}

// The first baseline reaches back no further than the job's start, so a
// project written before the job is current from the start instead of stuck
// at an older revision.
func TestKafkaBaselineStartsWhereTheJobDoes(t *testing.T) {
	c := testKafka(t)
	b := testBackend(t)
	ctx := t.Context()
	source := kafka{config: c, pool: b.pool}
	covered, err := source.earliest(ctx)
	require.NoError(t, err)
	f := &feed{pool: b.pool, source: source, handler: memoryHub()}
	ts, err := f.loadBaseline(ctx, 24*time.Hour)
	require.NoError(t, err)
	require.Equal(t, covered, ts)

	_, err = source.open(ctx, covered.prev())
	require.ErrorIs(t, err, errNotCovered)
}

// A stream started before a job that writes the topic now fails, so the feed
// starts over rather than missing what came between the two jobs.
func TestKafkaStreamNoticesANewJob(t *testing.T) {
	dsn, c := kafkaDatabase(t, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	db, err := openDB(dsn)
	require.NoError(t, err)
	defer db.Close()
	b, err := Open(ctx, dsn, testOptions())
	require.NoError(t, err)
	defer b.Close()
	source := kafka{config: c, pool: b.pool}
	covered, err := source.earliest(ctx)
	require.NoError(t, err)
	stream, err := source.open(ctx, covered)
	require.NoError(t, err)
	defer stream.close()

	_, err = db.ExecContext(ctx, `UPDATE changefeed_jobs SET covers_from = cluster_logical_timestamp()::STRING`)
	require.NoError(t, err)
	for _, ok := stream.next(); ok; _, ok = stream.next() {
	}
	require.ErrorIs(t, stream.err(), errNotCovered)
}

// A replica reads the partitions a topic gains while it runs.
func TestKafkaReadsAddedPartitions(t *testing.T) {
	dsn, c := kafkaDatabase(t, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	b, err := Open(ctx, dsn, testOptions(), KafkaFeed(c))
	require.NoError(t, err)
	defer b.Close()
	require.NoError(t, b.hub.waitReady(ctx))

	client, err := kgo.NewClient(kgo.SeedBrokers(c.Brokers...), kgo.DefaultProduceTopic(c.Topic), kgo.RecordPartitioner(kgo.ManualPartitioner()))
	require.NoError(t, err)
	defer client.Close()
	_, err = kadm.NewClient(client).CreatePartitions(ctx, 1, c.Topic)
	require.NoError(t, err)
	// CockroachDB takes a while to see a new partition, so the test writes
	// to it as CockroachDB will, a project's counter and object rows.
	committed := hlc{wallNanos: time.Now().UnixNano()}.String()
	for _, row := range []string{
		`{"project": "grown", "key": "", "rv": 2, "deleted": false, "value": null}`,
		`{"project": "grown", "key": "/pods/test-ns/a", "rv": 2, "deleted": false, "value": "\\x00"}`,
	} {
		value := `{"after": ` + row + `, "before": null, "updated": "` + committed + `"}`
		require.NoError(t, client.ProduceSync(ctx, &kgo.Record{Partition: 1, Value: []byte(value)}).FirstErr())
	}
	_, err = b.hub.waitReleased(ctx, "grown", 2)
	require.NoError(t, err)
}

// A transaction whose counter lands on a partition the stream knows and whose
// object lands on one it hasn't seen yet isn't taken for a lost revision: a
// resolved timestamp past the transaction holds until the new partition
// reports too.
func TestKafkaResolvedWaitsForAddedPartitions(t *testing.T) {
	dsn, c := kafkaDatabase(t, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	b, err := Open(ctx, dsn, testOptions(), KafkaFeed(c))
	require.NoError(t, err)
	defer b.Close()
	require.NoError(t, b.hub.waitReady(ctx))
	_, sub, err := b.hub.subscribe(ctx, watchTarget{project: "split", key: "/pods/", recursive: true}, initialRevision)
	require.NoError(t, err)

	client, err := kgo.NewClient(kgo.SeedBrokers(c.Brokers...), kgo.DefaultProduceTopic(c.Topic), kgo.RecordPartitioner(kgo.ManualPartitioner()))
	require.NoError(t, err)
	defer client.Close()
	_, err = kadm.NewClient(client).CreatePartitions(ctx, 1, c.Topic)
	require.NoError(t, err)
	committed := hlc{wallNanos: time.Now().UnixNano()}
	produce := func(partition int32, value string) {
		require.NoError(t, client.ProduceSync(ctx, &kgo.Record{Partition: partition, Value: []byte(value)}).FirstErr())
	}
	row := func(key, value string) string {
		return `{"after": {"project": "split", "key": "` + key + `", "rv": 2, "deleted": false, "value": ` + value + `}, "before": null, "updated": "` + committed.String() + `"}`
	}
	produce(0, row("", "null"))
	produce(0, `{"resolved": "`+hlc{wallNanos: committed.wallNanos + 1}.String()+`"}`)
	produce(1, row("/pods/test-ns/a", `"\\x00"`))

	e, err := sub.next(ctx)
	require.NoError(t, err, "the hub gave up on the transaction")
	require.Equal(t, int64(2), e.rev)
}
