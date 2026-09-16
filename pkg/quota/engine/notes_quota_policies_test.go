package engine

import (
	"os"
	"testing"

	"github.com/go-logr/logr"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func loadPolicy(t *testing.T, path string) *quotav1alpha1.ClaimCreationPolicy {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	p := &quotav1alpha1.ClaimCreationPolicy{}
	if err := yaml.Unmarshal(b, p); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return p
}

func note(subjectGroup, creator string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "notes.miloapis.com/v1alpha1",
		"kind":       "Note",
		"metadata":   map[string]interface{}{"name": "note-abc", "namespace": "milo-system"},
		"spec": map[string]interface{}{
			"subjectRef": map[string]interface{}{"apiGroup": subjectGroup, "kind": "Contact", "name": "c1"},
			"creatorRef": map[string]interface{}{"name": creator},
		},
	}}
}

// Exactly one of the two policies must fire for any given subject group.
func TestPoliciesPartition(t *testing.T) {
	base := "../../../config/services/quota/claim-policies/"
	proj := loadPolicy(t, base+"note-claim-policy.yaml")
	user := loadPolicy(t, base+"user-note-claim-policy.yaml")
	e, err := NewCELEngine()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{
		"notification.miloapis.com", "iam.miloapis.com", "resourcemanager.miloapis.com",
		"networking.datumapis.com", "compute.datumapis.com",
	} {
		obj := note(group, "alice")
		gotProj, err := e.EvaluateConditions(proj.Spec.Trigger.Constraints, obj)
		if err != nil {
			t.Fatalf("%s project: %v", group, err)
		}
		gotUser, err := e.EvaluateConditions(user.Spec.Trigger.Constraints, obj)
		if err != nil {
			t.Fatalf("%s user: %v", group, err)
		}
		if gotProj == gotUser {
			t.Errorf("%s: policies overlap or leave a gap (project=%v user=%v)", group, gotProj, gotUser)
		}
	}
}

// The user policy must resolve its consumer to the note's creator.
func TestUserPolicyConsumerRendering(t *testing.T) {
	policy := loadPolicy(t, "../../../config/services/quota/claim-policies/user-note-claim-policy.yaml")
	ce, err := NewCELEngine()
	if err != nil {
		t.Fatal(err)
	}
	te := NewTemplateEngine(ce, logr.Discard())
	ctx := &EvaluationContext{
		Object:      note("notification.miloapis.com", "alice"),
		Namespace:   "milo-system",
		RequestInfo: &apirequest.RequestInfo{Verb: "create", Resource: "notes", Namespace: "milo-system"},
	}
	ctx.GVK.Group, ctx.GVK.Version, ctx.GVK.Kind = "notes.miloapis.com", "v1alpha1", "Note"
	claim, err := te.RenderClaim(policy, ctx)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if claim.Spec.ConsumerRef.Kind != "User" || claim.Spec.ConsumerRef.APIGroup != "iam.miloapis.com" {
		t.Errorf("consumer type = %+v", claim.Spec.ConsumerRef)
	}
	if claim.Spec.ConsumerRef.Name != "alice" {
		t.Errorf("consumer name = %q, want alice", claim.Spec.ConsumerRef.Name)
	}
	if len(claim.Spec.Requests) != 1 || claim.Spec.Requests[0].ResourceType != "notes.miloapis.com/user-notes" {
		t.Errorf("requests = %+v", claim.Spec.Requests)
	}
}
