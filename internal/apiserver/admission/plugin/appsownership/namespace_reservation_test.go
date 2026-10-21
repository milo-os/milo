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

func TestNamespaceReservationBootstrap(t *testing.T) {
	for _, metadata := range []string{"label", "annotation"} {
		for _, value := range []string{"", "reservation-uid"} {
			clean := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "apps"}}
			held := clean.DeepCopy()
			if metadata == "label" {
				held.Labels = map[string]string{NamespaceReservationKey: value}
			} else {
				held.Annotations = map[string]string{NamespaceReservationKey: value}
			}
			for _, tc := range []struct {
				name        string
				op          admission.Operation
				old, new    runtime.Object
				subresource string
				deny        bool
			}{
				{"ordinary create", admission.Create, nil, clean, "", false},
				{"ordinary update", admission.Update, clean, clean, "", false},
				{"ordinary delete", admission.Delete, clean, nil, "", false},
				{"ordinary finalize", admission.Update, clean, clean, "finalize", false},
				{"forged reservation", admission.Create, nil, held, "", true},
				{"acquisition disabled", admission.Update, clean, held, "", true},
				{"removal disabled", admission.Update, held, clean, "", true},
				{"reserved delete", admission.Delete, held, nil, "", true},
				{"reserved status", admission.Update, held, held, "status", true},
				{"reserved finalize", admission.Update, held, clean, "finalize", true},
				{"missing old delete", admission.Delete, nil, nil, "", true},
				{"missing old finalize", admission.Update, nil, clean, "finalize", true},
			} {
				t.Run(metadata+"/"+value+"/"+tc.name, func(t *testing.T) {
					// No scope, caller or dry-run exemption is available.
					for _, caller := range []user.Info{&user.DefaultInfo{Name: "customer"}, &user.DefaultInfo{Name: "admin", Groups: []string{user.SystemPrivilegedGroup}}} {
						attrs := admission.NewAttributesRecord(tc.new, tc.old, corev1.SchemeGroupVersion.WithKind("Namespace"), "", "apps", corev1.SchemeGroupVersion.WithResource("namespaces"), tc.subresource, tc.op, nil, true, caller)
						err := NewPlugin().Validate(context.Background(), attrs, nil)
						if tc.deny {
							require.Error(t, err)
						} else {
							require.NoError(t, err)
						}
					}
				})
			}
		}
	}
}

func TestNamespaceReservationReservedOnEveryKind(t *testing.T) {
	for _, metadata := range []string{"label", "annotation"} {
		obj := object(nil)
		if metadata == "label" {
			obj.SetLabels(map[string]string{NamespaceReservationKey: ""})
		} else {
			obj.SetAnnotations(map[string]string{NamespaceReservationKey: ""})
		}
		for _, gr := range []schema.GroupVersionResource{
			{Group: "compute.datumapis.com", Version: "v1alpha", Resource: "workloads"},
			{Group: "resourcemanager.miloapis.com", Version: "v1alpha1", Resource: "projects"},
		} {
			attrs := admission.NewAttributesRecord(obj, nil, obj.GroupVersionKind(), "", "api", gr, "", admission.Create, nil, false, &user.DefaultInfo{Name: "customer"})
			require.Error(t, NewPlugin().Validate(context.Background(), attrs, nil))
		}
	}
}

// Existing optional native rules must not create an exception to the independent
// reservation key. In particular, an unchanged reservation would otherwise pass
// the native writers' same-outside-status/finalizers comparisons.
func TestNamespaceReservationPrecedesNativeWriters(t *testing.T) {
	for _, sub := range []string{"status", ""} {
		for _, location := range []string{"label", "annotation"} {
			t.Run(sub+"/"+location, func(t *testing.T) {
				old := statusObject()
				next := old.DeepCopy()
				if sub == "status" {
					next.Object["status"] = map[string]any{"ready": true}
				} else {
					next.SetFinalizers([]string{workloadFinalizer})
				}
				p := NewPlugin()
				p.statusWriters = []StatusWriter{statusRule}
				p.finalizerWriters = []FinalizerWriter{{StatusWriter: statusRule, Finalizer: workloadFinalizer}}
				root := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), statusProject())
				p.SetDynamicClient(root)
				actor := &user.DefaultInfo{Name: statusRule.Username, UID: statusRule.UID}
				ctx := provenanceContext(t, actor, nil)
				attributes := func() admission.Attributes {
					return admission.NewAttributesRecord(next, old, old.GroupVersionKind(), old.GetNamespace(), old.GetName(), schema.GroupVersionResource{Group: statusRule.Group, Version: statusRule.Version, Resource: statusRule.Resource}, sub, admission.Update, nil, false, actor)
				}
				require.NoError(t, p.Validate(ctx, attributes(), nil))
				for _, o := range []*unstructured.Unstructured{old, next} {
					if location == "label" {
						labels := o.GetLabels()
						labels[NamespaceReservationKey] = ""
						o.SetLabels(labels)
					} else {
						o.SetAnnotations(map[string]string{NamespaceReservationKey: ""})
					}
				}
				root.ClearActions()
				require.Error(t, p.Validate(ctx, attributes(), nil))
				require.Empty(t, root.Actions(), "reservation must deny before a live Project lookup")
			})
		}
	}
}
