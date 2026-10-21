package appsownership

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestProjectReservationEveryKindAndShape(t *testing.T) {
	for _, field := range []string{"labels", "annotations"} {
		for _, value := range []any{"", "reservation-uid", nil, int64(1), map[string]any{"partial": true}} {
			held := object(nil)
			held.Object["metadata"].(map[string]any)[field] = map[string]any{ProjectReservationKey: value}
			clean := object(nil)
			for _, tc := range []struct {
				name      string
				op        admission.Operation
				old, next runtime.Object
				sub       string
			}{
				{"forge", admission.Create, nil, held, ""},
				{"adopt", admission.Update, clean, held, ""},
				{"strip", admission.Update, held, clean, ""},
				{"preserve", admission.Update, held, held, ""},
				{"status", admission.Update, held, held, "status"},
				{"finalize", admission.Update, held, held, "finalize"},
				{"delete", admission.Delete, held, &metav1.DeleteOptions{}, ""},
			} {
				t.Run(field+"/"+tc.name, func(t *testing.T) {
					for _, gr := range []schema.GroupVersionResource{{Group: "resourcemanager.miloapis.com", Version: "v1alpha1", Resource: "projects"}, {Group: "compute.datumapis.com", Version: "v1alpha", Resource: "workloads"}, {Version: "v1", Resource: "namespaces"}} {
						attrs := admission.NewAttributesRecord(tc.next, tc.old, gr.GroupVersion().WithKind("Project"), "", "api", gr, tc.sub, tc.op, nil, false, &user.DefaultInfo{Name: "admin", Groups: []string{user.SystemPrivilegedGroup}})
						err := NewPlugin().Validate(context.Background(), attrs, nil)
						require.ErrorContains(t, err, "project reservation")
					}
				})
			}
		}
	}
	for _, field := range []string{"labels", "annotations"} {
		held := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "probe"}}
		if field == "labels" {
			held.Labels = map[string]string{ProjectReservationKey: ""}
		} else {
			held.Annotations = map[string]string{ProjectReservationKey: ""}
		}
		attrs := admission.NewAttributesRecord(nil, held, corev1.SchemeGroupVersion.WithKind("Namespace"), "", "probe", corev1.SchemeGroupVersion.WithResource("namespaces"), "", admission.Delete, nil, false, &user.DefaultInfo{})
		require.ErrorContains(t, NewPlugin().Validate(context.Background(), attrs, nil), "project reservation")
	}
}

func TestProjectReservationPrecedesNativeExceptions(t *testing.T) {
	native := &user.DefaultInfo{Name: statusRule.Username, UID: statusRule.UID}
	for _, mode := range []string{"status", "finalizer"} {
		for _, field := range []string{"labels", "annotations"} {
			t.Run(mode+"/"+field, func(t *testing.T) {
				p := NewPlugin()
				p.statusWriters = []StatusWriter{statusRule}
				p.finalizerWriters = []FinalizerWriter{{StatusWriter: statusRule, Finalizer: workloadFinalizer}}
				p.SetDynamicClient(dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), statusProject()))
				old, next := statusObject(), statusObject()
				sub := "status"
				if mode == "status" {
					next.Object["status"] = map[string]any{"ready": true}
				} else {
					sub = ""
					next.SetFinalizers([]string{workloadFinalizer})
				}
				attrs := func() admission.Attributes {
					return admission.NewAttributesRecord(next, old, schema.GroupVersionKind{Group: statusRule.Group, Version: statusRule.Version, Kind: statusRule.Kind}, old.GetNamespace(), old.GetName(), schema.GroupVersionResource{Group: statusRule.Group, Version: statusRule.Version, Resource: statusRule.Resource}, sub, admission.Update, nil, false, native)
				}
				require.NoError(t, p.Validate(provenanceContext(t, native, nil), attrs(), nil), "the native rule must actually qualify without the reservation")
				for _, obj := range []*unstructured.Unstructured{old, next} {
					values, _, err := unstructured.NestedStringMap(obj.Object, "metadata", field)
					require.NoError(t, err)
					if values == nil {
						values = map[string]string{}
					}
					values[ProjectReservationKey] = ""
					require.NoError(t, unstructured.SetNestedStringMap(obj.Object, values, "metadata", field))
				}
				require.ErrorContains(t, p.Validate(provenanceContext(t, native, nil), attrs(), nil), "project reservation")
			})
		}
	}
}
