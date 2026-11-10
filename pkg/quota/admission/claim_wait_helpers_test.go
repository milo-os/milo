package admission

import (
	"context"
	"time"

	"k8s.io/client-go/dynamic"
)

// readUntilOutcome is a claim wait for a fake client, whose watch cannot start
// at a resource version: it reads the claim, as the real wait does after a
// 410, until the claim has an outcome or ctx ends.
func readUntilOutcome(ctx context.Context, claims dynamic.ResourceInterface, ref claimRef) ClaimResult {
	w := claimWait{claims: claims, ref: ref}
	for ctx.Err() == nil {
		if out, _ := w.decideByGet(ctx); out != nil {
			return *out
		}
		time.Sleep(5 * time.Millisecond)
	}
	return w.unfinished(ctx, nil)
}
