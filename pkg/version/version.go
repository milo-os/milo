// Package version exposes the Milo release a binary was built from.
//
// The Kubernetes version machinery in k8s.io/component-base/version also
// carries a version, but that value has a second job: the API server parses it
// to derive the binary version used for feature gates and compatibility
// validation, so it reports the Kubernetes API level Milo implements rather
// than Milo's own release. This package keeps the Milo release available
// alongside it.
//
// Values are injected at build time with -ldflags -X (see the Dockerfile).
package version

import (
	apimachineryversion "k8s.io/apimachinery/pkg/version"
	baseversion "k8s.io/component-base/version"
)

// Defaults describe an ad-hoc build (e.g. `go build ./cmd/milo`) that was not
// given version information through ldflags.
var (
	version      = "v0.0.0-dev"
	gitCommit    = "unknown"
	gitTreeState = "dirty"
	buildDate    = "unknown"
)

// Info reports the Milo release together with the standard Kubernetes version
// information the API server serves on /version.
type Info struct {
	// MiloVersion is the Milo release this binary was built from, such as
	// "v0.32.5". GitVersion reports the Kubernetes API level instead.
	MiloVersion string `json:"miloVersion"`

	apimachineryversion.Info
}

// Get returns the version information for this binary.
func Get() Info {
	info := baseversion.Get()

	// Fall back to this package's values when the Kubernetes machinery was not
	// given build information, so the two never disagree.
	if info.GitCommit == "" || info.GitCommit == "$Format:%H$" {
		info.GitCommit = gitCommit
	}
	if info.GitTreeState == "" {
		info.GitTreeState = gitTreeState
	}
	if info.BuildDate == "" || info.BuildDate == "1970-01-01T00:00:00Z" {
		info.BuildDate = buildDate
	}

	return Info{
		MiloVersion: version,
		Info:        info,
	}
}
