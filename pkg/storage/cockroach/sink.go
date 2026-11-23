package cockroach

import (
	"net/url"
	"slices"
)

// Sink is a changefeed sink URI for [ChangefeedSink]. It implements
// pflag.Value.
type Sink string

func (s *Sink) Set(v string) error {
	*s = Sink(v)
	return nil
}

func (s Sink) Type() string { return "string" }

// shownSinkParams are the sink parameters that hold no secrets.
var shownSinkParams = []string{"topic_name", "tls_enabled", "sasl_enabled", "sasl_mechanism", "sasl_user"}

// String returns the sink with every other parameter's value redacted, as
// CockroachDB lists sinks, since parameters such as sasl_password and
// client_key are secrets.
func (s Sink) String() string {
	if s == "" || s == RemoveChangefeed {
		return string(s)
	}
	u, err := url.Parse(string(s))
	if err != nil {
		return "malformed sink"
	}
	if u.User != nil {
		u.User = url.User("redacted")
	}
	q := u.Query()
	for k := range q {
		if !slices.Contains(shownSinkParams, k) {
			q.Set(k, "redacted")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
