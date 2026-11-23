package cockroach

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"k8s.io/klog/v2"
)

// KafkaConfig says where to read the changefeed that [ChangefeedSink] has
// CockroachDB write to Kafka.
type KafkaConfig struct {
	Brokers []string
	Topic   string
	// TLS is nil for plaintext connections.
	TLS *tls.Config
}

// KafkaFeed has watches read the objects table's changes from the topic that
// one changefeed job writes, which every API server shares, instead of
// running a changefeed per server.
func KafkaFeed(c KafkaConfig) Option {
	return func(o *options) { o.kafka = &c }
}

// kafkaSeekMargin is how far before a cursor the feed starts reading. A
// message's Kafka timestamp is when CockroachDB sent it, after its commit,
// but the sending node's clock can trail the commit timestamp by up to the
// cluster's maximum clock offset (500ms by default).
const kafkaSeekMargin = 5 * time.Second

// kafkaCheckInterval is how often a stream checks that its job still covers
// its cursor and whether the topic gained partitions.
const kafkaCheckInterval = 5 * time.Second

var (
	// errRetentionPassed means a partition no longer holds every message
	// since the cursor.
	errRetentionPassed = fmt.Errorf("%w: the topic no longer holds the changefeed's messages from the cursor", errStartOver)
	// errNotCovered means the job writing the topic started after the cursor,
	// so the topic lacks the changes in between.
	errNotCovered = fmt.Errorf("%w: the changefeed job writing the topic started after the cursor", errStartOver)
	// errNoChangefeedJob means no job writes the topic yet.
	errNoChangefeedJob = errors.New("no changefeed job is recorded: run the storage migration with a changefeed sink")
)

// kafka reads every partition of the changefeed's topic directly, with no
// consumer group: every API server needs every message, and starts reading
// from its cursor. The job's record in changefeed_jobs says from when the
// topic holds every change.
type kafka struct {
	config KafkaConfig
	pool   querier
}

// earliest returns the timestamp the job's topic holds every change after.
func (k kafka) earliest(ctx context.Context) (hlc, error) {
	var ts hlc
	err := k.pool.QueryRow(ctx, `SELECT covers_from FROM changefeed_jobs WHERE name = $1`, changefeedJobName).Scan(&ts)
	if errors.Is(err, pgx.ErrNoRows) {
		return hlc{}, errNoChangefeedJob
	}
	return ts, err
}

