package accessowner

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/warning"

	milorequest "go.miloapis.com/milo/pkg/request"
	"go.miloapis.com/milo/pkg/server/access"
	"go.miloapis.com/milo/pkg/server/filters"
)

var (
	votes    = schema.GroupVersionResource{Group: "feedback.miloapis.com", Version: "v1alpha1", Resource: "votes"}
	releases = schema.GroupVersionResource{Group: "feedback.miloapis.com", Version: "v1alpha1", Resource: "releases"}
)

// newPlugin returns the plugin with rules synced from a CRD for votes that
// names spec.userRef.name as its owner field, and one for broken whose
// declaration is invalid.
func newPlugin(t *testing.T) *Plugin {
	t.Helper()
	crd := func(plural string, annotations map[string]string) *apiextensionsv1.CustomResourceDefinition {
		return &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: plural + ".feedback.miloapis.com", Annotations: annotations},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: votes.Group, Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: plural},
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
					Name: "v1alpha1", Served: true,
					SelectableFields: []apiextensionsv1.SelectableField{{JSONPath: ".spec.userRef.name"}},
				}},
			},
		}
	}
	client := apiextensionsfake.NewClientset(
		crd("votes", map[string]string{access.OwnerFieldAnnotation: "spec.userRef.name"}),
		crd("broken", map[string]string{access.OwnerFieldAnnotation: ""}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	factory := apiextensionsinformers.NewSharedInformerFactory(client, 0)
	rules := access.NewRules()
	if err := rules.Attach(factory); err != nil {
		t.Fatal(err)
	}
	factory.Start(ctx.Done())
	if err := wait.PollUntilContextTimeout(ctx, 10*time.Millisecond, 5*time.Second, true, func(context.Context) (bool, error) {
		return rules.HasSynced(), nil
	}); err != nil {
		t.Fatalf("rules never synced: %v", err)
	}

	p := NewPlugin()
	p.SetAccessRules(rules)
	if err := p.ValidateInitialization(); err != nil {
		t.Fatal(err)
	}
	return p
}

func vote(owner string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "feedback.miloapis.com/v1alpha1", "kind": "Vote",
		"metadata": map[string]any{"name": "p1." + owner},
	}}
	if owner != "" {
		u.Object["spec"] = map[string]any{"userRef": map[string]any{"name": owner}}
	}
	return u
}

func inUser(id string) context.Context {
	return context.WithValue(context.Background(), filters.UserIDContextKey, id) //nolint:staticcheck // the key Milo's filters use
}

func inOrg(callerUID string) context.Context {
	ctx := milorequest.WithOrganization(context.Background(), "o")
	return request.WithUser(ctx, &user.DefaultInfo{Name: "someone@example.com", UID: callerUID})
}

func validate(ctx context.Context, p *Plugin, op admission.Operation, gvr schema.GroupVersionResource, subresource string, obj, old *unstructured.Unstructured) error {
	return validateAs(ctx, &user.DefaultInfo{Name: "someone"}, p, op, gvr, subresource, obj, old)
}

func validateAs(ctx context.Context, u user.Info, p *Plugin, op admission.Operation, gvr schema.GroupVersionResource, subresource string, obj, old *unstructured.Unstructured) error {
	var oldObj runtime.Object
	if old != nil {
		oldObj = old
	}
	attrs := admission.NewAttributesRecord(obj, oldObj, schema.GroupVersionKind{Group: gvr.Group, Version: gvr.Version, Kind: "Vote"},
		"", obj.GetName(), gvr, subresource, op, nil, false, u)
	return p.Validate(ctx, attrs, nil)
}

