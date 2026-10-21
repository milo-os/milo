package appsownership

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	milorequest "go.miloapis.com/milo/pkg/request"
	datumfilters "go.miloapis.com/milo/pkg/server/filters"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

var statusRule = StatusWriter{Username: "native-controller", UID: "native-uid", Group: "compute.datumapis.com", Version: "v1alpha", Resource: "workloads", Kind: "Workload"}

func provenanceContext(t *testing.T, original user.Info, header http.Header) context.Context {
	t.Helper()
	var ctx context.Context
	h := datumfilters.WithAuthenticationProvenance(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() }))
	req := httptest.NewRequest(http.MethodPut, "https://example.test/status", nil)
	req = req.WithContext(request.WithUser(req.Context(), original))
	req.Header = header
	h.ServeHTTP(httptest.NewRecorder(), req)
	require.NotNil(t, ctx)
	return milorequest.WithProject(ctx, "project-a")
}

func statusObject() *unstructured.Unstructured {
	obj := object(map[string]string{ProjectUIDLabel: "project-uid", AppUIDLabel: "app-uid"})
	obj.SetUID(types.UID("resource-uid"))
	obj.SetResourceVersion("7")
	obj.Object["spec"] = map[string]interface{}{"mode": "preserved"}
	return obj
}

func statusProject() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "resourcemanager.miloapis.com/v1alpha1", "kind": "Project", "metadata": map[string]interface{}{"name": "project-a", "uid": "project-uid"}}}
}

