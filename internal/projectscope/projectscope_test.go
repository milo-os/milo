package projectscope

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestStaysGlobal(t *testing.T) {
	for _, tc := range []struct {
		gr   schema.GroupResource
		want bool
	}{
		{schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, true},
		{schema.GroupResource{Group: "apiregistration.k8s.io", Resource: "apiservices"}, true},
		{schema.GroupResource{Resource: "namespaces"}, false},
		{schema.GroupResource{Resource: "configmaps"}, false},
		{schema.GroupResource{Group: "rbac.authorization.k8s.io", Resource: "clusterroles"}, false},
		{schema.GroupResource{Group: "services.miloapis.com", Resource: "serviceentitlements"}, false},
	} {
		if got := StaysGlobal(tc.gr); got != tc.want {
			t.Errorf("StaysGlobal(%s) = %t, want %t", tc.gr, got, tc.want)
		}
	}
}
