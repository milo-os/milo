package milo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

func TestProjectCacheSyncTimeoutRejectsNegative(t *testing.T) {
	_, err := New(nil, Options{ProjectCacheSyncTimeout: -time.Second})
	require.ErrorContains(t, err, "must not be negative")
}

func TestProjectCacheSyncTimeoutCancelsFailedCache(t *testing.T) {
	for _, timeout := range []time.Duration{0, 10 * time.Millisecond} {
		t.Run(timeout.String(), func(t *testing.T) {
			p, project := newTestProvider(metav1.ConditionTrue, nil)
			p.opts.ProjectCacheSyncTimeout = timeout
			observed := make(chan time.Time, 1)
			stopped := make(chan struct{})
			p.opts.ClusterOptions = append(p.opts.ClusterOptions, func(o *cluster.Options) {
				o.NewCache = func(_ *rest.Config, _ cache.Options) (cache.Cache, error) {
					return &deadlineCache{observed: observed, stopped: stopped}, nil
				}
			})
			before := time.Now()
			_, err := p.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(project)})
			require.ErrorContains(t, err, "failed to sync project")
			expected := timeout
			if expected == 0 {
				expected = DefaultProjectCacheSyncTimeout
			}
			deadline := <-observed
			require.False(t, deadline.Before(before.Add(expected)))
			require.False(t, deadline.After(time.Now().Add(expected)))
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("failed project's cache was not canceled")
			}
			require.Empty(t, p.projects)
			require.Empty(t, p.cancelFns)
		})
	}
}

type deadlineCache struct {
	cache.Cache
	observed chan time.Time
	stopped  chan struct{}
}

func (c *deadlineCache) Start(ctx context.Context) error { <-ctx.Done(); close(c.stopped); return nil }
func (c *deadlineCache) WaitForCacheSync(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		c.observed <- time.Time{}
	} else {
		c.observed <- deadline
	}
	return false
}
