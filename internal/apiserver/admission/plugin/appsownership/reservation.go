package appsownership

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
)

// ProjectReservationKey reserves a future pre-deletion hold on the root Project.
// Acquisition, mutation and release have no authorized writer in this slice.
// Reserve both metadata spellings on every kind before any native exception.
const ProjectReservationKey = "apps.miloapis.com/project-reservation"

func validateProjectReservation(a admission.Attributes) error {
	objects := []runtime.Object{a.GetOldObject()}
	if a.GetOperation() != admission.Delete {
		objects = append(objects, a.GetObject())
	}
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		if native, ok := obj.(runtime.Unstructured); ok {
			for _, field := range []string{"labels", "annotations"} {
				_, present, err := unstructured.NestedFieldNoCopy(native.UnstructuredContent(), "metadata", field, ProjectReservationKey)
				if err != nil {
					return admission.NewForbidden(a, fmt.Errorf("Apps project reservation metadata cannot be inspected"))
				}
				if present {
					return projectReservationDenied(a)
				}
			}
			continue
		}
		accessor, err := meta.Accessor(obj)
		if err != nil {
			return admission.NewForbidden(a, fmt.Errorf("Apps project reservation metadata cannot be inspected"))
		}
		if _, present := accessor.GetLabels()[ProjectReservationKey]; present {
			return projectReservationDenied(a)
		}
		if _, present := accessor.GetAnnotations()[ProjectReservationKey]; present {
			return projectReservationDenied(a)
		}
	}
	return nil
}
func projectReservationDenied(a admission.Attributes) error {
	return admission.NewForbidden(a, fmt.Errorf("Apps project reservation is reserved; acquisition and release remain disabled"))
}
