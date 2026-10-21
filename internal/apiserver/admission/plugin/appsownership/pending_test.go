package appsownership

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership/pendingwire"
	milorequest "go.miloapis.com/milo/pkg/request"
	datumfilters "go.miloapis.com/milo/pkg/server/filters"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	request "k8s.io/apiserver/pkg/endpoints/request"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func pendingContext(t *testing.T, verb, sub string) (context.Context, user.Info) {
	t.Helper()
	var ctx context.Context
	handler := datumfilters.WithAuthenticationProvenance(datumfilters.ProjectContextAuthorizationDecorator(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() })))
	u := &user.DefaultInfo{Name: "customer", UID: "customer-uid", Groups: []string{"system:authenticated"}}
	r := httptest.NewRequest("POST", "https://localhost/", nil)
	r = r.WithContext(request.WithUser(milorequest.WithProject(r.Context(), "project"), u))
	handler.ServeHTTP(httptest.NewRecorder(), r)
	ri := &request.RequestInfo{IsResourceRequest: true, Verb: verb, APIGroup: GrantGroup, APIVersion: "v1alpha1", Resource: "apps", Subresource: sub}
	if verb != "create" {
		ri.Name = "web"
	}
	ctx = request.WithRequestInfo(ctx, ri)
	effective, _ := request.UserFrom(ctx)
	return ctx, effective
}
func pendingFixture(t *testing.T) *unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile("pendingwire/testdata/spec.json")
	require.NoError(t, err)
	var spec map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &spec))
	d, err := pendingwire.ValidateSpec(spec, "project-uid")
	require.NoError(t, err)
	o := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "apps.miloapis.com/v1alpha1", "kind": "App", "metadata": map[string]interface{}{"name": "web"}, "spec": spec}}
	o.SetGeneration(1)
	o.SetAnnotations(map[string]string{PendingProtocolAnnotation: PendingProtocol, PendingProjectAnnotation: "project-uid", PendingActorAnnotation: "customer-uid", PendingDigestAnnotation: d, PendingGenerationAnnotation: "1"})
	o.SetFinalizers([]string{PendingFinalizer})
	return o
}
func pendingPlugin() *Plugin {
	p := NewPlugin()
	p.pendingRequests = PendingRequestsV1
	p.dynamicClient = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "resourcemanager.miloapis.com/v1alpha1", "kind": "Project", "metadata": map[string]interface{}{"name": "project", "uid": "project-uid"}}})
	return p
}
func pendingAttrs(u user.Info, old, next *unstructured.Unstructured, op admission.Operation, sub string) admission.Attributes {
	var options runtime.Object
	if op == admission.Delete && old != nil {
		uid, rv := old.GetUID(), old.GetResourceVersion()
		options = &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}}
	}
	return pendingAttrsOptions(u, old, next, op, sub, options)
}
func pendingAttrsOptions(u user.Info, old, next *unstructured.Unstructured, op admission.Operation, sub string, options runtime.Object) admission.Attributes {
	var a, b runtime.Object
	if old != nil {
		a = old
	}
	if next != nil {
		b = next
	}
	return admission.NewAttributesRecord(b, a, schema.GroupVersionKind{Group: GrantGroup, Version: "v1alpha1", Kind: "App"}, "", "web", schema.GroupVersionResource{Group: GrantGroup, Version: "v1alpha1", Resource: "apps"}, sub, op, options, false, u)
}
func TestPendingCreateCustody(t *testing.T) {
	ctx, u := pendingContext(t, "create", "")
	p := pendingPlugin()
	require.NoError(t, p.Validate(ctx, pendingAttrs(u, nil, pendingFixture(t), admission.Create, ""), nil))
	for _, tc := range []struct {
		name   string
		change func(*unstructured.Unstructured)
	}{
		{"actor forgery", func(o *unstructured.Unstructured) {
			a := o.GetAnnotations()
			a[PendingActorAnnotation] = "other"
			o.SetAnnotations(a)
		}},
		{"new protocol", func(o *unstructured.Unstructured) {
			a := o.GetAnnotations()
			a[PendingProtocolAnnotation] = "install-request-v1"
			o.SetAnnotations(a)
		}},
		{"no hold", func(o *unstructured.Unstructured) { o.SetFinalizers(nil) }},
		{"ownerref", func(o *unstructured.Unstructured) { o.SetOwnerReferences([]metav1.OwnerReference{{Name: "owner"}}) }},
		{"Accepted", func(o *unstructured.Unstructured) { o.Object["status"] = map[string]interface{}{"phase": "Accepted"} }},
		{"invalid inputs", func(o *unstructured.Unstructured) { o.Object["spec"].(map[string]interface{})["inputsJSON"] = "{}" }},
		{"hidden future status", func(o *unstructured.Unstructured) { o.Object["status"] = map[string]interface{}{"unknown": true} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := pendingFixture(t)
			tc.change(o)
			require.ErrorContains(t, p.Validate(ctx, pendingAttrs(u, nil, o, admission.Create, ""), nil), pendingMessage)
		})
	}
	p.pendingRequests = ""
	require.Error(t, p.Validate(ctx, pendingAttrs(u, nil, pendingFixture(t), admission.Create, ""), nil))
	p = pendingPlugin()
	p.dynamicClient = nil
	require.Error(t, p.ValidateInitialization())
	require.Error(t, p.Validate(ctx, pendingAttrs(u, nil, pendingFixture(t), admission.Create, ""), nil))
}
func TestPendingLifetimeCustody(t *testing.T) {
	ctx, u := pendingContext(t, "update", "")
	p := pendingPlugin()
	old := pendingFixture(t)
	old.SetUID("app-uid")
	old.SetResourceVersion("10")
	next := old.DeepCopy()
	require.NoError(t, p.Validate(ctx, pendingAttrs(u, old, next, admission.Update, ""), nil))
	next.SetFinalizers(nil)
	require.Error(t, p.Validate(ctx, pendingAttrs(u, old, next, admission.Update, ""), nil))
	now := metav1.NewTime(time.Now())
	old.SetDeletionTimestamp(&now)
	old.SetFinalizers([]string{PendingFinalizer, "other.test/hold"})
	next = old.DeepCopy()
	next.SetFinalizers([]string{"other.test/hold"})
	require.NoError(t, p.Validate(ctx, pendingAttrs(u, old, next, admission.Update, ""), nil))
	next.SetFinalizers(nil)
	require.Error(t, p.Validate(ctx, pendingAttrs(u, old, next, admission.Update, ""), nil))
	next = old.DeepCopy()
	next.SetFinalizers([]string{"other.test/hold"})
	next.SetLabels(map[string]string{"mixed": "change"})
	require.Error(t, p.Validate(ctx, pendingAttrs(u, old, next, admission.Update, ""), nil))
	next = old.DeepCopy()
	next.SetUID("replacement")
	require.Error(t, p.Validate(ctx, pendingAttrs(u, old, next, admission.Update, ""), nil))
	ctx, u = pendingContext(t, "update", "status")
	next = old.DeepCopy()
	next.Object["status"] = pendingStatus("project-uid")
	require.NoError(t, p.Validate(ctx, pendingAttrs(u, old, next, admission.Update, "status"), nil))
	next.Object["status"].(map[string]interface{})["phase"] = "Accepted"
	require.Error(t, p.Validate(ctx, pendingAttrs(u, old, next, admission.Update, "status"), nil))
}
func TestPendingConfigAndFamily(t *testing.T) {
	_, err := readConfiguration(strings.NewReader("apiVersion: apiserver.miloapis.com/v1alpha1\nkind: AppsOwnershipConfiguration\npendingRequests: false"))
	require.Error(t, err)
	for _, enabled := range []bool{false, true} {
		p := pendingPlugin()
		if !enabled {
			p.pendingRequests = ""
		}
		for _, resource := range []string{"apps", "apprevisions"} {
			for _, version := range []string{"v1alpha1", "v2"} {
				for _, op := range []admission.Operation{admission.Create, admission.Update, admission.Delete} {
					for _, sub := range []string{"", "status", "finalize"} {
						a := admission.NewAttributesRecord(nil, nil, schema.GroupVersionKind{Group: GrantGroup, Version: version, Kind: "App"}, "", "forged", schema.GroupVersionResource{Group: GrantGroup, Version: version, Resource: resource}, sub, op, nil, false, &user.DefaultInfo{Name: "system-admin", UID: "uid", Groups: []string{"system:masters"}})
						require.ErrorContains(t, p.Validate(context.Background(), a, nil), pendingMessage)
					}
				}
			}
		}
	}
}

