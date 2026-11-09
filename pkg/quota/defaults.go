package quota

import "time"

// DefaultStalePendingClaimAge is the abandonment window shared by admission
// retries and pending-claim cleanup. It exceeds the normal admission timeout.
const DefaultStalePendingClaimAge = 5 * time.Minute

// DefaultClaimWaitTimeout is how long an admission request waits for the
// outcome of the ResourceClaim it creates.
const DefaultClaimWaitTimeout = 30 * time.Second

// DefaultDeniedClaimRetention is how long a denied auto-created claim is kept,
// from its creation, before cleanup deletes it. It exceeds
// DefaultClaimWaitTimeout, so a waiting request that must re-read its claim
// (after its watch answers 410 Gone) still finds the denial and its reason.
// A retry of the same resource inside this window replaces the kept claim
// instead of waiting for it.
const DefaultDeniedClaimRetention = 2 * DefaultClaimWaitTimeout
