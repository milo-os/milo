package appsownership

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
)

const (
	GrantGroup              = "apps.miloapis.com"
	GrantResource           = "appinstallgrants"
	GrantCRDName            = GrantResource + "." + GrantGroup
	grantReservationMessage = "Apps install grant and native registration are reserved; issuance, mutation and removal remain disabled"
)

// reserveGrant protects the resource family, independently of markers, callers,
// Project routing, dynamic webhooks and configured native writer exceptions.
// Registration is reserved too: CRD deletion can erase instances indirectly,
// and an APIService can route the whole Apps group/version outside native admission.
// This bootstrap has no production registration or issuance exception.
func reserveGrant(a admission.Attributes) error {
	resource := a.GetResource()
	if resource.Group == GrantGroup && resource.Resource == GrantResource {
		return admission.NewForbidden(a, fmt.Errorf("%s", grantReservationMessage))
	}
	crd := resource.Group == "apiextensions.k8s.io" && resource.Resource == "customresourcedefinitions"
	service := resource.Group == "apiregistration.k8s.io" && resource.Resource == "apiservices"
	if !crd && !service {
		return nil
	}
	protectedName := func(name string) bool {
		if crd {
			return name == GrantCRDName
		}
		return strings.HasSuffix(name, "."+GrantGroup)
	}
	if protectedName(a.GetName()) {
		return admission.NewForbidden(a, fmt.Errorf("%s", grantReservationMessage))
	}
	// DELETE's object may be options. Only the old registration identifies its target.
	objects := []runtime.Object{a.GetOldObject()}
	if a.GetOperation() != admission.Delete {
		objects = append(objects, a.GetObject())
	}
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		metadata, err := meta.Accessor(obj)
		if err != nil {
			return admission.NewForbidden(a, fmt.Errorf("Apps registration metadata cannot be inspected"))
		}
		if protectedName(metadata.GetName()) {
			return admission.NewForbidden(a, fmt.Errorf("%s", grantReservationMessage))
		}
		var value map[string]interface{}
		if raw, ok := obj.(runtime.Unstructured); ok {
			value = raw.UnstructuredContent()
		} else {
			value, err = runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
			if err != nil {
				return admission.NewForbidden(a, fmt.Errorf("Apps registration fields cannot be inspected"))
			}
		}
		group, _, err := unstructured.NestedString(value, "spec", "group")
		if err != nil {
			return admission.NewForbidden(a, fmt.Errorf("Apps registration group cannot be inspected"))
		}
		if group != GrantGroup {
			continue
		}
		if service {
			return admission.NewForbidden(a, fmt.Errorf("%s", grantReservationMessage))
		}
		plural, _, err := unstructured.NestedString(value, "spec", "names", "plural")
		if err != nil {
			return admission.NewForbidden(a, fmt.Errorf("Apps registration resource cannot be inspected"))
		}
		if plural == GrantResource {
			return admission.NewForbidden(a, fmt.Errorf("%s", grantReservationMessage))
		}
	}
	return nil
}
