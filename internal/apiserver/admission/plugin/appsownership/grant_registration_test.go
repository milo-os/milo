package appsownership

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	iam "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	milorequest "go.miloapis.com/milo/pkg/request"
	datumfilters "go.miloapis.com/milo/pkg/server/filters"
	ext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	request "k8s.io/apiserver/pkg/endpoints/request"
)

func registrationContext(t *testing.T, u user.Info, headers http.Header, verb, sub string) context.Context {
	t.Helper()
	var ctx context.Context
	h := datumfilters.WithAuthenticationProvenance(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() }))
	req := httptest.NewRequest("POST", "https://localhost/apis/apiextensions.k8s.io/v1/customresourcedefinitions", nil)
	req = req.WithContext(request.WithUser(req.Context(), u))
	req.Header = headers
	h.ServeHTTP(httptest.NewRecorder(), req)
	ri := &request.RequestInfo{IsResourceRequest: true, Verb: verb, APIGroup: "apiextensions.k8s.io", APIVersion: "v1", Resource: "customresourcedefinitions", Subresource: sub}
	if sub != "" {
		ri.Name = GrantCRDName
	}
	return request.WithRequestInfo(ctx, ri)
}
func registrationAttrs(u user.Info, old, next runtime.Object, op admission.Operation, sub string) admission.Attributes {
	return admission.NewAttributesRecord(next, old, extv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"), "", GrantCRDName, extv1.SchemeGroupVersion.WithResource("customresourcedefinitions"), sub, op, nil, false, u)
}
func initialRegistration() *extv1.CustomResourceDefinition {
	o := expectedGrantCRD.DeepCopy()
	o.Generation = 1
	o.Status.StoredVersions = []string{"v1alpha1"}
	return o
}
func TestGrantRegistrationContractConfiguration(t *testing.T) {
	raw := "apiVersion: apiserver.miloapis.com/v1alpha1\nkind: AppsOwnershipConfiguration\ngrantRegistration: "
	_, err := readConfiguration(strings.NewReader(raw + GrantRegistrationV1))
	require.NoError(t, err)
	for _, v := range []string{"v1", "true", GrantRegistrationV1 + "x", "*"} {
		_, err = readConfiguration(strings.NewReader(raw + v))
		require.Error(t, err)
	}
	c, err := readConfiguration(nil)
	require.NoError(t, err)
	require.Empty(t, c.GrantRegistration)
	require.Equal(t, "None", string(expectedGrantCRD.Spec.Conversion.Strategy))
}
func TestGrantRegistrationCreateBoundary(t *testing.T) {
	operator := &user.DefaultInfo{Name: "operator", UID: "operator-uid", Groups: []string{"authorized-root"}}
	for _, tc := range []struct {
		name     string
		change   func(*extv1.CustomResourceDefinition)
		scope    func(context.Context) context.Context
		headers  http.Header
		user     *user.DefaultInfo
		verb     string
		disabled bool
	}{
		{name: "exact native contract"},
		{name: "default disabled", disabled: true},
		{name: "SSA create", verb: "patch"},
		{name: "project", scope: func(c context.Context) context.Context { return milorequest.WithProject(c, "p") }},
		{name: "empty project context", scope: func(c context.Context) context.Context { return milorequest.WithProject(c, "") }},
		{name: "organization", scope: func(c context.Context) context.Context { return milorequest.WithOrganization(c, "o") }},
		{name: "user scope", scope: func(c context.Context) context.Context {
			return context.WithValue(c, datumfilters.UserIDContextKey, "u")
		}},
		{name: "missing provenance", scope: func(context.Context) context.Context { return context.Background() }},
		{name: "self impersonation", headers: http.Header{"ImPeRsOnAtE-uSeR": []string{"operator"}}},
		{name: "missing UID", user: &user.DefaultInfo{Name: "operator"}},
		{name: "parent extras", user: &user.DefaultInfo{Name: "operator", UID: "uid", Extra: map[string][]string{iam.ParentNameExtraKey: {}}}},
		{name: "extra annotations", change: func(o *extv1.CustomResourceDefinition) { o.Annotations["example.test/x"] = "value" }},
		{name: "label marker", change: func(o *extv1.CustomResourceDefinition) { o.Labels = map[string]string{AppUIDLabel: "app"} }},
		{name: "ownerref", change: func(o *extv1.CustomResourceDefinition) {
			o.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "owner", UID: "owner"}}
		}},
		{name: "finalizer", change: func(o *extv1.CustomResourceDefinition) { o.Finalizers = []string{"example.test/hold"} }},
		{name: "deletion", change: func(o *extv1.CustomResourceDefinition) { now := metav1.Now(); o.DeletionTimestamp = &now }},
		{name: "weaken CEL", change: func(o *extv1.CustomResourceDefinition) {
			o.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"] = extv1.JSONSchemaProps{Type: "object"}
		}},
		{name: "extra version", change: func(o *extv1.CustomResourceDefinition) {
			v := o.Spec.Versions[0]
			v.Name = "v2"
			v.Storage = false
			o.Spec.Versions = append(o.Spec.Versions, v)
		}},
		{name: "forged status", change: func(o *extv1.CustomResourceDefinition) { o.Status.AcceptedNames = o.Spec.Names }},
		{name: "RV", change: func(o *extv1.CustomResourceDefinition) { o.ResourceVersion = "1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := initialRegistration()
			if tc.change != nil {
				tc.change(o)
			}
			u := operator
			if tc.user != nil {
				u = tc.user
			}
			verb := tc.verb
			if verb == "" {
				verb = "create"
			}
			ctx := registrationContext(t, u, tc.headers, verb, "")
			if tc.scope != nil {
				ctx = tc.scope(ctx)
			}
			p := NewPlugin()
			if !tc.disabled {
				p.grantRegistration = GrantRegistrationV1
			}
			// Exercise native internal conversion too, as used by real CRD admission.
			internal := &ext.CustomResourceDefinition{}
			require.NoError(t, registrationScheme.Convert(o, internal, nil))
			err := p.Validate(ctx, registrationAttrs(u, nil, internal, admission.Create, ""), nil)
			if tc.name == "exact native contract" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, grantReservationMessage)
			}
		})
	}
}
func TestGrantRegistrationStatusBoundary(t *testing.T) {
	u := &user.DefaultInfo{Name: "ordinary-authorized-root-status", UID: "root-status-uid"}
	old := initialRegistration()
	old.UID = "crd-uid"
	old.ResourceVersion = "7"
	pending := old.DeepCopy()
	pending.Status.AcceptedNames.Plural = expectedGrantCRD.Spec.Names.Plural
	full := old.DeepCopy()
	full.Status.AcceptedNames = expectedGrantCRD.Spec.Names
	full.Status.Conditions = []extv1.CustomResourceDefinitionCondition{{Type: extv1.NamesAccepted, Status: extv1.ConditionTrue, LastTransitionTime: metav1.Now()}, {Type: extv1.Established, Status: extv1.ConditionTrue, LastTransitionTime: metav1.Now()}}
	p := NewPlugin()
	p.grantRegistration = GrantRegistrationV1
	ctx := registrationContext(t, u, nil, "update", "status")
	for _, next := range []*extv1.CustomResourceDefinition{pending, full} {
		require.NoError(t, p.Validate(ctx, registrationAttrs(u, old, next, admission.Update, "status"), nil))
	}
	for _, tc := range []struct {
		name   string
		change func(*extv1.CustomResourceDefinition)
	}{
		{"accepted alias", func(o *extv1.CustomResourceDefinition) { o.Status.AcceptedNames.Kind = "Different" }},
		{"stored alias", func(o *extv1.CustomResourceDefinition) { o.Status.StoredVersions = []string{"v2"} }},
		{"condition alias", func(o *extv1.CustomResourceDefinition) { o.Status.Conditions[0].Type = "Custom" }},
		{"duplicate condition", func(o *extv1.CustomResourceDefinition) {
			o.Status.Conditions = append(o.Status.Conditions, o.Status.Conditions[0])
		}},
		{"invalid state", func(o *extv1.CustomResourceDefinition) { o.Status.Conditions[0].Status = extv1.ConditionFalse }},
		{"oversize message", func(o *extv1.CustomResourceDefinition) { o.Status.Conditions[0].Message = strings.Repeat("x", 8193) }},
		{"labels", func(o *extv1.CustomResourceDefinition) { o.Labels = map[string]string{"x": "y"} }},
		{"metadata revision", func(o *extv1.CustomResourceDefinition) { o.Generation++ }},
		{"stale RV", func(o *extv1.CustomResourceDefinition) { o.ResourceVersion = "6" }},
		{"different UID", func(o *extv1.CustomResourceDefinition) { o.UID = "replacement" }},
		{"spec drift", func(o *extv1.CustomResourceDefinition) { o.Spec.Names.ShortNames = []string{"grant"} }},
		{"finalizer", func(o *extv1.CustomResourceDefinition) { o.Finalizers = []string{"example.test/delete"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := full.DeepCopy()
			tc.change(next)
			require.ErrorContains(t, p.Validate(ctx, registrationAttrs(u, old, next, admission.Update, "status"), nil), grantReservationMessage)
		})
	}
	require.ErrorContains(t, p.Validate(ctx, registrationAttrs(u, full, pending, admission.Update, "status"), nil), grantReservationMessage)
	corrupt := old.DeepCopy()
	corrupt.Status.StoredVersions = []string{"v2"}
	require.ErrorContains(t, p.Validate(ctx, registrationAttrs(u, corrupt, full, admission.Update, "status"), nil), grantReservationMessage, "cannot repair corrupt prior state")
	require.ErrorContains(t, p.Validate(ctx, registrationAttrs(u, full, full, admission.Update, ""), nil), grantReservationMessage, "main update never allowed")
}
