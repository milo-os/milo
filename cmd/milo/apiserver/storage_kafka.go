package app

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/spf13/pflag"

	"go.miloapis.com/milo/pkg/storage/cockroach"
)

// kafkaOptions say where watches read the shared changefeed with
// --cockroachdb-watch-feed=kafka.
type kafkaOptions struct {
	brokers      []string
	topic        string
	caFile       string
	fetchMaxWait time.Duration
}

func (o *kafkaOptions) addFlags(fs *pflag.FlagSet) {
	fs.StringSliceVar(&o.brokers, "cockroachdb-kafka-brokers", o.brokers, "Kafka brokers, as host:port, to read the changefeed from with --cockroachdb-watch-feed=kafka.")
	fs.StringVar(&o.topic, "cockroachdb-kafka-topic", o.topic, "Topic the changefeed job writes to, the sink's topic_name, or else the table's name.")
	fs.StringVar(&o.caFile, "cockroachdb-kafka-ca-file", o.caFile, "PEM bundle of CAs the brokers' certificates are checked against. If unset, the connection to the brokers is plaintext.")
	fs.DurationVar(&o.fetchMaxWait, "cockroachdb-kafka-fetch-max-wait", o.fetchMaxWait, "How long the brokers hold a fetch for more messages, so each fetch carries more of them. Saves API server CPU at the cost of up to this much watch latency. At least 10ms when set. If unset, a fetch returns as soon as a message arrives.")
}

func (o kafkaOptions) config() (cockroach.KafkaConfig, error) {
	c := cockroach.KafkaConfig{Brokers: o.brokers, Topic: o.topic, FetchMaxWait: o.fetchMaxWait}
	if o.fetchMaxWait != 0 && o.fetchMaxWait < 10*time.Millisecond {
		return c, fmt.Errorf("--cockroachdb-kafka-fetch-max-wait must be at least 10ms, got %s", o.fetchMaxWait)
	}
	if o.caFile == "" {
		return c, nil
	}
	pem, err := os.ReadFile(o.caFile)
	if err != nil {
		return c, fmt.Errorf("read --cockroachdb-kafka-ca-file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return c, fmt.Errorf("--cockroachdb-kafka-ca-file %s holds no PEM certificates", o.caFile)
	}
	c.TLS = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return c, nil
}
