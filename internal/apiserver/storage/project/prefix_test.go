package projectstorage

import "testing"

func TestProjectFromPrefix(t *testing.T) {
	for prefix, want := range map[string]string{
		"/projects/p1":  "p1",
		"/projects/p1/": "p1",
		"projects/p1":   "p1",
		"/registry":     "",
		"":              "",
		"/projectsx/p1": "",
	} {
		if got, err := ProjectFromPrefix(prefix); err != nil || got != want {
			t.Errorf("ProjectFromPrefix(%q) = %q, %v, want %q", prefix, got, err, want)
		}
	}
	// A malformed prefix under /projects fails rather than naming the shared
	// resources.
	for _, prefix := range []string{
		"/projects", "/projects/", "/projects/a/b", "/projects/../projects",
		"/projects/p1/../../registry", "/registry/../projects/p1", "/projects//p1",
	} {
		if got, err := ProjectFromPrefix(prefix); err == nil {
			t.Errorf("ProjectFromPrefix(%q) = %q, want an error", prefix, got)
		}
	}
}
