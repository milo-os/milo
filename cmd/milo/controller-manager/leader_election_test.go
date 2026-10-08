package app

import (
	"sync"
	"testing"
	"time"

	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/klog/v2"
)

func TestLeaderElectionRestConfigDoesNotMutateBase(t *testing.T) {
	limiter := flowcontrol.NewTokenBucketRateLimiter(1, 1)
	base := &restclient.Config{Host: "https://example.invalid", QPS: 50, Burst: 100, RateLimiter: limiter}

	cfg := leaderElectionRestConfig(base)

	if cfg.RateLimiter != nil {
		t.Errorf("lease config kept the base rate limiter")
	}
	if cfg.QPS != leaderElectionQPS || cfg.Burst != leaderElectionBurst {
		t.Errorf("got QPS %v Burst %d, want %v and %d", cfg.QPS, cfg.Burst, leaderElectionQPS, leaderElectionBurst)
	}
	if cfg.Dial == nil {
		t.Errorf("lease config has no dedicated dialer, so it would share the base connection")
	}
	if base.RateLimiter != limiter || base.QPS != 50 || base.Burst != 100 || base.Dial != nil {
		t.Errorf("base config was mutated: %+v", base)
	}
}

func TestWaitForLeaseReleaseReturnsWhenElectionsFinish(t *testing.T) {
	var elections sync.WaitGroup
	elections.Add(1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		elections.Done()
	}()

	start := time.Now()
	waitForLeaseRelease(klog.Background(), &elections, time.Minute)
	if time.Since(start) > 10*time.Second {
		t.Fatalf("waited %v for a release that finished in 10ms", time.Since(start))
	}
}

func TestWaitForLeaseReleaseGivesUpAfterTimeout(t *testing.T) {
	var elections sync.WaitGroup
	elections.Add(1)
	defer elections.Done()

	start := time.Now()
	waitForLeaseRelease(klog.Background(), &elections, 20*time.Millisecond)
	if time.Since(start) > 10*time.Second {
		t.Fatalf("waited %v past a 20ms timeout", time.Since(start))
	}
}