func TestConfiguredStatusBoundary(t *testing.T) {
	native := &user.DefaultInfo{Name: statusRule.Username, UID: statusRule.UID}
	for _, tc := range []struct {
		name                                                                   string
		mutate                                                                 func(*unstructured.Unstructured, *unstructured.Unstructured)
		original                                                               user.Info
		effective                                                              user.Info
		headers                                                                http.Header
		noProvenance, noProject, noClient, noRule, missingProject, lookupError bool
		projectUID                                                             string
		deletingProject                                                        bool
		resource, version, kind, subresource                                   string
		op                                                                     admission.Operation
		want                                                                   bool
	}{
		{name: "authenticated exact status", want: true},
		{name: "server-managed fields", want: true, mutate: func(_, n *unstructured.Unstructured) {
			n.SetResourceVersion("8")
			n.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "native-controller"}})
		}},
		{name: "customer", original: &user.DefaultInfo{Name: "customer", UID: "customer-uid"}},
		{name: "same username different UID", original: &user.DefaultInfo{Name: statusRule.Username, UID: "recreated-identity"}},
		{name: "effective identity substituted", effective: &user.DefaultInfo{Name: "other", UID: "other"}},
		{name: "no provenance", noProvenance: true},
		{name: "self impersonation", headers: http.Header{"Impersonate-User": {statusRule.Username}}},
		{name: "empty impersonation header", headers: http.Header{"Impersonate-User": {""}}},
		{name: "unknown impersonation header", headers: http.Header{"Impersonate-Other": {"ignored"}}},
		{name: "project missing", noProject: true},
		{name: "root client missing", noClient: true},
		{name: "default configuration", noRule: true},
		{name: "project not found", missingProject: true},
		{name: "project lookup unavailable", lookupError: true},
		{name: "recreated Project", projectUID: "replacement-project"},
		{name: "Project deleting", deletingProject: true},
		{name: "different resource", resource: "instances"},
		{name: "unconfigured version", version: "v1beta1"},
		{name: "kind mismatch", kind: "Other"},
		{name: "ordinary update", subresource: "main"},
		{name: "finalize", subresource: "finalize"},
		{name: "create", op: admission.Create},
		{name: "delete", op: admission.Delete},
		{name: "spec changed", mutate: func(_, n *unstructured.Unstructured) { n.Object["spec"] = map[string]interface{}{"mode": "changed"} }},
		{name: "marker stripped", mutate: func(_, n *unstructured.Unstructured) { n.SetLabels(nil) }},
		{name: "marker rebound", mutate: func(_, n *unstructured.Unstructured) {
			n.SetLabels(map[string]string{ProjectUIDLabel: "project-uid", AppUIDLabel: "other-app"})
		}},
		{name: "resource UID changed", mutate: func(_, n *unstructured.Unstructured) { n.SetUID("replacement") }},
		{name: "resource name changed", mutate: func(_, n *unstructured.Unstructured) { n.SetName("other") }},
		{name: "resource namespace changed", mutate: func(_, n *unstructured.Unstructured) { n.SetNamespace("other") }},
		{name: "additional label changed", mutate: func(_, n *unstructured.Unstructured) {
			labels := n.GetLabels()
			labels["changed"] = "yes"
			n.SetLabels(labels)
		}},
		{name: "annotation changed", mutate: func(_, n *unstructured.Unstructured) { n.SetAnnotations(map[string]string{"changed": "yes"}) }},
		{name: "finalizer changed", mutate: func(_, n *unstructured.Unstructured) { n.SetFinalizers([]string{"native.example/finalizer"}) }},
		{name: "ownerRef added", mutate: func(_, n *unstructured.Unstructured) {
			n.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: "external", UID: "other"}})
		}},
		{name: "existing ownerRef", mutate: func(o, n *unstructured.Unstructured) {
			refs := []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: "external", UID: "other"}}
			o.SetOwnerReferences(refs)
			n.SetOwnerReferences(refs)
		}},
		{name: "existing reserved annotation", mutate: func(o, n *unstructured.Unstructured) {
			o.SetAnnotations(map[string]string{AppUIDLabel: "app-uid"})
			n.SetAnnotations(o.GetAnnotations())
		}},
		{name: "partial markers", mutate: func(o, n *unstructured.Unstructured) {
			o.SetLabels(map[string]string{ProjectUIDLabel: "project-uid"})
			n.SetLabels(o.GetLabels())
		}},
		{name: "empty markers", mutate: func(o, n *unstructured.Unstructured) {
			o.SetLabels(map[string]string{ProjectUIDLabel: "project-uid", AppUIDLabel: ""})
			n.SetLabels(o.GetLabels())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.original
			if original == nil {
				original = native
			}
			effective := tc.effective
			if effective == nil {
				effective = original
			}
			ctx := provenanceContext(t, original, tc.headers)
			if tc.noProvenance {
				ctx = milorequest.WithProject(context.Background(), "project-a")
			}
			if tc.noProject {
				ctx = milorequest.WithProject(ctx, "")
			}
			old, next := statusObject(), statusObject()
			next.Object["status"] = map[string]interface{}{"readyReplicas": int64(1)}
			if tc.mutate != nil {
				tc.mutate(old, next)
			}
			project := statusProject()
			if tc.projectUID != "" {
				project.SetUID(types.UID(tc.projectUID))
			}
			if tc.deletingProject {
				now := metav1.Now()
				project.SetDeletionTimestamp(&now)
			}
			client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), project)
			if tc.missingProject {
				client = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
			}
			if tc.lookupError {
				client.PrependReactor("get", "projects", func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("unavailable") })
			}
			p := NewPlugin()
			p.statusWriters = []StatusWriter{statusRule}
			p.SetDynamicClient(client)
			if tc.noClient {
				p.SetDynamicClient(nil)
			}
			if tc.noRule {
				p.statusWriters = nil
			}
			resource, version, kind := tc.resource, tc.version, tc.kind
			if resource == "" {
				resource = statusRule.Resource
			}
			if version == "" {
				version = statusRule.Version
			}
			if kind == "" {
				kind = statusRule.Kind
			}
			sub := tc.subresource
			if sub == "" {
				sub = "status"
			}
			if sub == "main" {
				sub = ""
			}
			op := tc.op
			if op == "" {
				op = admission.Update
			}
			attrs := admission.NewAttributesRecord(next, old, schema.GroupVersionKind{Group: statusRule.Group, Version: version, Kind: kind}, "default", "api", schema.GroupVersionResource{Group: statusRule.Group, Version: version, Resource: resource}, sub, op, nil, false, effective)
			beforeOld, beforeNext := old.DeepCopy(), next.DeepCopy()
			err := p.Validate(ctx, attrs, nil)
			if tc.want {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, beforeOld, old)
			require.Equal(t, beforeNext, next)
		})
	}
}

func TestStatusWriterConfiguration(t *testing.T) {
	valid := `apiVersion: apiserver.miloapis.com/v1alpha1
kind: AppsOwnershipConfiguration
statusWriters:
- username: native-controller
  uid: native-uid
  group: compute.datumapis.com
  version: v1alpha
  resource: workloads
  kind: Workload
`
	rules, err := readConfiguration(strings.NewReader(valid))
	require.NoError(t, err)
	require.Equal(t, []StatusWriter{statusRule}, rules.StatusWriters)
	for _, raw := range []string{"", "{}", valid + "unknown: true\n", strings.Replace(valid, "uid: native-uid", "uid: ''", 1), strings.Replace(valid, "group: compute.datumapis.com", "group: '*'", 1), strings.Replace(valid, "resource: workloads", "resource: workloads/status", 1), valid + "statusWriters: []\n", strings.Repeat(" ", 64*1024+1)} {
		_, err := readConfiguration(strings.NewReader(raw))
		require.Error(t, err)
	}
	rules, err = readConfiguration(nil)
	require.NoError(t, err)
	require.Empty(t, rules)
	p := NewPlugin()
	require.NoError(t, p.ValidateInitialization())
	p.statusWriters = []StatusWriter{statusRule}
	require.Error(t, p.ValidateInitialization())
	p.SetDynamicClient(dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()))
	require.NoError(t, p.ValidateInitialization())
}
