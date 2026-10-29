package admission

import (
	"context"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"go.miloapis.com/milo/pkg/quota/engine"
)

// waitingWatchManager registers waiters that get no result, and counts the
// waiters removed by claim name.
type waitingWatchManager struct {
	registered chan struct{}
	once       sync.Once
	mu         sync.Mutex
	byName     int
}

func (m *waitingWatchManager) RegisterClaimWaiter(ctx context.Context, claimName, namespace string, timeout time.Duration) (ClaimWaiter, error) {
	m.once.Do(func() { close(m.registered) })
	return make(chanWaiter, 1), nil
}

func (m *waitingWatchManager) UnregisterClaimWaiter(claimName, namespace string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byName++
}

func (m *waitingWatchManager) Start(ctx context.Context) error { return nil }
func (m *waitingWatchManager) Stop()                           {}

// TestCancelledRequestRemovesNoWaiterByName checks that a request whose
// context ends while it waits removes no waiter by claim name. Another request
// may hold a newer waiter for the same claim; the request's own cancel removes
// its own waiter.
func TestCancelledRequestRemovesNoWaiterByName(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := quotav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	celEngine, err := engine.NewCELEngine()
	if err != nil {
		t.Fatalf("create CEL engine: %v", err)
	}
	logger := zap.New(zap.UseDevMode(true))
	gvk := endpointSliceGVK()
	plugin := &ResourceQuotaEnforcementPlugin{
		Handler:        admission.NewHandler(admission.Create),
		dynamicClient:  &fakeGrantingDynamicClient{FakeDynamicClient: fake.NewSimpleDynamicClient(scheme)},
		policyEngine:   &testPolicyEngine{policy: newDeterministicClaimPolicy(), gvk: gvk},
		templateEngine: engine.NewTemplateEngine(celEngine, logger.WithName("template")),
		config:         DefaultAdmissionPluginConfig(),
		logger:         logger.WithName("plugin"),
	}
	waiters := &waitingWatchManager{registered: make(chan struct{})}
	plugin.watchManagers.Store("", waiters)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-waiters.registered; cancel() }()
	attrs := &testAdmissionAttributes{
		operation: admission.Create,
		object:    newStructuredEndpointSlice("test-eps", "default"),
		gvk:       gvk,
		name:      "test-eps",
		namespace: "default",
		userInfo:  &user.DefaultInfo{Name: "system:control@networking.datumapis.com"},
	}
	if err := plugin.Validate(ctx, attrs, nil); err == nil {
		t.Fatal("a request whose context ended was admitted")
	}

	waiters.mu.Lock()
	defer waiters.mu.Unlock()
	if waiters.byName != 0 {
		t.Fatalf("the cancelled request removed %d waiter(s) by claim name; a newer waiter for the same claim would go with it", waiters.byName)
	}
}
