package appsownership

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	regv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

func TestGrantFamilyReserved(t *testing.T) {
	for _, registration := range []string{"", GrantRegistrationV1} {
		for _, identity := range []string{"customer", "system-admin", "native-status", "native-finalizer"} {
			for _, version := range []string{"v1alpha1", "v1", "unknown"} {
				for _, sub := range []string{"", "status", "finalizers", "scale"} {
					for _, op := range []admission.Operation{admission.Create, admission.Update, admission.Delete} {
						for _, namespace := range []string{"", "default"} {
							attrs := admission.NewAttributesRecord(nil, nil, schema.GroupVersionKind{Group: GrantGroup, Version: version, Kind: "Unknown"}, namespace, "", schema.GroupVersionResource{Group: GrantGroup, Version: version, Resource: GrantResource}, sub, op, nil, false, &user.DefaultInfo{Name: identity, UID: "principal-uid", Groups: []string{user.SystemPrivilegedGroup}})
							p := NewPlugin()
							p.grantRegistration = registration
							rule := StatusWriter{Username: identity, UID: "principal-uid", Group: GrantGroup, Version: version, Resource: GrantResource, Kind: "Unknown"}
							p.statusWriters = []StatusWriter{rule}
							p.finalizerWriters = []FinalizerWriter{{StatusWriter: rule, Finalizer: "example.test/native"}}
							err := p.Validate(context.Background(), attrs, nil)
							require.ErrorContains(t, err, grantReservationMessage, "registration=%q identity=%s version=%s subresource=%s operation=%s", registration, identity, version, sub, op)
						}
					}
				}
			}
		}
	}
}
func registrationObject(name, group, plural string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{"metadata": map[string]interface{}{"name": name}, "spec": map[string]interface{}{"group": group, "names": map[string]interface{}{"plural": plural}}}}
}
func TestGrantRegistrationReserved(t *testing.T) {
	crd := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	service := schema.GroupVersionResource{Group: "apiregistration.k8s.io", Version: "v1", Resource: "apiservices"}
	cases := []struct {
		name        string
		resource    schema.GroupVersionResource
		requestName string
		object      runtime.Object
		deny        bool
	}{
		{"CRD request name", crd, GrantCRDName, &extv1.CustomResourceDefinition{}, true},
		{"CRD typed metadata", crd, "", &extv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: GrantCRDName}}, true},
		{"CRD typed spec", crd, "other", &extv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "other"}, Spec: extv1.CustomResourceDefinitionSpec{Group: GrantGroup, Names: extv1.CustomResourceDefinitionNames{Plural: GrantResource}}}, true},
		{"CRD raw spec", crd, "other", registrationObject("other", GrantGroup, GrantResource), true},
		{"unrelated Apps CRD", crd, "", registrationObject("apptemplates."+GrantGroup, GrantGroup, "apptemplates"), false},
		{"unrelated group CRD", crd, "", registrationObject("appinstallgrants.example.test", "example.test", GrantResource), false},
		{"APIService typed metadata", service, "", &regv1.APIService{ObjectMeta: metav1.ObjectMeta{Name: "v1." + GrantGroup}}, true},
		{"APIService typed spec", service, "other", &regv1.APIService{ObjectMeta: metav1.ObjectMeta{Name: "other"}, Spec: regv1.APIServiceSpec{Group: GrantGroup}}, true},
		{"APIService request name", service, "v9." + GrantGroup, &regv1.APIService{}, true},
		{"APIService raw spec", service, "other", registrationObject("other", GrantGroup, ""), true},
		{"unrelated APIService", service, "", &regv1.APIService{ObjectMeta: metav1.ObjectMeta{Name: "v1.example.test"}, Spec: regv1.APIServiceSpec{Group: "example.test"}}, false},
	}
	for _, tc := range cases {
		for _, op := range []admission.Operation{admission.Create, admission.Update, admission.Delete} {
			t.Run(tc.name+"/"+string(op), func(t *testing.T) {
				var old, next runtime.Object
				if op != admission.Create {
					old = tc.object
				}
				if op != admission.Delete {
					next = tc.object
				}
				attrs := admission.NewAttributesRecord(next, old, schema.GroupVersionKind{}, "", tc.requestName, tc.resource, "", op, nil, false, &user.DefaultInfo{Name: "customer"})
				err := NewPlugin().Validate(context.Background(), attrs, nil)
				if tc.deny {
					require.ErrorContains(t, err, grantReservationMessage)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
	// A new object cannot hide the protected old registration, even if a fake
	// conversion removes its identifying fields. Inspect both old and new.
	old := registrationObject("", GrantGroup, GrantResource)
	next := registrationObject("other", "elsewhere.test", "other")
	for _, objects := range [][2]runtime.Object{{old, next}, {next, old}} {
		a := admission.NewAttributesRecord(objects[1], objects[0], schema.GroupVersionKind{}, "", "other", crd, "", admission.Update, nil, false, nil)
		require.ErrorContains(t, NewPlugin().Validate(context.Background(), a, nil), grantReservationMessage)
	}
	malformed := registrationObject("other", "elsewhere.test", "other")
	malformed.Object["spec"] = true
	a := admission.NewAttributesRecord(malformed, nil, schema.GroupVersionKind{}, "", "other", crd, "", admission.Create, nil, false, nil)
	require.ErrorContains(t, NewPlugin().Validate(context.Background(), a, nil), "cannot be inspected")
	// Similar strings on an ordinary object are not enrollment.
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: GrantCRDName}}
	a = admission.NewAttributesRecord(cm, nil, corev1.SchemeGroupVersion.WithKind("ConfigMap"), "default", cm.Name, corev1.SchemeGroupVersion.WithResource("configmaps"), "", admission.Create, nil, false, nil)
	require.NoError(t, NewPlugin().Validate(context.Background(), a, nil))
}