func TestPendingScopeAndMissingProof(t *testing.T) {
	ctx, u := pendingContext(t, "create", "")
	for _, tc := range []struct {
		name    string
		context func(context.Context) context.Context
		actor   user.Info
	}{
		{name: "missing provenance", context: func(context.Context) context.Context { return milorequest.WithProject(context.Background(), "project") }},
		{name: "wrong Project", context: func(c context.Context) context.Context { return milorequest.WithProject(c, "replacement") }},
		{name: "organization scope", context: func(c context.Context) context.Context { return milorequest.WithOrganization(c, "org") }},
		{name: "user scope", context: func(c context.Context) context.Context {
			return context.WithValue(c, datumfilters.UserIDContextKey, "u")
		}},
		{name: "changed effective identity", actor: &user.DefaultInfo{Name: u.GetName(), UID: "forged", Groups: u.GetGroups(), Extra: u.GetExtra()}},
		{name: "changed effective groups", actor: &user.DefaultInfo{Name: u.GetName(), UID: u.GetUID(), Groups: []string{"system:masters"}, Extra: u.GetExtra()}},
		{name: "missing route extras", actor: &user.DefaultInfo{Name: u.GetName(), UID: u.GetUID(), Groups: u.GetGroups()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, actor := ctx, u
			if tc.context != nil {
				c = tc.context(c)
			}
			if tc.actor != nil {
				actor = tc.actor
			}
			require.Error(t, pendingPlugin().Validate(c, pendingAttrs(actor, nil, pendingFixture(t), admission.Create, ""), nil))
		})
	}
}

