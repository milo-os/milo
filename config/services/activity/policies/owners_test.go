// SPDX-License-Identifier: AGPL-3.0-only

// Package policies holds the ActivityPolicy components Milo ships. This test
// keeps every policy's owner declared, so the activity processor's alerts
// for a failing policy reach the team that owns it.
package policies

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// The labels the activity processor reads a policy's owner from.
var ownerLabels = []string{"meta.datumapis.com/service", "meta.datumapis.com/team"}

type component struct {
	Components []string `json:"components"`
	Resources  []string `json:"resources"`
	Labels     []struct {
		Pairs map[string]string `json:"pairs"`
	} `json:"labels"`
}

func readComponent(t *testing.T, dir string) component {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var c component
	if err := yaml.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse %s/kustomization.yaml: %v", dir, err)
	}
	return c
}

// declaredOwner returns the owner labels a group's kustomization sets on its
// policies, by label name.
func declaredOwner(c component) map[string]string {
	owner := map[string]string{}
	for _, l := range c.Labels {
		for k, v := range l.Pairs {
			owner[k] = v
		}
	}
	return owner
}

// policyFiles returns the YAML files in a group directory other than its
// kustomization.
func policyFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") && e.Name() != "kustomization.yaml" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files
}

// TestEveryPolicyDeclaresItsOwner fails when a policy group sets no owner, or
// when a policy file sits outside its group's resources, where the group's
// owner labels would not reach it.
func TestEveryPolicyDeclaresItsOwner(t *testing.T) {
	groups := readComponent(t, ".").Components
	if len(groups) == 0 {
		t.Fatal("the policies component lists no groups")
	}
	for _, group := range groups {
		c := readComponent(t, group)
		owner := declaredOwner(c)
		for _, label := range ownerLabels {
			if owner[label] == "" {
				t.Errorf("%s: its kustomization sets no %s on its policies", group, label)
			}
		}
		listed := map[string]bool{}
		for _, r := range c.Resources {
			listed[r] = true
		}
		for _, f := range policyFiles(t, group) {
			if !listed[f] {
				t.Errorf("%s/%s: not in its group's resources, so its owner labels never reach it", group, f)
			}
		}
	}
}
