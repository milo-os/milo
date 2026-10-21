package app

import (
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
)

var grantFixtureResource = schema.GroupVersionResource{Group: appsownership.GrantGroup, Version: "v1alpha1", Resource: appsownership.GrantResource}

// Open-latch setup allows ordinary Kubernetes CRD controllers to establish only
// the pinned fixture schema. Once sealed, even these status writes are denied.
// This function and its caller exist only in the test binary.
func grantCRDSetupStatus(a admission.Attributes) bool {
	return a.GetOperation() == admission.Update && a.GetSubresource() == "status" && a.GetNamespace() == "" && a.GetName() == appsownership.GrantCRDName && a.GetResource() == (schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"})
}
func grantSetupPermitted(a admission.Attributes) bool {
	if a.GetNamespace() != "" || a.GetSubresource() != "" {
		return false
	}
	if a.GetResource() == (schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}) {
		return a.GetOperation() == admission.Create && a.GetName() == appsownership.GrantCRDName
	}
	return a.GetResource() == grantFixtureResource && (a.GetName() == "held-grant" || a.GetName() == "held-grant-reacquire") && a.GetOperation() == admission.Create
}
