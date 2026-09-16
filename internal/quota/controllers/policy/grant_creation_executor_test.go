package policy

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"go.miloapis.com/milo/pkg/quota/engine"
)

// recordingLogSink is a logr.LogSink that records every Error call and every
// Info message so tests can assert on what the executor logged.
type recordingLogSink struct {
	mu       sync.Mutex
	errors   []string
	messages []string
}

func (s *recordingLogSink) Init(logr.RuntimeInfo) {}
func (s *recordingLogSink) Enabled(int) bool      { return true }
func (s *recordingLogSink) Info(_ int, msg string, _ ...interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
}
func (s *recordingLogSink) Error(_ error, msg string, _ ...interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors = append(s.errors, msg)
}
func (s *recordingLogSink) WithValues(...interface{}) logr.LogSink { return s }
func (s *recordingLogSink) WithName(string) logr.LogSink           { return s }

func (s *recordingLogSink) errorCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.errors)
}

func (s *recordingLogSink) hasMessage(substr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.messages {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

// testLocalManager satisfies manager.Manager for the single method the
// executor calls on the local manager: GetClient.
type testLocalManager struct {
	manager.Manager
	client client.Client
}

func (m *testLocalManager) GetClient() client.Client { return m.client }

// executorTestManager extends testManager so GetLocalManager returns a usable client.
type executorTestManager struct {
	*testManager
	local manager.Manager
}

func (m *executorTestManager) GetLocalManager() manager.Manager { return m.local }

// executorFixture bundles the collaborators a processTriggerResource test inspects.
type executorFixture struct {
	controller *GrantCreationController
	recorder   *record.FakeRecorder
	sink       *recordingLogSink
	getCalls   *int
}

// setupExecutor builds a GrantCreationController backed by a fake client whose
// Create behaviour is controlled by createErr (nil means Create succeeds).
func setupExecutor(t *testing.T, createErr error, objs ...client.Object) *executorFixture {
	t.Helper()
	scheme := testScheme()
	getCalls := 0

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				getCalls++
				return cl.Get(ctx, key, obj, opts...)
			},
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if createErr != nil {
					return createErr
				}
				return cl.Create(ctx, obj, opts...)
			},
		}).
		Build()

	celEngine, err := engine.NewCELEngine()
	if err != nil {
		t.Fatalf("Failed to create CEL engine: %v", err)
	}

	sink := &recordingLogSink{}
	recorder := record.NewFakeRecorder(10)
	mgr := &executorTestManager{
		testManager: &testManager{cluster: &testCluster{client: c}},
		local:       &testLocalManager{client: c},
	}

	controller := &GrantCreationController{
		Scheme:         scheme,
		Manager:        mgr,
		TemplateEngine: engine.NewTemplateEngine(celEngine, logr.Discard()),
		CELEngine:      celEngine,
		EventRecorder:  recorder,
		logger:         logr.New(sink),
	}

	return &executorFixture{controller: controller, recorder: recorder, sink: sink, getCalls: &getCalls}
}

// newTriggerNamespace builds an unstructured Namespace trigger resource.
func newTriggerNamespace(name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind("Namespace")
	obj.SetName(name)
	obj.SetUID("trigger-uid")
	return obj
}

// namespaceTerminatingError mirrors the Forbidden error milo's namespace
// lifecycle admission plugin returns for creates into a terminating namespace.
func namespaceTerminatingError() error {
	err := apierrors.NewForbidden(
		schema.GroupResource{Group: "quota.miloapis.com", Resource: "resourcegrants"},
		"test-grant",
		errors.New("unable to create new content in namespace milo-system because it is being terminated"),
	)
	err.ErrStatus.Details.Causes = append(err.ErrStatus.Details.Causes, metav1.StatusCause{
		Type:    corev1.NamespaceTerminatingCause,
		Message: "namespace milo-system is being terminated",
		Field:   "metadata.namespace",
	})
	return err
}

