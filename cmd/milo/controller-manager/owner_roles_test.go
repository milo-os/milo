package app

import (
	"strings"
	"testing"
)

func TestOwnerRoleNamespaceWarnings(t *testing.T) {
	for _, tc := range []struct {
		name         string
		orgOwnerNS   string
		projOwnerNS  string
		assignableNS string
		wantFlags    []string
	}{
		{
			name:         "both owner roles in the assignable namespace",
			orgOwnerNS:   "datum-cloud",
			projOwnerNS:  "datum-cloud",
			assignableNS: "datum-cloud",
		},
		{
			name:         "defaults: owner roles in the system namespace",
			orgOwnerNS:   "milo-system",
			projOwnerNS:  "milo-system",
			assignableNS: "datum-cloud",
			wantFlags:    []string{"--organization-owner-role-namespace", "--project-owner-role-namespace"},
		},
		{
			name:         "only the project owner role is outside",
			orgOwnerNS:   "datum-cloud",
			projOwnerNS:  "milo-system",
			assignableNS: "datum-cloud",
			wantFlags:    []string{"--project-owner-role-namespace"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ownerRoleNamespaceWarnings(tc.orgOwnerNS, tc.projOwnerNS, tc.assignableNS)
			if len(got) != len(tc.wantFlags) {
				t.Fatalf("got %d warnings, want %d: %q", len(got), len(tc.wantFlags), got)
			}
			for i, flag := range tc.wantFlags {
				if !strings.HasPrefix(got[i], flag+" ") {
					t.Errorf("warning %d = %q, want it to name %s", i, got[i], flag)
				}
				if !strings.Contains(got[i], flag+"="+tc.assignableNS) {
					t.Errorf("warning %d = %q, want it to suggest %s=%s", i, got[i], flag, tc.assignableNS)
				}
			}
		})
	}
}
