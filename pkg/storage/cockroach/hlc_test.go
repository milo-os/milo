package cockroach

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseHLC(t *testing.T) {
	for in, want := range map[string]string{
		"123":                            "123.0000000000",
		"123.0000000005":                 "123.0000000005",
		"1793185225165253245.2147483647": "1793185225165253245.2147483647",
	} {
		ts, err := parseHLC(in)
		require.NoError(t, err, in)
		require.Equal(t, want, ts.String())
	}
	for _, in := range []string{
		"", "-1", "+1", "1.", "1.2'", "1'; COMMIT; --", "1\n", "1.2.3", "1e3",
		"9223372036854775808", "1.2147483648", "now",
	} {
		_, err := parseHLC(in)
		require.Error(t, err, "%q", in)
	}
}

func TestHLCPrev(t *testing.T) {
	for in, want := range map[string]string{
		"1793185225165253245.0000000005": "1793185225165253245.0000000004",
		"1793185225165253245.0000000001": "1793185225165253245.0000000000",
		"1793185225165253245.0000000000": "1793185225165253244.2147483647",
	} {
		ts, err := parseHLC(in)
		require.NoError(t, err)
		require.Equal(t, want, ts.prev().String())
	}
}

func TestHLCUnmarshalJSON(t *testing.T) {
	var ts hlc
	require.NoError(t, json.Unmarshal([]byte(`"12.0000000003"`), &ts))
	require.Equal(t, hlc{wallNanos: 12, logical: 3}, ts)
	require.Error(t, json.Unmarshal([]byte(`"12'"`), &ts))
}
