package projectstorage

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	generic "k8s.io/apiserver/pkg/registry/generic"
)

// TestGetRESTOptions_LeavesKindsEveryProjectSharesGlobal covers the kinds
// projectscope keeps global: their storage is never made project-aware.
func TestGetRESTOptions_LeavesKindsEveryProjectSharesGlobal(t *testing.T) {
	getter := WithProjectAwareDecorator(emptyOptionsGetter{}, noProjectKnown(), testDrain)
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

// TestGetRESTOptions_WithStorageCoversSharedKinds checks that WithStorage
// reaches the kinds every project shares, not only the per-project ones.
func TestGetRESTOptions_WithStorageCoversSharedKinds(t *testing.T) {
	getter := WithProjectAwareDecorator(emptyOptionsGetter{},
		noProjectKnown(),
		testDrain,
		WithStorage(generic.UndecoratedStorage),
	)
	for _, gr := range []schema.GroupResource{
		{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"},
		{Group: "apiregistration.k8s.io", Resource: "apiservices"},
		{Resource: "configmaps"},
	} {
		opts, err := getter.GetRESTOptions(gr, nil)
		if err != nil {
			t.Fatalf("GetRESTOptions(%s): %v", gr, err)
		}
		if opts.Decorator == nil {
			t.Errorf("GetRESTOptions(%s): no storage decorator", gr)
		}
	}
}