func TestPendingRegistrationReserved(t *testing.T) {
	resource := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	for _, name := range []string{"apps", "apprevisions"} {
		for _, op := range []admission.Operation{admission.Create, admission.Update, admission.Delete} {
			for _, placement := range []string{"name", "old", "next"} {
				old, next := registrationObject("ordinary.example.test", "example.test", "ordinary"), registrationObject("ordinary.example.test", "example.test", "ordinary")
				requestName := "ordinary.example.test"
				switch placement {
				case "name":
					requestName = name + "." + GrantGroup
				case "old":
					old = registrationObject("", GrantGroup, name)
				case "next":
					next = registrationObject("", GrantGroup, name)
				}
				if (op == admission.Create && placement == "old") || (op == admission.Delete && placement == "next") {
					continue
				}
				var a, b runtime.Object
				if op != admission.Create {
					a = old
				}
				if op != admission.Delete {
					b = next
				}
				attrs := admission.NewAttributesRecord(b, a, schema.GroupVersionKind{}, "", requestName, resource, "", op, nil, false, nil)
				require.ErrorContains(t, NewPlugin().Validate(context.Background(), attrs, nil), pendingMessage)
			}
		}
	}
}

func TestPendingProjectReadFailureAndTermination(t *testing.T) {
	ctx, u := pendingContext(t, "create", "")
	p := pendingPlugin()
	p.dynamicClient.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "projects", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("private backend error sentinel")
	})
	err := p.Validate(ctx, pendingAttrs(u, nil, pendingFixture(t), admission.Create, ""), nil)
	require.ErrorContains(t, err, pendingMessage)
	require.NotContains(t, err.Error(), "private backend")
	p = pendingPlugin()
	project := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "resourcemanager.miloapis.com/v1alpha1", "kind": "Project", "metadata": map[string]interface{}{"name": "project", "uid": "project-uid", "deletionTimestamp": time.Now().UTC().Format(time.RFC3339)}}}
	p.dynamicClient.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "projects", func(ktesting.Action) (bool, runtime.Object, error) { return true, project, nil })
	require.Error(t, p.Validate(ctx, pendingAttrs(u, nil, pendingFixture(t), admission.Create, ""), nil))
	old := pendingFixture(t)
	old.SetUID("app-uid")
	old.SetResourceVersion("10")
	ctx, u = pendingContext(t, "delete", "")
	require.NoError(t, p.Validate(ctx, pendingAttrs(u, old, nil, admission.Delete, ""), nil))
	now := metav1.NewTime(time.Now())
	old.SetDeletionTimestamp(&now)
	next := old.DeepCopy()
	next.SetFinalizers(nil)
	ctx, u = pendingContext(t, "update", "")
	require.NoError(t, p.Validate(ctx, pendingAttrs(u, old, next, admission.Update, ""), nil))
}
func TestPendingConfigurationVersion(t *testing.T) {
	prefix := "apiVersion: apiserver.miloapis.com/v1alpha1\nkind: AppsOwnershipConfiguration\npendingRequests: "
	for _, s := range []string{"unknown", PendingRequestsV1 + "x", "pending-only-v1", "install-request-v1", "*"} {
		_, err := readConfiguration(strings.NewReader(prefix + s))
		require.Error(t, err)
	}
	config, err := readConfiguration(strings.NewReader(prefix + PendingRequestsV1))
	require.NoError(t, err)
	require.Equal(t, PendingRequestsV1, config.PendingRequests)
	config, err = readConfiguration(nil)
	require.NoError(t, err)
	require.Empty(t, config.PendingRequests)
}

