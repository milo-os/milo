package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
	utilwaitgroup "k8s.io/apimachinery/pkg/util/waitgroup"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/server"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/api/legacyscheme"

	"go.miloapis.com/milo/internal/apiserver/certidentity"
	datumfilters "go.miloapis.com/milo/pkg/server/filters"
)

func drainLimiter(int) (utilwaitgroup.RateLimiter, context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	return rate.NewLimiter(rate.Inf, 1), ctx, cancel
}

func TestWatchJoinsWaitGroupOnlyWithGracePeriod(t *testing.T) {
	cases := []struct {
		name        string
		grace       time.Duration
		wantWatches int
		wantSignal  bool
	}{
		{"grace period set", 30 * time.Second, 1, true},
		{"grace period zero", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := server.NewConfig(legacyscheme.Codecs)
			c.RequestInfoResolver = server.NewRequestInfoResolver(c)
			c.FeatureGate = utilfeature.DefaultFeatureGate
			c.ShutdownWatchTerminationGracePeriod = tc.grace
			c.Authentication.Authenticator = authenticator.RequestFunc(func(*http.Request) (*authenticator.Response, bool, error) {
				return &authenticator.Response{User: &user.DefaultInfo{Name: "watcher"}}, true, nil
			})
			c.Authorization.Authorizer = authorizer.AuthorizerFunc(func(context.Context, authorizer.Attributes) (authorizer.Decision, string, error) {
				return authorizer.DecisionAllow, "", nil
			})

			entered := make(chan bool, 1)
			release := make(chan struct{})
			watch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- genericapirequest.ServerShutdownSignalFrom(r.Context()) != nil
				<-release
				w.WriteHeader(http.StatusOK)
			})
			guard := certidentity.NewGuard(nil, time.Minute)
			handler := datumfilters.ProjectRouterWithRequestInfo(DefaultBuildHandlerChain(watch, c, nil, guard), c.RequestInfoResolver)

			srv := httptest.NewServer(handler)
			defer srv.Close()
			var releaseOnce sync.Once
			releaseWatch := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseWatch()

			respErr := make(chan error, 1)
			go func() {
				resp, err := srv.Client().Get(srv.URL + "/api/v1/namespaces/default/configmaps?watch=true")
				if err == nil {
					_ = resp.Body.Close()
				}
				respErr <- err
			}()

			select {
			case gotSignal := <-entered:
				if gotSignal != tc.wantSignal {
					t.Errorf("shutdown signal attached = %v, want %v", gotSignal, tc.wantSignal)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("watch request never reached the handler")
			}

			type drained struct{ before, after int }
			done := make(chan drained, 1)
			go func() {
				before, after, _ := c.WatchRequestWaitGroup.Wait(drainLimiter)
				done <- drained{before, after}
			}()
			if tc.wantWatches > 0 {
				select {
				case <-done:
					t.Fatal("wait group drained while the watch was still open")
				case <-time.After(100 * time.Millisecond):
				}
			}
			releaseWatch()

			select {
			case got := <-done:
				if got.before != tc.wantWatches || got.after != 0 {
					t.Errorf("watches before/after drain = %d/%d, want %d/0", got.before, got.after, tc.wantWatches)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("wait group never drained")
			}
			if err := <-respErr; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWatchDrainSignalClosesOnceDrainStarts(t *testing.T) {
	shutdownInitiated := make(chan struct{})
	watches := &utilwaitgroup.RateLimitedSafeWaitGroup{}
	signal := newWatchDrainSignal(shutdownInitiated, watches, time.Millisecond)

	assertOpen := func(stage string) {
		t.Helper()
		select {
		case <-signal.ShuttingDown():
			t.Fatalf("signal closed %s", stage)
		case <-time.After(50 * time.Millisecond):
		}
	}

	assertOpen("before shutdown was initiated")
	close(shutdownInitiated)
	assertOpen("before the watch drain started")

	if err := watches.Add(1); err != nil {
		t.Fatal(err)
	}
	drainReturned := make(chan struct{})
	go func() {
		defer close(drainReturned)
		_, _, _ = watches.Wait(drainLimiter)
	}()

	select {
	case <-signal.ShuttingDown():
	case <-time.After(5 * time.Second):
		t.Fatal("signal never closed after the watch drain started")
	}
	watches.Done()
	<-drainReturned
}
