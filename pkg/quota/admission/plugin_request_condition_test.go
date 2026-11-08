package admission

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"go.miloapis.com/milo/pkg/quota/engine"
)

func TestRequestConditionAdmission(t *testing.T) {
	const (
		ipClaims   = "ipam.miloapis.com/ipclaims"
		publicIPv4 = "ipam.miloapis.com/public-ipv4claims"
		publicOnly = "trigger.spec.ipFamily == 'IPv4' && trigger.spec.addressScope == 'Public'"
	)

	gvk := schema.GroupVersionKind{Group: "ipam.miloapis.com", Version: "v1alpha1", Kind: "IPClaim"}

	tests := []struct {
		name          string
		requests      []quotav1alpha1.ResourceRequestTemplate
		family, scope string
		wantClaim     []string
		wantDenied    string
	}{
		{
			name: "public IPv4 claim charges both resource types",
			requests: []quotav1alpha1.ResourceRequestTemplate{
				{ResourceType: ipClaims, Amount: 1},
				{ResourceType: publicIPv4, Amount: 1, Condition: publicOnly},
			},
			family:    "IPv4",
			scope:     "Public",
			wantClaim: []string{ipClaims, publicIPv4},
		},
		{
			name: "private claim charges only the unconditional resource type",
			requests: []quotav1alpha1.ResourceRequestTemplate{
				{ResourceType: ipClaims, Amount: 1},
				{ResourceType: publicIPv4, Amount: 1, Condition: publicOnly},
			},
			family:    "IPv6",
			scope:     "Private",
			wantClaim: []string{ipClaims},
		},
		{
			name: "no applicable request admits without a claim",
			requests: []quotav1alpha1.ResourceRequestTemplate{
				{ResourceType: publicIPv4, Amount: 1, Condition: publicOnly},
			},
			family: "IPv6",
			scope:  "Private",
		},
		{
			name: "condition evaluation error denies the resource",
			requests: []quotav1alpha1.ResourceRequestTemplate{
				{ResourceType: ipClaims, Amount: 1},
				{ResourceType: publicIPv4, Amount: 1, Condition: "trigger.spec.missing == 'x'"},
			},
			family:     "IPv4",
			scope:      "Public",
			wantDenied: "misconfigured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := &quotav1alpha1.ClaimCreationPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "ipclaim-policy"},
				Spec: quotav1alpha1.ClaimCreationPolicySpec{
					Trigger: quotav1alpha1.ClaimTriggerSpec{
						Resource: quotav1alpha1.ClaimTriggerResource{
							APIVersion: "ipam.miloapis.com/v1alpha1",
							Kind:       "IPClaim",
						},
					},
					Disabled: ptr.To(false),
					Target: quotav1alpha1.ClaimTargetSpec{
						ResourceClaimTemplate: quotav1alpha1.ResourceClaimTemplate{
							Spec: quotav1alpha1.ResourceClaimTemplateSpec{
								ConsumerRef: quotav1alpha1.ConsumerRef{
									APIGroup: "resourcemanager.miloapis.com",
									Kind:     "Project",
									Name:     "test-project",
								},
								Requests: tt.requests,
							},
						},
					},
				},
			}

			obj := &unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": "ipam.miloapis.com/v1alpha1",
				"kind":       "IPClaim",
				"metadata": map[string]interface{}{
					"name":      "claim-a",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"ipFamily":     tt.family,
					"addressScope": tt.scope,
				},
			}}

			scheme := runtime.NewScheme()
			if err := quotav1alpha1.AddToScheme(scheme); err != nil {
				t.Fatalf("AddToScheme() error = %v", err)
			}
			client := &fakeGrantingDynamicClient{FakeDynamicClient: fake.NewSimpleDynamicClient(scheme)}

			celEngine, err := engine.NewCELEngine()
			if err != nil {
				t.Fatalf("NewCELEngine() error = %v", err)
			}
			logger := zap.New(zap.UseDevMode(true))

			plugin := &ResourceQuotaEnforcementPlugin{
				Handler:        admission.NewHandler(admission.Create, admission.Update),
				dynamicClient:  client,
				policyEngine:   &testPolicyEngine{policy: policy, gvk: gvk},
				templateEngine: engine.NewTemplateEngine(celEngine, logger.WithName("template")),
				config:         DefaultAdmissionPluginConfig(),
				logger:         logger.WithName("plugin"),
			}
			plugin.watchManagers.Store("", &testWatchManager{behavior: "grant"})

			attrs := &testAdmissionAttributes{
				operation: admission.Create,
				object:    obj,
				gvk:       gvk,
				name:      obj.GetName(),
				namespace: obj.GetNamespace(),
				userInfo:  &user.DefaultInfo{Name: "test-user"},
			}

			err = plugin.Validate(context.Background(), attrs, nil)

			var created *unstructured.Unstructured
			for _, action := range client.Actions() {
				if create, ok := action.(k8stesting.CreateAction); ok && action.Matches("create", "resourceclaims") {
					created = create.GetObject().(*unstructured.Unstructured)
				}
			}

			if tt.wantDenied != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantDenied) {
					t.Fatalf("Validate() error = %v, want denial containing %q", err, tt.wantDenied)
				}
				if created != nil {
					t.Fatalf("Validate() created a ResourceClaim on a condition evaluation error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}

			if len(tt.wantClaim) == 0 {
				if created != nil {
					t.Fatalf("Validate() created ResourceClaim %s, want none", created.GetName())
				}
				return
			}
			if created == nil {
				t.Fatalf("Validate() created no ResourceClaim, want one charging %v", tt.wantClaim)
			}

			var claim quotav1alpha1.ResourceClaim
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(created.Object, &claim); err != nil {
				t.Fatalf("FromUnstructured() error = %v", err)
			}
			var got []string
			for _, r := range claim.Spec.Requests {
				got = append(got, r.ResourceType)
			}
			if strings.Join(got, ",") != strings.Join(tt.wantClaim, ",") {
				t.Errorf("ResourceClaim requests = %v, want %v", got, tt.wantClaim)
			}
		})
	}
}