func TestCreate(t *testing.T) {
	p := newPlugin(t)
	for _, tc := range []struct {
		name      string
		ctx       context.Context
		gvr       schema.GroupVersionResource
		owner     string
		forbidden bool
	}{
		{"own vote in user context", inUser("u1"), votes, "u1", false},
		{"another user's vote in user context", inUser("u1"), votes, "u2", true},
		{"no owner in user context", inUser("u1"), votes, "", true},
		{"own vote in org context", inOrg("u1"), votes, "u1", false},
		{"another user's vote in org context", inOrg("u1"), votes, "u2", true},
		{"caller without an ID in org context", inOrg(""), votes, "", true},
		{"platform context", context.Background(), votes, "u2", false},
		{"project context", milorequest.WithProject(inUser("u1"), "p"), votes, "u2", false},
		{"resource without a rule", inUser("u1"), releases, "u2", false},
	} {
		err := validate(tc.ctx, p, admission.Create, tc.gvr, "", vote(tc.owner), nil)
		if tc.forbidden != apierrors.IsForbidden(err) || (!tc.forbidden && err != nil) {
			t.Errorf("%s: err = %v, want forbidden %v", tc.name, err, tc.forbidden)
		}
	}
}

func TestUpdateCantChangeTheOwner(t *testing.T) {
	p := newPlugin(t)
	if err := validate(inUser("u1"), p, admission.Update, votes, "", vote("u2"), vote("u1")); !apierrors.IsForbidden(err) {
		t.Errorf("changing owner: err = %v, want forbidden", err)
	}
	if err := validate(inUser("u1"), p, admission.Update, votes, "", vote("u1"), vote("u1")); err != nil {
		t.Errorf("keeping owner: %v", err)
	}
	if err := validate(inUser("u1"), p, admission.Update, votes, "status", vote("u2"), vote("u1")); err != nil {
		t.Errorf("status update: %v", err)
	}
	if err := validate(context.Background(), p, admission.Update, votes, "", vote("u2"), vote("u1")); err != nil {
		t.Errorf("platform context: %v", err)
	}
}

func TestNotSyncedFailsClosed(t *testing.T) {
	p := NewPlugin()
	p.SetAccessRules(access.NewRules())
	if err := validate(inUser("u1"), p, admission.Create, votes, "", vote("u1"), nil); !apierrors.IsServiceUnavailable(err) {
		t.Errorf("err = %v, want ServiceUnavailable", err)
	}
	if err := validate(context.Background(), p, admission.Create, votes, "", vote("u1"), nil); !apierrors.IsServiceUnavailable(err) {
		t.Errorf("platform context: err = %v, want ServiceUnavailable", err)
	}
	if err := validateAs(context.Background(), masters, p, admission.Create, votes, "", vote("u1"), nil); err != nil {
		t.Errorf("system:masters: %v", err)
	}
	if err := validate(milorequest.WithProject(context.Background(), "p"), p, admission.Create, votes, "", vote("u1"), nil); err != nil {
		t.Errorf("project context: %v", err)
	}
}

var masters = &user.DefaultInfo{Name: "admin", Groups: []string{user.SystemPrivilegedGroup}}

func TestInvalidDeclarationRefusesWrites(t *testing.T) {
	p := newPlugin(t)
	broken := schema.GroupVersionResource{Group: votes.Group, Version: "v1alpha1", Resource: "broken"}
	for _, ctx := range []context.Context{context.Background(), inUser("u1"), inOrg("u1")} {
		err := validate(ctx, p, admission.Create, broken, "", vote("u1"), nil)
		if !apierrors.IsServiceUnavailable(err) || !strings.Contains(err.Error(), "broken.feedback.miloapis.com") {
			t.Errorf("err = %v, want ServiceUnavailable naming the CRD", err)
		}
	}
	if err := validateAs(inUser("u1"), masters, p, admission.Create, broken, "", vote("u1"), nil); err != nil {
		t.Errorf("system:masters: %v", err)
	}
}

func TestNeedsRules(t *testing.T) {
	if err := NewPlugin().ValidateInitialization(); err == nil {
		t.Error("plugin without rules initialised")
	}
}

// crdObject is a CRD in the internal form admission sees.
func crdObject(annotations map[string]string) *apiextensions.CustomResourceDefinition {
	return &apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "notes.example.com", Annotations: annotations},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensions.CustomResourceDefinitionNames{Plural: "notes"},
			Versions: []apiextensions.CustomResourceDefinitionVersion{{Name: "v1", Served: true, Storage: true,
				SelectableFields: []apiextensions.SelectableField{{JSONPath: ".spec.owner"}}}},
		},
	}
}

