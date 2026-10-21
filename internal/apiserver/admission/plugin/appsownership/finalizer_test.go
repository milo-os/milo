package appsownership

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	milorequest "go.miloapis.com/milo/pkg/request"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

const workloadFinalizer = "compute.datumapis.com/workload-controller"

func TestConfiguredFinalizerBoundary(t *testing.T) {
	native := &user.DefaultInfo{Name: statusRule.Username, UID: statusRule.UID}
	for _, tc := range []struct {
		name                                                   string
		mutate                                                 func(*unstructured.Unstructured, *unstructured.Unstructured)
		rule                                                   func(*FinalizerWriter)
		header                                                 http.Header
		original                                               user.Info
		noProvenance, noProject, noClient, noRule, lookupError bool
		projectUID                                             string
		deletingProject                                        bool
		operation                                              admission.Operation
		subresource                                            string
		want                                                   bool
	}{
		{name: "add exact before termination", want: true},
		{name: "storage managed fields", want: true, mutate: func(_, n *unstructured.Unstructured) {
			n.SetResourceVersion("8")
			n.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "native"}})
		}},
		{name: "preserve other finalizers", want: true, mutate: func(o, n *unstructured.Unstructured) {
			o.SetFinalizers([]string{"other.example/hold"})
			n.SetFinalizers([]string{"other.example/hold", workloadFinalizer})
		}},
		{name: "remove during termination", want: true, mutate: func(o, n *unstructured.Unstructured) {
			stamp := metav1.Now()
			o.SetDeletionTimestamp(&stamp)
			n.SetDeletionTimestamp(&stamp)
			o.SetFinalizers([]string{workloadFinalizer, "other.example/hold"})
			n.SetFinalizers([]string{"other.example/hold"})
		}},
		{name: "remove last during termination completes deletion", want: true, mutate: func(o, n *unstructured.Unstructured) {
			stamp := metav1.Now()
			o.SetDeletionTimestamp(&stamp)
			n.SetDeletionTimestamp(&stamp)
			o.SetFinalizers([]string{workloadFinalizer})
			n.SetFinalizers(nil)
		}},
		{name: "remove before termination", mutate: func(o, n *unstructured.Unstructured) {
			o.SetFinalizers([]string{workloadFinalizer})
			n.SetFinalizers(nil)
		}},
		{name: "add during termination", mutate: func(o, n *unstructured.Unstructured) {
			stamp := metav1.Now()
			o.SetDeletionTimestamp(&stamp)
			n.SetDeletionTimestamp(&stamp)
		}},
		{name: "initiate termination", mutate: func(_, n *unstructured.Unstructured) { stamp := metav1.Now(); n.SetDeletionTimestamp(&stamp) }},
		{name: "change grace period", mutate: func(_, n *unstructured.Unstructured) { zero := int64(0); n.SetDeletionGracePeriodSeconds(&zero) }},
		{name: "remove other finalizer", mutate: func(o, n *unstructured.Unstructured) { o.SetFinalizers([]string{"other.example/hold"}) }},
		{name: "add other finalizer", mutate: func(_, n *unstructured.Unstructured) {
			n.SetFinalizers([]string{workloadFinalizer, "other.example/hold"})
		}},
		{name: "reorder other finalizers", mutate: func(o, n *unstructured.Unstructured) {
			o.SetFinalizers([]string{"other.example/a", "other.example/b"})
			n.SetFinalizers([]string{"other.example/b", workloadFinalizer, "other.example/a"})
		}},
		{name: "duplicate finalizer", mutate: func(_, n *unstructured.Unstructured) { n.SetFinalizers([]string{workloadFinalizer, workloadFinalizer}) }},
		{name: "malformed old finalizer list", mutate: func(o, n *unstructured.Unstructured) {
			o.Object["metadata"].(map[string]any)["finalizers"] = []any{int64(42)}
		}},
		{name: "malformed new finalizer list", mutate: func(o, n *unstructured.Unstructured) {
			stamp := metav1.Now()
			o.SetDeletionTimestamp(&stamp)
			n.SetDeletionTimestamp(&stamp)
			o.SetFinalizers([]string{workloadFinalizer})
			n.Object["metadata"].(map[string]any)["finalizers"] = []any{int64(42)}
		}},
		{name: "no change", mutate: func(_, n *unstructured.Unstructured) { n.SetFinalizers(nil) }},
		{name: "mixed spec", mutate: func(_, n *unstructured.Unstructured) { n.Object["spec"] = map[string]any{"mode": "changed"} }},
		{name: "mixed status", mutate: func(_, n *unstructured.Unstructured) { n.Object["status"] = map[string]any{"ready": true} }},
		{name: "label strip", mutate: func(_, n *unstructured.Unstructured) { n.SetLabels(nil) }},
		{name: "label rebound", mutate: func(_, n *unstructured.Unstructured) {
			n.SetLabels(map[string]string{ProjectUIDLabel: "project-uid", AppUIDLabel: "foreign-app"})
		}},
		{name: "extra annotation", mutate: func(_, n *unstructured.Unstructured) { n.SetAnnotations(map[string]string{"other": "changed"}) }},
		{name: "partial markers", mutate: func(o, n *unstructured.Unstructured) {
			o.SetLabels(map[string]string{ProjectUIDLabel: "project-uid"})
			n.SetLabels(o.GetLabels())
		}},
		{name: "malformed marker", mutate: func(o, n *unstructured.Unstructured) {
			o.SetLabels(map[string]string{ProjectUIDLabel: "project-uid", AppUIDLabel: "not/valid"})
			n.SetLabels(o.GetLabels())
		}},
		{name: "existing reserved annotation", mutate: func(o, n *unstructured.Unstructured) {
			o.SetAnnotations(map[string]string{AppUIDLabel: "app-uid"})
			n.SetAnnotations(o.GetAnnotations())
		}},
		{name: "external ownerref", mutate: func(_, n *unstructured.Unstructured) {
			n.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: "outside", UID: "foreign"}})
		}},
		{name: "existing ownerref", mutate: func(o, n *unstructured.Unstructured) {
			refs := []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: "outside", UID: "foreign"}}
			o.SetOwnerReferences(refs)
			n.SetOwnerReferences(refs)
		}},
		{name: "resource UID changed", mutate: func(_, n *unstructured.Unstructured) { n.SetUID("replacement") }},
		{name: "name changed", mutate: func(_, n *unstructured.Unstructured) { n.SetName("other") }},
		{name: "namespace changed", mutate: func(_, n *unstructured.Unstructured) { n.SetNamespace("other") }},
		{name: "unconfigured principal", original: &user.DefaultInfo{Name: "customer", UID: "customer-uid"}},
		{name: "same name recreated principal", original: &user.DefaultInfo{Name: statusRule.Username, UID: "replacement-uid"}},
		{name: "self impersonation", header: http.Header{"Impersonate-User": {statusRule.Username}}},
		{name: "empty impersonation", header: http.Header{"Impersonate-User": {""}}},
		{name: "unknown impersonation", header: http.Header{"Impersonate-Unknown": {"ignored"}}},
		{name: "missing provenance", noProvenance: true},
		{name: "missing Project", noProject: true},
		{name: "Project unavailable", lookupError: true},
		{name: "recreated Project", projectUID: "replacement-project"},
		{name: "deleting Project", deletingProject: true},
		{name: "no root client", noClient: true},
		{name: "default guard", noRule: true},
		{name: "unconfigured finalizer", rule: func(r *FinalizerWriter) { r.Finalizer = "other.example/finalizer" }},
		{name: "wrong kind", rule: func(r *FinalizerWriter) { r.Kind = "Instance" }},
		{name: "wrong version", rule: func(r *FinalizerWriter) { r.Version = "v1beta1" }},
		{name: "status subresource", subresource: "status"},
		{name: "finalize subresource", subresource: "finalize"},
		{name: "delete operation", operation: admission.Delete},
		{name: "create operation", operation: admission.Create},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.original
			if original == nil {
				original = native
			}
			ctx := provenanceContext(t, original, tc.header)
			if tc.noProvenance {
				ctx = milorequest.WithProject(context.Background(), "project-a")
			}
			if tc.noProject {
				ctx = milorequest.WithProject(ctx, "")
			}
			old, next := statusObject(), statusObject()
			next.SetFinalizers([]string{workloadFinalizer})
			if tc.mutate != nil {
				tc.mutate(old, next)
			}
			project := statusProject()
			if tc.projectUID != "" {
				project.SetUID(types.UID(tc.projectUID))
			}
			if tc.deletingProject {
				stamp := metav1.Now()
				project.SetDeletionTimestamp(&stamp)
			}
			client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), project)
			if tc.lookupError {
				client.PrependReactor("get", "projects", func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("unavailable") })
			}
			p := NewPlugin()
			p.SetDynamicClient(client)
			rule := FinalizerWriter{StatusWriter: statusRule, Finalizer: workloadFinalizer}
			if tc.rule != nil {
				tc.rule(&rule)
			}
			p.finalizerWriters = []FinalizerWriter{rule}
			if tc.noRule {
				p.finalizerWriters = nil
			}
			if tc.noClient {
				p.SetDynamicClient(nil)
			}
			op := tc.operation
			if op == "" {
				op = admission.Update
			}
			attrs := admission.NewAttributesRecord(next, old, schema.GroupVersionKind{Group: statusRule.Group, Version: statusRule.Version, Kind: statusRule.Kind}, "default", "api", schema.GroupVersionResource{Group: statusRule.Group, Version: statusRule.Version, Resource: statusRule.Resource}, tc.subresource, op, nil, false, original)
			beforeOld, beforeNew := old.DeepCopy(), next.DeepCopy()
			err := p.Validate(ctx, attrs, nil)
			if tc.want {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, beforeOld, old)
			require.Equal(t, beforeNew, next)
		})
	}
}

