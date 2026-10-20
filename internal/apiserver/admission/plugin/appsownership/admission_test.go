package appsownership

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	authenticationv1 "k8s.io/api/authentication/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/registry/rest"
)

func object(labels map[string]string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "compute.datumapis.com/v1alpha", "kind": "Workload", "metadata": map[string]interface{}{"name": "api", "namespace": "default"}}}
	o.SetLabels(labels)
	return o
}

func TestBootstrapOwnershipGuard(t *testing.T) {
	clean := object(nil)
	marked := object(map[string]string{ProjectUIDLabel: "project-uid", AppUIDLabel: "app-uid"})
	partial := object(map[string]string{AppUIDLabel: ""})
	annotation := object(nil)
	annotation.SetAnnotations(map[string]string{ProjectUIDLabel: ""})
	terminating := marked.DeepCopy()
	now := metav1.Now()
	terminating.SetDeletionTimestamp(&now)
	cases := []struct {
		name        string
		op          admission.Operation
		old, new    runtime.Object
		subresource string
		deny        bool
	}{
		{"unmarked create", admission.Create, nil, clean, "", false},
		{"unmarked update", admission.Update, clean, clean, "", false},
		{"unmarked delete", admission.Delete, clean, nil, "", false},
		{"forged complete marker", admission.Create, nil, marked, "", true},
		{"empty partial marker", admission.Create, nil, partial, "", true},
		{"annotation instead of label", admission.Create, nil, annotation, "", true},
		{"adopt unmarked object", admission.Update, clean, marked, "", true},
		{"strip marker in patch or apply", admission.Update, marked, clean, "", true},
		{"preserve marker", admission.Update, marked, marked, "", true},
		{"delete old marked object", admission.Delete, marked, nil, "", true},
		{"status no bypass", admission.Update, marked, marked, "status", true},
		{"finalizer no bypass", admission.Update, terminating, terminating, "", true},
		{"missing old update", admission.Update, nil, clean, "", true},
		{"missing old delete", admission.Delete, nil, nil, "", true},
	}
	for _, gr := range []schema.GroupVersionResource{{Group: "compute.datumapis.com", Version: "v1alpha", Resource: "workloads"}, {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}} {
		for _, caller := range []*user.DefaultInfo{{Name: "customer"}, {Name: "apps-controller"}, {Name: "release-worker"}, {Name: "admin", Groups: []string{user.SystemPrivilegedGroup}}, {Name: "system:serviceaccount:apps-system:controller", UID: "forged-uid"}} {
			for _, tc := range cases {
				t.Run(gr.Resource+"/"+caller.Name+"/"+tc.name, func(t *testing.T) {
					attrs := admission.NewAttributesRecord(tc.new, tc.old, gr.GroupVersion().WithKind("Workload"), "default", "api", gr, tc.subresource, tc.op, nil, false, caller)
					err := NewPlugin().Validate(context.Background(), attrs, nil)
					if tc.deny {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}
				})
			}
		}
	}
}

func TestOperations(t *testing.T) {
	p := NewPlugin()
	for _, op := range []admission.Operation{admission.Create, admission.Update, admission.Delete} {
		require.True(t, p.Handles(op))
	}
	require.False(t, p.Handles(admission.Connect))
}

// Exercise the actual Kubernetes deletion adapter: DeleteOptions are operation
// options, and each DeleteCollection member is passed as the old object.
func TestKubernetesDeleteAdapter(t *testing.T) {
	gr := schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}
	opts := &metav1.DeleteOptions{}
	attrs := admission.NewAttributesRecord(nil, nil, gr.GroupVersion().WithKind("HTTPRoute"), "default", "api", gr, "", admission.Delete, opts, false, &user.DefaultInfo{Name: "customer"})
	validate := rest.AdmissionToValidateObjectDeleteFunc(NewPlugin(), attrs, nil)
	require.NoError(t, validate(context.Background(), object(nil)))
	require.Error(t, validate(context.Background(), object(map[string]string{AppUIDLabel: "owner"})))
}

func TestUnmarkedNativeRequestShapes(t *testing.T) {
	cases := []struct {
		name        string
		obj         runtime.Object
		subresource string
	}{
		{"TokenReview", &authenticationv1.TokenReview{}, ""},
		{"Eviction", &policyv1.Eviction{}, "eviction"},
		{"Binding", &corev1.Binding{}, "binding"},
		{"Scale", &autoscalingv1.Scale{}, "scale"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gr := schema.GroupVersionResource{Version: "v1", Resource: "requests"}
			attrs := admission.NewAttributesRecord(tc.obj, nil, schema.GroupVersionKind{Kind: tc.name}, "default", "api", gr, tc.subresource, admission.Create, nil, false, &user.DefaultInfo{Name: "customer"})
			require.NoError(t, NewPlugin().Validate(context.Background(), attrs, nil))
		})
	}
	// Scale lacks its parent's labels. This passes as an unmarked request and is
	// intentionally NOT evidence that a marked parent's scale is protected.
	gr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	attrs := admission.NewAttributesRecord(&autoscalingv1.Scale{}, &autoscalingv1.Scale{}, schema.GroupVersionKind{Kind: "Scale"}, "default", "api", gr, "scale", admission.Update, nil, false, &user.DefaultInfo{Name: "customer"})
	require.NoError(t, NewPlugin().Validate(context.Background(), attrs, nil))
}
