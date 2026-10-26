package projectstorage

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestGetRESTOptions_LeavesKindsEveryProjectSharesGlobal covers the kinds
// projectscope keeps global: their storage is never made project-aware.
func TestGetRESTOptions_LeavesKindsEveryProjectSharesGlobal(t *testing.T) {
	getter := WithProjectAwareDecorator(emptyOptionsGetter{}, noProjectKnown())
	for _, tc := range []struct {
		gr         schema.GroupResource
		perProject bool
	}{
		{schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, false},
		{schema.GroupResource{Group: "apiregistration.k8s.io", Resource: "apiservices"}, false},
		{schema.GroupResource{Resource: "configmaps"}, true},
	} {
		opts, err := getter.GetRESTOptions(tc.gr, nil)
		if err != nil {
			t.Fatalf("GetRESTOptions(%s): %v", tc.gr, err)
		}
		if got := opts.Decorator != nil; got != tc.perProject {
			t.Errorf("GetRESTOptions(%s): project-aware storage = %t, want %t", tc.gr, got, tc.perProject)
		}
	}
}
