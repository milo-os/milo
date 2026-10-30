package engine

import (
	"errors"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

const (
	ipClaimsType     = "ipam.miloapis.com/ipclaims"
	publicIPv4Type   = "ipam.miloapis.com/public-ipv4claims"
	publicIPv4Filter = "trigger.spec.ipFamily == 'IPv4' && trigger.spec.addressScope == 'Public'"
)

func ipClaim(family, scope string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "ipam.miloapis.com/v1alpha1",
		"kind":       "IPClaim",
		"metadata": map[string]interface{}{
			"name":      "claim-a",
			"namespace": "default",
		},
		"spec": map[string]interface{}{
			"ipFamily":     family,
			"addressScope": scope,
		},
	}}
}

func conditionPolicy(requests ...quotav1alpha1.ResourceRequestTemplate) *quotav1alpha1.ClaimCreationPolicy {
	return &quotav1alpha1.ClaimCreationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ipclaim-policy"},
		Spec: quotav1alpha1.ClaimCreationPolicySpec{
			Target: quotav1alpha1.ClaimTargetSpec{
				ResourceClaimTemplate: quotav1alpha1.ResourceClaimTemplate{
					Metadata: quotav1alpha1.ObjectMetaTemplate{Name: "{{trigger.metadata.name}}-claim"},
					Spec: quotav1alpha1.ResourceClaimTemplateSpec{
						Requests: requests,
					},
				},
			},
		},
	}
}

func resourceTypes(requests []quotav1alpha1.ResourceRequest) []string {
	types := make([]string, 0, len(requests))
	for _, r := range requests {
		types = append(types, r.ResourceType)
	}
	return types
}

func TestRenderClaimRequestConditions(t *testing.T) {
	celEngine, err := NewCELEngine()
	if err != nil {
		t.Fatalf("NewCELEngine() error = %v", err)
	}
	engine := NewTemplateEngine(celEngine, logr.Discard())

	tests := []struct {
		name      string
		requests  []quotav1alpha1.ResourceRequestTemplate
		trigger   *unstructured.Unstructured
		user      UserContext
		wantTypes []string
	}{
		{
			name: "empty condition always includes the request",
			requests: []quotav1alpha1.ResourceRequestTemplate{
				{ResourceType: ipClaimsType, Amount: 1},
			},
			trigger:   ipClaim("IPv6", "Private"),
			wantTypes: []string{ipClaimsType},
		},
		{
			name: "true condition keeps the request",
			requests: []quotav1alpha1.ResourceRequestTemplate{
				{ResourceType: publicIPv4Type, Amount: 1, Condition: publicIPv4Filter},
			},
			trigger:   ipClaim("IPv4", "Public"),
			wantTypes: []string{publicIPv4Type},
		},
		{
			name: "false condition drops the request",
			requests: []quotav1alpha1.ResourceRequestTemplate{
				{ResourceType: publicIPv4Type, Amount: 1, Condition: publicIPv4Filter},
			},
			trigger:   ipClaim("IPv4", "Private"),
			wantTypes: []string{},
		},
		{
			name: "mixed conditions keep only matching requests for a public IPv4 claim",
			requests: []quotav1alpha1.ResourceRequestTemplate{
				{ResourceType: ipClaimsType, Amount: 1},
				{ResourceType: publicIPv4Type, Amount: 1, Condition: publicIPv4Filter},
			},
			trigger:   ipClaim("IPv4", "Public"),
			wantTypes: []string{ipClaimsType, publicIPv4Type},
		},
		{
			name: "mixed conditions keep only matching requests for a private IPv6 claim",
			requests: []quotav1alpha1.ResourceRequestTemplate{
				{ResourceType: ipClaimsType, Amount: 1},
				{ResourceType: publicIPv4Type, Amount: 1, Condition: publicIPv4Filter},
			},
			trigger:   ipClaim("IPv6", "Private"),
			wantTypes: []string{ipClaimsType},
		},
		{
			name: "every request dropped renders a claim with no requests",
			requests: []quotav1alpha1.ResourceRequestTemplate{
				{ResourceType: ipClaimsType, Amount: 1, Condition: "false"},
				{ResourceType: publicIPv4Type, Amount: 1, Condition: publicIPv4Filter},
			},
			trigger:   ipClaim("IPv6", "Public"),
			wantTypes: []string{},
		},
		{
			name: "condition can read the user variable",
			requests: []quotav1alpha1.ResourceRequestTemplate{
				{ResourceType: ipClaimsType, Amount: 1, Condition: "user.groups.exists(g, g == 'metered')"},
			},
			trigger:   ipClaim("IPv4", "Public"),
			user:      UserContext{Name: "alice", Groups: []string{"metered"}},
			wantTypes: []string{ipClaimsType},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim, err := engine.RenderClaim(conditionPolicy(tt.requests...), &EvaluationContext{
				Object:    tt.trigger,
				User:      tt.user,
				Namespace: "default",
			})
			if err != nil {
				t.Fatalf("RenderClaim() error = %v", err)
			}

			got := resourceTypes(claim.Spec.Requests)
			if len(got) != len(tt.wantTypes) {
				t.Fatalf("RenderClaim() requests = %v, want %v", got, tt.wantTypes)
			}
			for i := range got {
				if got[i] != tt.wantTypes[i] {
					t.Fatalf("RenderClaim() requests = %v, want %v", got, tt.wantTypes)
				}
			}
			if claim.Name != "claim-a-claim" {
				t.Errorf("RenderClaim() name = %q, want %q", claim.Name, "claim-a-claim")
			}
		})
	}
}

func TestRenderClaimRequestConditionEvaluationError(t *testing.T) {
	celEngine, err := NewCELEngine()
	if err != nil {
		t.Fatalf("NewCELEngine() error = %v", err)
	}
	engine := NewTemplateEngine(celEngine, logr.Discard())

	tests := []struct {
		name      string
		condition string
	}{
		{name: "missing field", condition: "trigger.spec.missing == 'x'"},
		{name: "non-boolean result", condition: "trigger.spec.ipFamily"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := conditionPolicy(
				quotav1alpha1.ResourceRequestTemplate{ResourceType: ipClaimsType, Amount: 1},
				quotav1alpha1.ResourceRequestTemplate{ResourceType: publicIPv4Type, Amount: 1, Condition: tt.condition},
			)

			claim, err := engine.RenderClaim(policy, &EvaluationContext{Object: ipClaim("IPv4", "Public")})
			if err == nil {
				t.Fatalf("RenderClaim() = %v, want error", claim)
			}

			var condErr *RequestConditionError
			if !errors.As(err, &condErr) {
				t.Fatalf("RenderClaim() error = %v, want *RequestConditionError", err)
			}
			if condErr.Index != 1 || condErr.ResourceType != publicIPv4Type {
				t.Errorf("RequestConditionError = {Index: %d, ResourceType: %q}, want {1, %q}", condErr.Index, condErr.ResourceType, publicIPv4Type)
			}
		})
	}
}
