package projectfence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func validProof() Proof {
	return Proof{ProjectName: "project-a", ProjectUID: "project-uid", AppUID: "app-uid", Resource: schema.GroupResource{Group: "test.example", Resource: "things"}, Kind: "Thing", Name: "one", Namespace: "default", UID: "thing-uid"}
}

func TestPrivateProofLifecycle(t *testing.T) {
	raw := context.Background()
	require.False(t, Record(raw, validProof(), "7"))
	require.False(t, Bind(raw, "/one"))
	ctx := WithContext(raw)
	require.True(t, Record(ctx, validProof(), "7"))
	proof, ok := Read(ctx)
	require.True(t, ok)
	require.EqualValues(t, 7, proof.ProjectRevision)
	proof.ProjectName = "mutated-copy"
	original, _ := Read(ctx)
	require.Equal(t, "project-a", original.ProjectName)
	require.False(t, Bind(ctx, ""))
	require.True(t, Bind(ctx, "/one"))
	require.True(t, Bind(ctx, "/one"))
	require.False(t, Bind(ctx, "/two"))
	_, target, bound := Bound(ctx)
	require.True(t, bound)
	require.Equal(t, "/one", target)
	other := WithContext(raw)
	_, ok = Read(other)
	require.False(t, ok)
	Clear(ctx)
	_, _, bound = Bound(ctx)
	require.False(t, bound)
}

func TestInvalidReplacementClearsProof(t *testing.T) {
	for _, revision := range []string{"", "0", "-1", "+1", "01", "9223372036854775808", "1.0"} {
		t.Run(revision, func(t *testing.T) {
			ctx := WithContext(context.Background())
			require.True(t, Record(ctx, validProof(), "8"))
			require.True(t, Bind(ctx, "/one"))
			require.False(t, Record(ctx, validProof(), revision))
			_, _, ok := Bound(ctx)
			require.False(t, ok)
			_, ok = Read(ctx)
			require.False(t, ok)
		})
	}
}
