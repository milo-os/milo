package app

import (
	"testing"
	"time"

	"k8s.io/apiserver/pkg/storage/cacher"
)

const consistencyCheckFlag = "watch-cache-consistency-check-period"

func TestWatchCacheConsistencyCheckPeriodDefaultsToOneHour(t *testing.T) {
	f := NewCommand().Flags().Lookup(consistencyCheckFlag)
	if f == nil {
		t.Fatalf("the API server has no --%s flag", consistencyCheckFlag)
	}
	if got, want := f.DefValue, time.Hour.String(); got != want {
		t.Errorf("--%s defaults to %s, want %s", consistencyCheckFlag, got, want)
	}
}

func TestWatchCacheConsistencyCheckPeriodFlagSetsTheCacherPeriod(t *testing.T) {
	restoreCacherPeriod(t)
	if err := NewCommand().ParseFlags([]string{"--" + consistencyCheckFlag + "=2m"}); err != nil {
		t.Fatalf("parsing --%s: %v", consistencyCheckFlag, err)
	}
	if err := applyStorageFlags(); err != nil {
		t.Fatalf("applyStorageFlags: %v", err)
	}
	if got := cacher.ConsistencyCheckPeriod; got != 2*time.Minute {
		t.Errorf("cacher.ConsistencyCheckPeriod = %s after --%s=2m, want 2m0s", got, consistencyCheckFlag)
	}
}

func TestWatchCacheConsistencyCheckPeriodRefusesANonPositivePeriod(t *testing.T) {
	restoreCacherPeriod(t)
	if err := NewCommand().ParseFlags([]string{"--" + consistencyCheckFlag + "=0s"}); err != nil {
		t.Fatalf("parsing --%s: %v", consistencyCheckFlag, err)
	}
	if err := applyStorageFlags(); err == nil {
		t.Errorf("applyStorageFlags accepted --%s=0s; a cache would check itself without pause", consistencyCheckFlag)
	}
}

// restoreCacherPeriod puts the process-wide period back when the test ends, so tests do not leak it into each other.
func restoreCacherPeriod(t *testing.T) {
	t.Helper()
	period := cacher.ConsistencyCheckPeriod
	t.Cleanup(func() { cacher.ConsistencyCheckPeriod = period })
}
