package resourcemanager

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// newTestEmitter returns an emitter whose client construction is intercepted,
// exposing the rest.Config the emitter built and the fake clientset it used.
func newTestEmitter(t *testing.T, base *rest.Config) (*ProjectEventEmitter, func() *rest.Config, *fake.Clientset) {
	t.Helper()

	cs := fake.NewSimpleClientset()
	var captured *rest.Config

	e := NewProjectEventEmitter(base, DefaultProjectEventUser, controllerOwnerName)
	e.newClient = func(cfg *rest.Config) (kubernetes.Interface, error) {
		captured = cfg
		return cs, nil
	}

	return e, func() *rest.Config { return captured }, cs
}

func TestProjectEventEmitter_ImpersonatesProjectParentContext(t *testing.T) {
	base := &rest.Config{Host: "https://milo.example"}
	e, capturedConfig, _ := newTestEmitter(t, base)

	project := &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "project-123", UID: "project-uid-1"},
	}

	if err := e.Emit(context.Background(), project, "Suspended", "Abuse"); err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}

	cfg := capturedConfig()
	if cfg == nil {
		t.Fatal("expected a client to be built")
	}

	if cfg.Impersonate.UserName != DefaultProjectEventUser {
		t.Errorf("expected Impersonate-User %q, got %q", DefaultProjectEventUser, cfg.Impersonate.UserName)
	}

	// The parent extras are what the events proxy reads to derive scope
	// annotations; without them the event is stored untagged.
	if got := cfg.Impersonate.Extra[iamv1alpha1.ParentKindExtraKey]; len(got) != 1 || got[0] != projectKind {
		t.Errorf("expected %s = [%q], got %v", iamv1alpha1.ParentKindExtraKey, projectKind, got)
	}
	if got := cfg.Impersonate.Extra[iamv1alpha1.ParentNameExtraKey]; len(got) != 1 || got[0] != "project-123" {
		t.Errorf("expected %s = [\"project-123\"], got %v", iamv1alpha1.ParentNameExtraKey, got)
	}
}

func TestProjectEventEmitter_DoesNotMutateBaseConfig(t *testing.T) {
	// Each emit targets a different project, so leaking impersonation into the
	// shared base config would scope every later event to the first project.
	base := &rest.Config{Host: "https://milo.example"}
	e, capturedConfig, _ := newTestEmitter(t, base)

	first := &resourcemanagerv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "project-aaa"}}
	if err := e.Emit(context.Background(), first, "Suspended", "Abuse"); err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}

	if base.Impersonate.UserName != "" || base.Impersonate.Extra != nil {
		t.Errorf("base config must not be mutated, got %+v", base.Impersonate)
	}

	second := &resourcemanagerv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "project-bbb"}}
	if err := e.Emit(context.Background(), second, "Reinstated", ""); err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}

	if got := capturedConfig().Impersonate.Extra[iamv1alpha1.ParentNameExtraKey]; len(got) != 1 || got[0] != "project-bbb" {
		t.Errorf("second emit must carry the second project's scope, got %v", got)
	}
}

func TestProjectEventEmitter_CreatesCoreEventForActivityPolicy(t *testing.T) {
	// Milo serves core/v1 Events with and without the EventsProxy gate, and
	// Activity converts them to the events.k8s.io/v1 shape the ActivityPolicy
	// eventRules read: involvedObject becomes regarding, message becomes
	// note, and source.component becomes reportingController.
	e, _, cs := newTestEmitter(t, &rest.Config{Host: "https://milo.example"})

	project := &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "project-123", UID: "project-uid-1"},
	}

	if err := e.Emit(context.Background(), project, "Suspended", "Abuse"); err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}

	var created *corev1.Event
	for _, action := range cs.Actions() {
		ca, ok := action.(k8stesting.CreateAction)
		if !ok {
			continue
		}
		if ev, ok := ca.GetObject().(*corev1.Event); ok {
			created = ev
			if ns := ca.GetNamespace(); ns != metav1.NamespaceDefault {
				t.Errorf("expected the event in namespace %q, got %q", metav1.NamespaceDefault, ns)
			}
			break
		}
	}
	if created == nil {
		t.Fatal("expected a core/v1 Event to be created")
	}

	if created.InvolvedObject.Name != "project-123" {
		t.Errorf("expected InvolvedObject.Name project-123, got %q", created.InvolvedObject.Name)
	}
	if created.InvolvedObject.Kind != "Project" {
		t.Errorf("expected InvolvedObject.Kind Project, got %q", created.InvolvedObject.Kind)
	}
	if want := resourcemanagerv1alpha1.GroupVersion.String(); created.InvolvedObject.APIVersion != want {
		t.Errorf("expected InvolvedObject.APIVersion %q, got %q", want, created.InvolvedObject.APIVersion)
	}
	if created.InvolvedObject.UID != "project-uid-1" {
		t.Errorf("expected InvolvedObject.UID project-uid-1, got %q", created.InvolvedObject.UID)
	}
	if created.Message != "Abuse" {
		t.Errorf("expected Message Abuse, got %q", created.Message)
	}
	if created.Reason != "Suspended" {
		t.Errorf("expected Reason Suspended, got %q", created.Reason)
	}
	if created.Type != corev1.EventTypeNormal {
		t.Errorf("expected Type Normal, got %q", created.Type)
	}
	if created.Source.Component != controllerOwnerName {
		t.Errorf("expected Source.Component %q, got %q", controllerOwnerName, created.Source.Component)
	}
	if created.ReportingController != controllerOwnerName {
		t.Errorf("expected ReportingController %q, got %q", controllerOwnerName, created.ReportingController)
	}
	if created.FirstTimestamp.IsZero() || created.LastTimestamp.IsZero() {
		t.Error("FirstTimestamp and LastTimestamp must be set; Activity derives eventTime from FirstTimestamp")
	}
	if created.Count != 1 {
		t.Errorf("expected Count 1, got %d", created.Count)
	}
}

func TestProjectEventEmitter_RejectsNilProject(t *testing.T) {
	e, _, _ := newTestEmitter(t, &rest.Config{Host: "https://milo.example"})

	if err := e.Emit(context.Background(), nil, "Suspended", "Abuse"); err == nil {
		t.Error("expected an error for a nil project")
	}
}

func TestProjectSuspensionPropagatorController_ProjectEventUser(t *testing.T) {
	if got := (&ProjectSuspensionPropagatorController{}).projectEventUser(); got != DefaultProjectEventUser {
		t.Errorf("unset ProjectEventUser: expected %q, got %q", DefaultProjectEventUser, got)
	}

	const custom = "system:example:custom-event-user"
	if got := (&ProjectSuspensionPropagatorController{ProjectEventUser: custom}).projectEventUser(); got != custom {
		t.Errorf("configured ProjectEventUser: expected %q, got %q", custom, got)
	}
}
