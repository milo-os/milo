package accessowner

import (
	"context"
	"testing"
	"time"

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

	milorequest "go.miloapis.com/milo/pkg/request"
	"go.miloapis.com/milo/pkg/server/access"
	"go.miloapis.com/milo/pkg/server/filters"
)

var (
	votes    = schema.GroupVersionResource{Group: "feedback.miloapis.com", Version: "v1alpha1", Resource: "votes"}
	releases = schema.GroupVersionResource{Group: "feedback.miloapis.com", Version: "v1alpha1", Resource: "releases"}
)

// newPlugin returns the plugin with rules synced from a CRD for votes that
// names spec.userRef.name as its owner field.
func newPlugin(t *testing.T) *Plugin {
	t.Helper()
	client := apiextensionsfake.NewClientset(&apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "votes.feedback.miloapis.com", Annotations: map[string]string{access.OwnerFieldAnnotation: "spec.userRef.name"}},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: votes.Group, Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: votes.Resource},
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	factory := apiextensionsinformers.NewSharedInformerFactory(client, 0)
	rules := access.NewRules()
	go func() { _ = rules.Run(ctx, factory) }()
	if err := wait.PollUntilContextTimeout(ctx, 10*time.Millisecond, 5*time.Second, true, func(context.Context) (bool, error) {
		factory.Start(ctx.Done())
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
	var oldObj runtime.Object
	if old != nil {
		oldObj = old
	}
	attrs := admission.NewAttributesRecord(obj, oldObj, schema.GroupVersionKind{Group: gvr.Group, Version: gvr.Version, Kind: "Vote"},
		"", obj.GetName(), gvr, subresource, op, nil, false, &user.DefaultInfo{Name: "someone"})
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
	if err := validate(context.Background(), p, admission.Create, votes, "", vote("u1"), nil); err != nil {
		t.Errorf("platform context: %v", err)
	}
}

func TestNeedsRules(t *testing.T) {
	if err := NewPlugin().ValidateInitialization(); err == nil {
		t.Error("plugin without rules initialised")
	}
}
