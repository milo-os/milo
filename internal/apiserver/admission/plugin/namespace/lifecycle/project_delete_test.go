package lifecycle

import (
	"context"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/clock"

	"go.miloapis.com/milo/internal/apiserver/projectstate"
	"go.miloapis.com/milo/pkg/request"
)

// recordedDeletions stands in for the shared view of Projects and keeps the
// names the plugin puts on its force-live list.
type recordedDeletions struct {
	names []string
}

func (r *recordedDeletions) ForceLiveLookup(name string) {
	r.names = append(r.names, name)
}

var projectGVK = schema.GroupVersionKind{Group: "resourcemanager.miloapis.com", Version: "v1alpha1", Kind: "Project"}

func newTestLifecycle(t *testing.T, projects projectDeletions) *Lifecycle {
	t.Helper()
	l, err := newLifecycleWithClock(sets.NewString(metav1.NamespaceDefault), clock.RealClock{})
	if err != nil {
		t.Fatalf("newLifecycleWithClock: %v", err)
	}
	l.projects = projects
	return l
}

func attributes(kind schema.GroupVersionKind, resource schema.GroupVersionResource, namespace, name string,
	op admission.Operation) admission.Attributes {
	return admission.NewAttributesRecord(nil, nil, kind, namespace, name, resource, "", op, nil, false,
		&user.DefaultInfo{Name: "tester"})
}

// TestProjectDeleteForcesALiveLookup covers a Project's DELETE: its name
// goes on the shared view's force-live list, as a Namespace's DELETE does in
// this plugin, so readers of the name ask storage until the reflector shows
// the deletion. Other requests record nothing.
func TestProjectDeleteForcesALiveLookup(t *testing.T) {
	deletions := &recordedDeletions{}
	l := newTestLifecycle(t, deletions)
	configMaps := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	configMap := schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}

	requests := []struct {
		ctx   context.Context
		attrs admission.Attributes
	}{
		{context.Background(), attributes(projectGVK, projectstate.ProjectGVR, "", "p", admission.Delete)},
		{context.Background(), attributes(projectGVK, projectstate.ProjectGVR, "", "q", admission.Update)},
		{request.WithProject(context.Background(), "p"), attributes(configMap, configMaps, "default", "cm", admission.Delete)},
	}
	for _, r := range requests {
		if err := l.Admit(r.ctx, r.attrs, nil); err != nil {
			t.Fatalf("Admit %s %s: %v", r.attrs.GetOperation(), r.attrs.GetResource().Resource, err)
		}
	}
	if want := []string{"p"}; !reflect.DeepEqual(deletions.names, want) {
		t.Fatalf("expected force-live names %v, got %v", want, deletions.names)
	}
}

// TestValidateInitializationRequiresProjectStates keeps a server from
// starting with the hook unwired, which would leave the reflector's old UID
// answering for a Project created again, with no sign.
func TestValidateInitializationRequiresProjectStates(t *testing.T) {
	l := newTestLifecycle(t, nil)
	client := fake.NewSimpleClientset()
	l.SetExternalKubeClientSet(client)
	l.SetExternalKubeInformerFactory(informers.NewSharedInformerFactory(client, 0))

	if err := l.ValidateInitialization(); err == nil {
		t.Fatal("expected ValidateInitialization to fail without project states")
	}
	l.projects = &recordedDeletions{}
	if err := l.ValidateInitialization(); err != nil {
		t.Fatalf("expected ValidateInitialization to pass with project states, got %v", err)
	}
}
