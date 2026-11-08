package quota

import "time"

// DefaultStalePendingClaimAge is the abandonment window shared by admission
// retries and pending-claim cleanup. It exceeds the normal admission timeout.
const DefaultStalePendingClaimAge = 5 * time.Minute
