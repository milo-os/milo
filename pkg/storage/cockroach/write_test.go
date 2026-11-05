package cockroach

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteBatchRejectsInvalidWrites(t *testing.T) {
	for name, writes := range map[string][]write{
		"empty":         nil,
		"counter key":   {createWrite("", nil, 0)},
		"duplicate key": {createWrite("a", nil, 0), createWrite("a", nil, 0)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newWriteBatch(writes...)
			require.Error(t, err)
		})
	}
	b, err := newWriteBatch(createWrite("b", nil, 0), createWrite("a", nil, 0))
	require.NoError(t, err)
	require.Equal(t, []string{"", "b", "a"}, b.keys, "the counter's key comes first, then the writes' in order")
}

// A precondition no row can meet fails its own write, not the batch.
func TestWriteMatches(t *testing.T) {
	live := &row{rev: 5}
	require.True(t, createWrite("a", nil, 0).matches(nil), "create of a missing key")
	require.False(t, deleteWrite("a", 5).matches(nil), "delete of a missing key")
	require.False(t, updateWrite("a", -1, nil, 0).matches(live))
	require.True(t, deleteWrite("a", 5).matches(live))
	require.False(t, createWrite("a", nil, 0).matches(live), "create of a live key")
}
