package app

import (
	"testing"
	"time"
)

const projectStorageIdleFlag = "project-storage-idle-timeout"

func TestProjectStorageIdleTimeoutDefaultsToFifteenMinutes(t *testing.T) {
	f := NewCommand().Flags().Lookup(projectStorageIdleFlag)
	if f == nil {
		t.Fatalf("the API server has no --%s flag", projectStorageIdleFlag)
	}
	if got, want := f.DefValue, (15 * time.Minute).String(); got != want {
		t.Errorf("--%s defaults to %s, want %s", projectStorageIdleFlag, got, want)
	}
}

func TestProjectStorageIdleTimeoutFlagSetsTheIdleTime(t *testing.T) {
	restoreIdleTimeout(t)
	for _, value := range []string{"30m", "0s"} {
		if err := NewCommand().ParseFlags([]string{"--" + projectStorageIdleFlag + "=" + value}); err != nil {
			t.Fatalf("parsing --%s: %v", projectStorageIdleFlag, err)
		}
		if err := checkProjectStorageIdleTimeout(projectStorageIdleTimeout); err != nil {
			t.Fatalf("--%s=%s refused: %v", projectStorageIdleFlag, value, err)
		}
		want, _ := time.ParseDuration(value)
		if projectStorageIdleTimeout != want {
			t.Errorf("the idle timeout is %s after --%s=%s, want %s", projectStorageIdleTimeout, projectStorageIdleFlag, value, want)
		}
	}
}

func TestProjectStorageIdleTimeoutRefusesANegativeTime(t *testing.T) {
	if err := checkProjectStorageIdleTimeout(-time.Minute); err == nil {
		t.Errorf("--%s=-1m was taken", projectStorageIdleFlag)
	}
}

// restoreIdleTimeout puts the flag's variable back when the test ends.
func restoreIdleTimeout(t *testing.T) {
	t.Helper()
	d := projectStorageIdleTimeout
	t.Cleanup(func() { projectStorageIdleTimeout = d })
}
