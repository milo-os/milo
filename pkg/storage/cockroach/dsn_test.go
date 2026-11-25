package cockroach

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDSNStringHidesPassword(t *testing.T) {
	for name, tc := range map[string]struct{ dsn, want string }{
		"empty":                 {"", ""},
		"url":                   {"postgresql://milo@cockroachdb:26257/milo?sslmode=verify-full", "cockroachdb:26257/milo"},
		"url with password":     {"postgres://milo:secret@cockroachdb:26257/milo", "cockroachdb:26257/milo"},
		"url password query":    {"postgresql://cockroachdb/milo?password=secret", "cockroachdb/milo"},
		"malformed url":         {"postgresql://milo:secret@cockroachdb:port/milo", "malformed connection string"},
		"keyword/value":         {"host=cockroachdb port=26257 dbname=milo user=milo password=secret", "cockroachdb:26257/milo"},
		"keyword/value no port": {"host=cockroachdb dbname=milo password=secret", "cockroachdb/milo"},
		"quoted password":       {"host=cockroachdb dbname=milo password='secret host=other'", "keyword/value connection string"},
		"escaped password":      {`host=cockroachdb dbname=milo password=sec\ret`, "keyword/value connection string"},
		"spaces around equals":  {"host = cockroachdb dbname = milo password = secret", "keyword/value connection string"},
	} {
		t.Run(name, func(t *testing.T) {
			d := DSN(tc.dsn)
			require.Equal(t, tc.want, d.String())
			require.NotContains(t, fmt.Sprint(d), "secret")
			require.NotContains(t, fmt.Sprint(&d), "secret")
		})
	}
}