func (k kafka) open(ctx context.Context, cursor hlc) (changefeedStream, error) {
	covered, err := k.earliest(ctx)
	if err != nil {
		return nil, err
	}
	if cursor.before(covered) {
		return nil, fmt.Errorf("%w: cursor %s, job from %s", errNotCovered, cursor, covered)
	}
	opts := []kgo.Opt{
		kgo.SeedBrokers(k.config.Brokers...),
		// A partition that lost messages the feed hasn't read yet fails the
		// changefeed instead of skipping ahead.
		kgo.ConsumeResetOffset(kgo.NoResetOffset()),
	}
	if k.config.TLS != nil {
		opts = append(opts, kgo.DialTLSConfig(k.config.TLS))
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	admin := kadm.NewClient(client)
	offsets, err := k.offsets(ctx, admin, cursor)
	if err != nil {
		client.Close()
		return nil, err
	}
	client.AddConsumePartitions(map[string]map[int32]kgo.Offset{k.config.Topic: offsets})
	s := &kafkaStream{
		ctx: ctx, source: k, cursor: cursor, client: client, admin: admin,
		partitions: map[int32]bool{}, resolved: map[int32]hlc{}, behind: map[int32]int64{},
		nextCheck: time.Now().Add(kafkaCheckInterval),
	}
	for p := range offsets {
		s.partitions[p] = true
	}
	return s, nil
}

// offsets finds where each partition's messages from just before cursor
// start.
func (k kafka) offsets(ctx context.Context, admin *kadm.Client, cursor hlc) (map[int32]kgo.Offset, error) {
	topic := k.config.Topic
	list := func(what string, lookup func(context.Context, ...string) (kadm.ListedOffsets, error)) (map[int32]kadm.ListedOffset, error) {
		listed, err := lookup(ctx, topic)
		if err == nil {
			err = listed.Error()
		}
		if err != nil {
			return nil, fmt.Errorf("find the %s offsets of topic %s: %w", what, topic, err)
		}
		return listed[topic], nil
	}
	// The ends come first. A partition with nothing from the cursor on gets
	// the end offset at the time of the lookup, which can already be past
	// messages written since.
	ends, err := list("end", admin.ListEndOffsets)
	if err != nil {
		return nil, err
	}
	since := cursor.time().Add(-kafkaSeekMargin).UnixMilli()
	from, err := list("cursor's", func(ctx context.Context, topics ...string) (kadm.ListedOffsets, error) {
		return admin.ListOffsetsAfterMilli(ctx, since, topics...)
	})
	if err != nil {
		return nil, err
	}
	starts, err := list("start", admin.ListStartOffsets)
	if err != nil {
		return nil, err
	}
	return startOffsets(from, ends, starts, cursor)
}

// startOffsets returns each partition's offset for the cursor, no later than
// its end before the lookup. A partition missing from ends was created during
// the lookup, so it's read from its beginning.
func startOffsets(from, ends, starts map[int32]kadm.ListedOffset, cursor hlc) (map[int32]kgo.Offset, error) {
	offsets := make(map[int32]kgo.Offset, len(from))
	for p, o := range from {
		end, ok := ends[p]
		if !ok {
			offsets[p] = kgo.NewOffset().AtStart()
			continue
		}
		offset := o.Offset
		if offset < 0 || offset > end.Offset {
			offset = end.Offset
		}
		// When the first message left is already past the cursor, the ones
		// before it may have been too.
		if start := starts[p].Offset; offset == start && start > 0 {
			return nil, fmt.Errorf("%w: partition %d starts after %s", errRetentionPassed, p, cursor.time())
		}
		offsets[p] = kgo.NewOffset().At(offset)
	}
	return offsets, nil
}

func (kafka) refused(err error) bool {
	return errors.Is(err, errNoChangefeedJob) || errors.Is(err, kerr.UnknownTopicOrPartition) || errors.Is(err, kerr.OffsetOutOfRange)
}

// kafkaStream reads the partitions in whatever order their messages arrive.
// The hub orders each project's writes itself and drops repeats. Resolved
// messages go to every partition, and a timestamp is resolved for the stream
// once every partition has reached it.
type kafkaStream struct {
	ctx    context.Context
	source kafka
	// cursor is where the stream started.
	cursor     hlc
	client     *kgo.Client
	admin      *kadm.Client
	records    []*kgo.Record
	partitions map[int32]bool
	resolved   map[int32]hlc
	behind     map[int32]int64
	released   hlc
	nextCheck  time.Time
	failed     error
}

func (s *kafkaStream) next() (feedMessage, bool) {
	for {
		for len(s.records) > 0 {
			r := s.records[0]
			s.records = s.records[1:]
			msg, err := decodeFeedMessage(r.Value)
			if err != nil {
				s.failed = err
				return feedMessage{}, false
			}
			if msg.Resolved.isZero() {
				return msg, true
			}
			ts, err := s.resolve(r.Partition, msg.Resolved)
			if err != nil {
				s.failed = err
				return feedMessage{}, false
			}
			// A resolved message that doesn't move the stream's resolved
			// timestamp, with ts zero, still shows the stream is alive.
			return feedMessage{Resolved: ts}, true
		}
		if time.Now().After(s.nextCheck) {
			if err := s.check(); err != nil {
				s.failed = err
				return feedMessage{}, false
			}
		}
		fetches := s.client.PollFetches(s.ctx)
		if err := s.ctx.Err(); err != nil {
			s.failed = err
			return feedMessage{}, false
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			s.failed = fmt.Errorf("read partition %d of topic %s: %w", errs[0].Partition, errs[0].Topic, errs[0].Err)
			return feedMessage{}, false
		}
		s.records = fetches.Records()
		s.noteBehind(fetches)
	}
}

// check fails the stream when a job started since writes the topic, and
// reads partitions the topic gained from their start. CockroachDB sends
// some keys to a new partition as soon as it sees it.
func (s *kafkaStream) check() error {
	s.nextCheck = time.Now().Add(kafkaCheckInterval)
	covered, err := s.source.earliest(s.ctx)
	if err != nil {
		return err
	}
	if s.cursor.before(covered) {
		return fmt.Errorf("%w: the stream started at %s, the job at %s", errNotCovered, s.cursor, covered)
	}
	topic := s.source.config.Topic
	topics, err := s.admin.ListTopics(s.ctx, topic)
	if err == nil {
		err = topics.Error()
	}
	if err != nil {
		return fmt.Errorf("list the partitions of topic %s: %w", topic, err)
	}
	added := map[int32]kgo.Offset{}
	for p := range topics[topic].Partitions {
		if !s.partitions[p] {
			s.partitions[p] = true
			added[p] = kgo.NewOffset().AtStart()
		}
	}
	if len(added) > 0 {
		klog.InfoS("Reading partitions the changefeed's topic gained", "topic", topic, "partitions", len(added))
		s.client.AddConsumePartitions(map[string]map[int32]kgo.Offset{topic: added})
	}
	return nil
}

// noteBehind records how far each partition's last fetch was from its end.
func (s *kafkaStream) noteBehind(fetches kgo.Fetches) {
	fetches.EachPartition(func(p kgo.FetchTopicPartition) {
		if n := len(p.Records); n > 0 {
			s.behind[p.Partition] = p.HighWatermark - p.Records[n-1].Offset - 1
		}
	})
	var total int64
	for _, n := range s.behind {
		total += n
	}
	kafkaBehind.Set(float64(total))
}

// resolve notes a resolved message on partition p, and returns the stream's
// resolved timestamp when that moved forward, or else zero.
//
// CockroachDB sends a resolved message only once every change before it has
// gone out, to whichever partition. A partition that holds one of those
// changes therefore exists by the time the message arrives, so the stream
// lists the partitions again before it moves forward, and waits for any new
// one to report as well.
func (s *kafkaStream) resolve(p int32, ts hlc) (hlc, error) {
	if s.resolved[p].before(ts) {
		s.resolved[p] = ts
	}
	if _, ok := s.lowest(); !ok {
		return hlc{}, nil
	}
	if err := s.check(); err != nil {
		return hlc{}, err
	}
	low, ok := s.lowest()
	if !ok {
		return hlc{}, nil
	}
	s.released = low
	return low, nil
}

// lowest returns the timestamp every partition has resolved, when that is
// past the one the stream last released.
func (s *kafkaStream) lowest() (hlc, bool) {
	if len(s.resolved) < len(s.partitions) {
		return hlc{}, false
	}
	var low hlc
	first := true
	for _, r := range s.resolved {
		if first || r.before(low) {
			low, first = r, false
		}
	}
	return low, s.released.before(low)
}

func (s *kafkaStream) err() error {
	if s.failed != nil {
		return s.failed
	}
	return errors.New("the changefeed ended")
}

func (s *kafkaStream) close() { s.client.Close() }