func TestFinalizerWriterConfiguration(t *testing.T) {
	rule := FinalizerWriter{StatusWriter: statusRule, Finalizer: workloadFinalizer}
	config := Configuration{APIVersion: "apiserver.miloapis.com/v1alpha1", Kind: "AppsOwnershipConfiguration", FinalizerWriters: []FinalizerWriter{rule}}
	raw, err := json.Marshal(config)
	require.NoError(t, err)
	parsed, err := readConfiguration(strings.NewReader(string(raw)))
	require.NoError(t, err)
	require.Equal(t, config, parsed)
	for _, value := range []string{"", "*", "unqualified", "compute.datumapis.com/*", "compute.datumapis.com/a/b"} {
		invalid := config
		invalid.FinalizerWriters = []FinalizerWriter{{StatusWriter: statusRule, Finalizer: value}}
		raw, err = json.Marshal(invalid)
		require.NoError(t, err)
		_, err = readConfiguration(strings.NewReader(string(raw)))
		require.Error(t, err)
	}
	config.FinalizerWriters = append(config.FinalizerWriters, rule)
	raw, err = json.Marshal(config)
	require.NoError(t, err)
	_, err = readConfiguration(strings.NewReader(string(raw)))
	require.Error(t, err)
	config.FinalizerWriters = nil
	for i := 0; i < 33; i++ {
		copy := rule
		copy.Finalizer = fmt.Sprintf("compute.datumapis.com/finalizer-%d", i)
		config.FinalizerWriters = append(config.FinalizerWriters, copy)
	}
	raw, err = json.Marshal(config)
	require.NoError(t, err)
	_, err = readConfiguration(strings.NewReader(string(raw)))
	require.Error(t, err)
	config.FinalizerWriters = []FinalizerWriter{rule}
	raw, err = json.Marshal(config)
	require.NoError(t, err)
	unknown := strings.TrimSuffix(string(raw), "}") + `,"unknown":true}`
	_, err = readConfiguration(strings.NewReader(unknown))
	require.Error(t, err)
	p := NewPlugin()
	p.finalizerWriters = []FinalizerWriter{rule}
	require.Error(t, p.ValidateInitialization())
}