type warnings []string

func (w *warnings) AddWarning(_, text string) { *w = append(*w, text) }

func validateCRDWrite(ctx context.Context, op admission.Operation, obj, old *apiextensions.CustomResourceDefinition) error {
	var oldObj runtime.Object
	if old != nil {
		oldObj = old
	}
	gvr := apiextensionsv1.SchemeGroupVersion.WithResource("customresourcedefinitions")
	attrs := admission.NewAttributesRecord(obj, oldObj, apiextensionsv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"),
		"", obj.Name, gvr, "", op, nil, false, masters)
	p := NewPlugin()
	p.SetAccessRules(access.NewRules())
	return p.Validate(ctx, attrs, nil)
}

func TestCRDWithAnInvalidDeclarationIsRefused(t *testing.T) {
	for name, annotations := range map[string]map[string]string{
		"blank":          {access.OwnerFieldAnnotation: ""},
		"misspelt":       {"iam.miloapis.com/owner-feild": "spec.owner"},
		"not selectable": {access.OwnerFieldAnnotation: "spec.text"},
	} {
		err := validateCRDWrite(context.Background(), admission.Create, crdObject(annotations), nil)
		if !apierrors.IsInvalid(err) {
			t.Errorf("%s: err = %v, want Invalid", name, err)
		}
	}
	if err := validateCRDWrite(context.Background(), admission.Create, crdObject(map[string]string{access.OwnerFieldAnnotation: "spec.owner"}), nil); err != nil {
		t.Errorf("valid declaration: %v", err)
	}
	if err := validateCRDWrite(context.Background(), admission.Create, crdObject(nil), nil); err != nil {
		t.Errorf("no declaration: %v", err)
	}
}

func TestCRDUpdatesRatchet(t *testing.T) {
	valid := crdObject(map[string]string{access.OwnerFieldAnnotation: "spec.owner"})
	broken := crdObject(map[string]string{access.OwnerFieldAnnotation: "spec.owner", "iam.miloapis.com/x": "y"})

	if err := validateCRDWrite(context.Background(), admission.Update, broken, valid); !apierrors.IsInvalid(err) {
		t.Errorf("breaking a valid CRD: err = %v, want Invalid", err)
	}
	// A CRD stored before the check can still be updated without fixing it,
	// and can be fixed.
	relabelled := broken.DeepCopy()
	relabelled.Labels = map[string]string{"a": "b"}
	if err := validateCRDWrite(context.Background(), admission.Update, relabelled, broken); err != nil {
		t.Errorf("unrelated update of an invalid CRD: %v", err)
	}
	if err := validateCRDWrite(context.Background(), admission.Update, valid, broken); err != nil {
		t.Errorf("fixing an invalid CRD: %v", err)
	}
	worse := broken.DeepCopy()
	worse.Annotations[access.HiddenFieldAnnotation] = ""
	if err := validateCRDWrite(context.Background(), admission.Update, worse, broken); !apierrors.IsInvalid(err) {
		t.Errorf("adding a problem to an invalid CRD: err = %v, want Invalid", err)
	}
}

func TestCRDRuleChangeWarns(t *testing.T) {
	var w warnings
	ctx := warning.WithWarningRecorder(context.Background(), &w)
	if err := validateCRDWrite(ctx, admission.Update, crdObject(map[string]string{access.OwnerFieldAnnotation: "spec.owner"}), crdObject(nil)); err != nil {
		t.Fatal(err)
	}
	if len(w) != 1 || !strings.Contains(w[0], "notes.example.com") {
		t.Errorf("warnings = %q", w)
	}
	w = nil
	if err := validateCRDWrite(ctx, admission.Update, crdObject(nil), crdObject(nil)); err != nil || len(w) != 0 {
		t.Errorf("unchanged rule: err %v, warnings %q", err, w)
	}
}
