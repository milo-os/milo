package version

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	baseversion "k8s.io/component-base/version"
)

// TestKubernetesVersionMatchesGoMod guards the assumption the Dockerfile makes
// when it builds the served gitVersion: that the Kubernetes minor version
// derived from the k8s.io/component-base requirement in go.mod matches the
// binary version the vendored libraries default to. A dependency bump that
// changes one without the other would make the API server report a Kubernetes
// API level it does not implement.
func TestKubernetesVersionMatchesGoMod(t *testing.T) {
	data, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}

	re := regexp.MustCompile(`(?m)^\s*k8s\.io/component-base (v[0-9]+\.[0-9]+\.[0-9]+\S*)`)
	match := re.FindSubmatch(data)
	if match == nil {
		t.Fatal("no k8s.io/component-base requirement found in go.mod")
	}

	parts := strings.Split(strings.TrimPrefix(string(match[1]), "v"), ".")
	fromGoMod := fmt.Sprintf("1.%s", parts[1])

	if fromGoMod != baseversion.DefaultKubeBinaryVersion {
		t.Errorf("go.mod requires k8s.io/component-base %s (Kubernetes %s), but DefaultKubeBinaryVersion is %s; "+
			"update the vendored dependencies together", match[1], fromGoMod, baseversion.DefaultKubeBinaryVersion)
	}
}

func TestGetReportsMiloVersion(t *testing.T) {
	info := Get()

	if info.MiloVersion == "" {
		t.Error("MiloVersion is empty")
	}
	if info.GitVersion == "" {
		t.Error("GitVersion is empty")
	}
}
