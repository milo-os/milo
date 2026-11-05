package cockroach

import (
	"context"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// admission caps the work in flight under each key. Work past the limit
// waits up to wait for a slot, then gets 429 Too Many Requests.
type admission struct {
	scope string // what the limit protects, for metrics and the 429's message
	limit int
	wait  time.Duration

	mu   sync.Mutex
	keys map[string]*slots
}

// slots is one key's work in flight. It stays in the map while anything holds
// or waits for a slot, so a release wakes only its own key's waiters.
type slots struct {
	inFlight, waiting int
	freed             broadcast
}

func newAdmission(scope string, limit int, wait time.Duration) *admission {
	return &admission{scope: scope, limit: max(limit, 1), wait: wait, keys: map[string]*slots{}}
}

// admit takes a slot under key, which the caller gives back with release. It
// returns 429 Too Many Requests when no slot frees up in time, and the
// context's error when ctx ends first.
func (a *admission) admit(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result := "admitted"
	var deadline <-chan time.Time
	a.mu.Lock()
	s := a.keys[key]
	if s == nil {
		s = &slots{}
		a.keys[key] = s
	}
	for s.inFlight >= a.limit {
		freed := s.freed.wait()
		s.waiting++
		a.mu.Unlock()
		if deadline == nil {
			deadline, result = time.After(a.wait), "waited"
		}
		var err error
		select {
		case <-freed:
		case <-deadline:
			result = "rejected"
			err = apierrors.NewTooManyRequests("storage for this "+a.scope+" is busy; retry the write shortly", 1)
		case <-ctx.Done():
			err = ctx.Err()
		}
		a.mu.Lock()
		s.waiting--
		if err != nil {
			a.forgetLocked(key, s)
			a.mu.Unlock()
			if result == "rejected" {
				writeAdmissions.WithLabelValues(a.scope, result).Inc()
			}
			return err
		}
	}
	s.inFlight++
	a.mu.Unlock()
	writeAdmissions.WithLabelValues(a.scope, result).Inc()
	return nil
}

func (a *admission) release(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.keys[key]
	s.inFlight--
	s.freed.fire()
	a.forgetLocked(key, s)
}

func (a *admission) forgetLocked(key string, s *slots) {
	if s.inFlight == 0 && s.waiting == 0 {
		delete(a.keys, key)
	}
}

// admittedWrites holds a slot under the write's project while next commits it.
type admittedWrites struct {
	*admission
	next committer
}

func (a admittedWrites) commit(ctx context.Context, project string, w write) (writeResult, error) {
	if err := a.admit(ctx, project); err != nil {
		return writeResult{}, err
	}
	defer a.release(project)
	return a.next.commit(ctx, project, w)
}

// admittedBatches holds one slot, shared by every project, while next commits
// a batch, which caps the transactions in flight.
type admittedBatches struct {
	*admission
	next batchCommitter
}

func (a admittedBatches) commitBatch(ctx context.Context, project string, b writeBatch) ([]writeResult, error) {
	if err := a.admit(ctx, ""); err != nil {
		return nil, err
	}
	defer a.release("")
	return a.next.commitBatch(ctx, project, b)
}
