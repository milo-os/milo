package appsownership

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
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