func drainEvents(recorder *record.FakeRecorder) []string {
	var events []string
	for {
		select {
		case e := <-recorder.Events:
			events = append(events, e)
		default:
			return events
		}
	}
}

func TestProcessTriggerResource_SkipsTriggerBeingDeleted(t *testing.T) {
	policy := newGrantPolicy("test-policy", 1)
	f := setupExecutor(t, nil, policy)

	trigger := newTriggerNamespace("doomed")
	now := metav1.Now()
	trigger.SetDeletionTimestamp(&now)

	f.controller.processTriggerResource(trigger, "test-policy", "UPDATE")

	if *f.getCalls != 0 {
		t.Errorf("Expected no client calls for a trigger being deleted, got %d Get calls", *f.getCalls)
	}
	if events := drainEvents(f.recorder); len(events) != 0 {
		t.Errorf("Expected no events, got %v", events)
	}
	if f.sink.errorCount() != 0 {
		t.Errorf("Expected no error-level logs, got %v", f.sink.errors)
	}
	if !f.sink.hasMessage("being deleted") {
		t.Errorf("Expected a skip message for the deleted trigger, got %v", f.sink.messages)
	}
}

func TestProcessTriggerResource_NamespaceTerminatingCreateIsNotRetried(t *testing.T) {
	policy := newGrantPolicy("test-policy", 1)
	f := setupExecutor(t, namespaceTerminatingError(), policy)

	f.controller.processTriggerResource(newTriggerNamespace("proj-1"), "test-policy", "UPDATE")

	if events := drainEvents(f.recorder); len(events) != 0 {
		t.Errorf("Expected no Warning event for a terminating target, got %v", events)
	}
	if f.sink.errorCount() != 0 {
		t.Errorf("Expected no error-level logs for a terminating target, got %v", f.sink.errors)
	}
	if !f.sink.hasMessage("not retrying") {
		t.Errorf("Expected a skip message for the terminating target, got %v", f.sink.messages)
	}
}

func TestProcessTriggerResource_NotFoundCreateIsNotRetried(t *testing.T) {
	policy := newGrantPolicy("test-policy", 1)
	notFound := apierrors.NewNotFound(
		schema.GroupResource{Group: "quota.miloapis.com", Resource: "resourcegrants"}, "test-grant")
	f := setupExecutor(t, notFound, policy)

	f.controller.processTriggerResource(newTriggerNamespace("proj-1"), "test-policy", "UPDATE")

	if events := drainEvents(f.recorder); len(events) != 0 {
		t.Errorf("Expected no Warning event for a missing target, got %v", events)
	}
	if f.sink.errorCount() != 0 {
		t.Errorf("Expected no error-level logs for a missing target, got %v", f.sink.errors)
	}
}

func TestProcessTriggerResource_UnrelatedErrorStillRecordsWarning(t *testing.T) {
	policy := newGrantPolicy("test-policy", 1)
	f := setupExecutor(t, apierrors.NewInternalError(errors.New("boom")), policy)

	f.controller.processTriggerResource(newTriggerNamespace("proj-1"), "test-policy", "UPDATE")

	events := drainEvents(f.recorder)
	if len(events) != 1 || !strings.Contains(events[0], "PolicyProcessingFailed") {
		t.Errorf("Expected one PolicyProcessingFailed Warning event, got %v", events)
	}
	if f.sink.errorCount() != 1 {
		t.Errorf("Expected one error-level log, got %v", f.sink.errors)
	}
}

func TestProcessTriggerResource_MissingGrantIsStillCreated(t *testing.T) {
	policy := newGrantPolicy("test-policy", 1)
	f := setupExecutor(t, nil, policy)

	f.controller.processTriggerResource(newTriggerNamespace("proj-1"), "test-policy", "ADD")

	events := drainEvents(f.recorder)
	if len(events) != 1 || !strings.Contains(events[0], "GrantCreated") {
		t.Errorf("Expected one GrantCreated event, got %v", events)
	}
	if f.sink.errorCount() != 0 {
		t.Errorf("Expected no error-level logs, got %v", f.sink.errors)
	}
}