func TestPendingMarkersBeforeConfiguredWriters(t *testing.T) {
	for _, key := range []string{ProjectUIDLabel, AppUIDLabel, NamespaceReservationKey} {
		for _, label := range []bool{false, true} {
			for _, status := range []bool{false, true} {
				p := pendingPlugin()
				rule := StatusWriter{Username: "customer", UID: "customer-uid", Group: GrantGroup, Version: "v1alpha1", Resource: "apps", Kind: "App"}
				p.statusWriters = []StatusWriter{rule}
				p.finalizerWriters = []FinalizerWriter{{StatusWriter: rule, Finalizer: PendingFinalizer}}
				old := pendingFixture(t)
				old.SetUID("app-uid")
				old.SetResourceVersion("10")
				if label {
					old.SetLabels(map[string]string{ProjectUIDLabel: "project-uid", AppUIDLabel: "app-uid", key: ""})
					if key != NamespaceReservationKey {
						old.SetLabels(map[string]string{ProjectUIDLabel: "project-uid", AppUIDLabel: "app-uid"})
					}
				} else {
					a := old.GetAnnotations()
					a[key] = ""
					old.SetAnnotations(a)
				}
				sub := ""
				if status {
					sub = "status"
				} else {
					now := metav1.NewTime(time.Now())
					old.SetDeletionTimestamp(&now)
				}
				next := old.DeepCopy()
				if status {
					next.Object["status"] = pendingStatus("project-uid")
				} else {
					next.SetFinalizers(nil)
				}
				ctx, u := pendingContext(t, "update", sub)
				require.ErrorContains(t, p.Validate(ctx, pendingAttrs(u, old, next, admission.Update, sub), nil), pendingMessage)
			}
		}
	}
}

func TestPendingDeletePreconditions(t *testing.T) {
	ctx, u := pendingContext(t, "delete", "")
	old := pendingFixture(t)
	old.SetUID("app-uid")
	old.SetResourceVersion("10")
	uid, rv := old.GetUID(), old.GetResourceVersion()
	wrongUID, wrongRV, empty := uid+"-replacement", "9", ""
	emptyUID := uid[:0]
	for _, tc := range []struct {
		name    string
		options runtime.Object
	}{
		{"missing options", nil},
		{"typed nil", (*metav1.DeleteOptions)(nil)},
		{"wrong options type", &metav1.UpdateOptions{}},
		{"missing preconditions", &metav1.DeleteOptions{}},
		{"missing UID", &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{ResourceVersion: &rv}}},
		{"missing RV", &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}},
		{"empty UID", &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &emptyUID, ResourceVersion: &rv}}},
		{"empty RV", &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &empty}}},
		{"wrong UID", &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &wrongUID, ResourceVersion: &rv}}},
		{"stale RV", &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &wrongRV}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, pendingPlugin().Validate(ctx, pendingAttrsOptions(u, old, nil, admission.Delete, "", tc.options), nil), pendingMessage)
		})
	}
	require.NoError(t, pendingPlugin().Validate(ctx, pendingAttrs(u, old, nil, admission.Delete, ""), nil))
	ctx, u = pendingContext(t, "deletecollection", "")
	require.ErrorContains(t, pendingPlugin().Validate(ctx, pendingAttrs(u, old, nil, admission.Delete, ""), nil), pendingMessage)
}
