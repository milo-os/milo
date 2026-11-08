package cockroach

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// hlc is a CockroachDB hybrid logical clock timestamp. AS OF SYSTEM TIME and a
// changefeed's cursor take no placeholders, so timestamps go into SQL text.
// An hlc only ever prints as digits and a dot, which keeps that safe.
type hlc struct {
	wallNanos int64
	logical   int32
}

func parseHLC(s string) (hlc, error) {
	wall, logical, dotted := strings.Cut(s, ".")
	w, err := strconv.ParseUint(wall, 10, 63)
	if err != nil {
		return hlc{}, fmt.Errorf("invalid HLC timestamp %q", s)
	}
	var l uint64
	if dotted {
		if l, err = strconv.ParseUint(logical, 10, 31); err != nil {
			return hlc{}, fmt.Errorf("invalid HLC timestamp %q", s)
		}
	}
	return hlc{wallNanos: int64(w), logical: int32(l)}, nil
}

func (t hlc) String() string { return fmt.Sprintf("%d.%010d", t.wallNanos, t.logical) }

func (t hlc) isZero() bool { return t == hlc{} }

func (t hlc) time() time.Time { return time.Unix(0, t.wallNanos) }

// prev is the timestamp just before t. Stepping back the wall time alone would
// skip commits at the same wall time with a lower logical counter.
func (t hlc) prev() hlc {
	if t.logical > 0 {
		return hlc{wallNanos: t.wallNanos, logical: t.logical - 1}
	}
	return hlc{wallNanos: t.wallNanos - 1, logical: math.MaxInt32}
}

// Scan reads a timestamp selected as a STRING.
func (t *hlc) Scan(src any) error {
	s, ok := src.(string)
	if !ok {
		return fmt.Errorf("scan HLC timestamp from %T", src)
	}
	return t.UnmarshalText([]byte(s))
}

// UnmarshalText reads a timestamp as changefeeds encode it, inside a JSON
// string.
func (t *hlc) UnmarshalText(text []byte) error {
	var err error
	*t, err = parseHLC(string(text))
	return err
}

// snapshotSQL begins a read-only transaction as of ts.
func snapshotSQL(ts hlc) string { return "BEGIN TRANSACTION AS OF SYSTEM TIME '" + ts.String() + "'" }

// clusterTimeSQL selects the transaction's timestamp in the form hlc scans.
const clusterTimeSQL = `SELECT cluster_logical_timestamp()::STRING`
