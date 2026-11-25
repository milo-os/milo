package cockroach

import (
	"net/url"
	"strings"
)

// DSN is a CockroachDB connection string. It implements pflag.Value.
type DSN string

func (d *DSN) Set(s string) error {
	*d = DSN(s)
	return nil
}

func (d DSN) Type() string { return "string" }

// String returns only host, port and database, since the DSN can contain a
// password.
func (d DSN) String() string {
	if d == "" {
		return ""
	}
	s := string(d)
	if strings.HasPrefix(s, "postgres://") || strings.HasPrefix(s, "postgresql://") {
		u, err := url.Parse(s)
		if err != nil {
			return "malformed connection string"
		}
		return u.Host + u.Path
	}
	// Quotes and escapes make a whitespace split unsafe, so skip those.
	if !strings.ContainsAny(s, `'\`) {
		pairs := map[string]string{}
		for _, field := range strings.Fields(s) {
			if k, v, ok := strings.Cut(field, "="); ok {
				pairs[k] = v
			}
		}
		if host := pairs["host"]; host != "" {
			if port := pairs["port"]; port != "" {
				host += ":" + port
			}
			return host + "/" + pairs["dbname"]
		}
	}
	return "keyword/value connection string"
}
