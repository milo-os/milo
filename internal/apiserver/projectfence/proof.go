// Package projectfence carries server-private admission evidence to a native
// resource's storage commit. It conveys no authorization by itself.
package projectfence

import (
	"context"
	"net/http"
	"strconv"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type key struct{}
type state struct {
	mu     sync.Mutex
	proof  Proof
	target string
}

// Proof is a value snapshot. Only successful native ownership admission writes
// it; headers, user extras, and object metadata cannot supply it.
type Proof struct {
	ProjectName, ProjectUID, AppUID string
	ProjectRevision                 int64
	Resource                        schema.GroupResource
	Kind, Name, Namespace, UID      string
}

func WithContext(ctx context.Context) context.Context { return context.WithValue(ctx, key{}, &state{}) }
func WithRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Value(key{}).(*state); !ok {
			r = r.WithContext(WithContext(r.Context()))
		}
		next.ServeHTTP(w, r)
	})
}
func Clear(ctx context.Context) {
	if s, ok := ctx.Value(key{}).(*state); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.proof = Proof{}
		s.target = ""
	}
}
func Record(ctx context.Context, p Proof, revision string) bool {
	Clear(ctx)
	s, ok := ctx.Value(key{}).(*state)
	if !ok {
		return false
	}
	rv, err := strconv.ParseInt(revision, 10, 64)
	if err != nil || rv <= 0 || strconv.FormatInt(rv, 10) != revision || p.ProjectName == "" || p.ProjectUID == "" || p.UID == "" {
		return false
	}
	p.ProjectRevision = rv
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proof = p
	s.target = ""
	return true
}
func Read(ctx context.Context) (Proof, bool) {
	s, ok := ctx.Value(key{}).(*state)
	if !ok {
		return Proof{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proof, s.proof.ProjectRevision > 0
}

// Bind locks the proof to the actual encoded storage key for this mutation. The
// raw client rejects any later attempt to use it for a different target.
func Bind(ctx context.Context, target string) bool {
	s, ok := ctx.Value(key{}).(*state)
	if !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proof.ProjectRevision <= 0 || target == "" || (s.target != "" && s.target != target) {
		return false
	}
	s.target = target
	return true
}
func Bound(ctx context.Context) (Proof, string, bool) {
	s, ok := ctx.Value(key{}).(*state)
	if !ok {
		return Proof{}, "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proof, s.target, s.proof.ProjectRevision > 0 && s.target != ""
}
