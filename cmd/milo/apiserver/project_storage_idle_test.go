package app

import (
	"testing"
	"time"

	projectstorage "go.miloapis.com/milo/internal/apiserver/storage/project"
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

func TestProjectStorageIdleTimeoutFlagSetsTheStorageSetting(t *testing.T) {
	restoreIdleTimeout(t)
	for _, value := range []string{"30m", "0s"} {
		if err := NewCommand().ParseFlags([]string{"--" + projectStorageIdleFlag + "=" + value}); err != nil {
			t.Fatalf("parsing --%s: %v", projectStorageIdleFlag, err)
		}
		if err := applyStorageFlags(); err != nil {
			t.Fatalf("applyStorageFlags with --%s=%s: %v", projectStorageIdleFlag, value, err)
		}
		want, _ := time.ParseDuration(value)
		if got := projectstorage.ChildIdleTimeout(); got != want {
			t.Errorf("the idle timeout is %s after --%s=%s, want %s", got, projectStorageIdleFlag, value, want)
		}
	}
}

func TestProjectStorageIdleTimeoutRefusesANegativeTime(t *testing.T) {
	restoreIdleTimeout(t)
	if err := NewCommand().ParseFlags([]string{"--" + projectStorageIdleFlag + "=-1m"}); err != nil {
		t.Fatalf("parsing --%s: %v", projectStorageIdleFlag, err)
	}
	if err := applyStorageFlags(); err == nil {
		t.Errorf("applyStorageFlags accepted --%s=-1m", projectStorageIdleFlag)
	}
}

// restoreIdleTimeout puts the process-wide idle timeout back when the test ends.
func restoreIdleTimeout(t *testing.T) {
	t.Helper()
	d := projectstorage.ChildIdleTimeout()
	t.Cleanup(func() { projectstorage.SetChildIdleTimeout(d) })
}
