package projectstorage

import (
	"fmt"
	"path"
	"strings"
)

// ProjectFromPrefix names the Project that owns a storage prefix,
// /projects/<name>, or returns "" for a prefix outside /projects, which
// belongs to the shared resources every project sees. Any other prefix that
// touches /projects, before or after cleaning, is an error, so a store built
// from a malformed one fails instead of serving its objects to every project.
func ProjectFromPrefix(prefix string) (string, error) {
	raw := "/" + strings.Trim(prefix, "/")
	clean := path.Clean(raw)
	if !underProjects(raw) && !underProjects(clean) {
		return "", nil
	}
	name, ok := strings.CutPrefix(clean, "/projects/")
	if !ok || clean != raw || strings.Contains(name, "/") {
		return "", fmt.Errorf("storage prefix %q is under /projects but names no single project", prefix)
	}
	return name, nil
}

func underProjects(p string) bool { return p == "/projects" || strings.HasPrefix(p, "/projects/") }
