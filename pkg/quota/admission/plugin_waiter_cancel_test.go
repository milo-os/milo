package admission

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"go.miloapis.com/milo/pkg/quota/engine"
)

// TestCancelledRequestEndsItsWait checks that a request whose context ends
// while it waits for its claim is not admitted, and that its wait ends with it:
// the claim watch it holds must not outlive the request.
func TestCancelledRequestEndsItsWait(t *testing.T) {
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
	waiting, ended := make(chan struct{}), make(chan struct{})
	plugin := &ResourceQuotaEnforcementPlugin{
		Handler:        admission.NewHandler(admission.Create),
		dynamicClient:  &fakeGrantingDynamicClient{FakeDynamicClient: fake.NewSimpleDynamicClient(scheme)},
		policyEngine:   &testPolicyEngine{policy: newDeterministicClaimPolicy(), gvk: gvk},
		templateEngine: engine.NewTemplateEngine(celEngine, logger.WithName("template")),
		config:         DefaultAdmissionPluginConfig(),
		logger:         logger.WithName("plugin"),
		awaitClaim: func(ctx context.Context, _ dynamic.ResourceInterface, ref claimRef) ClaimResult {
			defer close(ended)
			close(waiting)
			<-ctx.Done()
			return claimWait{ref: ref}.unfinished(ctx, nil)
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var cancelled time.Time
	go func() { <-waiting; cancelled = time.Now(); cancel() }()
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
	if late := time.Since(cancelled); late > time.Second {
		t.Fatalf("the request returned %v after its cancel; its wait must end with it", late)
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("the wait outlived its request")
	}
}
