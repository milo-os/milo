package activitypolicy_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"sigs.k8s.io/yaml"
)

// These tests guard the ActivityPolicy audit rules shipped from this repository.
// The activity processor applies no global filter, so each write rule must:
//
//   - match only requests that succeeded (2xx); a rejected request otherwise
//     shows up as a change that never happened
//   - skip dry runs (?dryRun=All), which succeed but persist nothing
//   - evaluate without error for every request shape the API server records,
//     because the processor stops at the first rule that errors and sends the
//     event to the dead-letter queue instead of the feed
const policiesGlob = "../../config/services/activity/policies/*/*-policy.yaml"

type auditRule struct {
	Name    string `json:"name"`
	Match   string `json:"match"`
	Summary string `json:"summary"`
}

type activityPolicy struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		AuditRules []auditRule `json:"auditRules"`
	} `json:"spec"`
}

func loadPolicies(t *testing.T) []activityPolicy {
	t.Helper()
	paths, err := filepath.Glob(policiesGlob)
	if err != nil {
		t.Fatalf("glob %q: %v", policiesGlob, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no policy files matched %q", policiesGlob)
	}
	policies := make([]activityPolicy, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var pol activityPolicy
		if err := yaml.Unmarshal(b, &pol); err != nil {
			t.Fatalf("unmarshal %s: %v", p, err)
		}
		policies = append(policies, pol)
	}
	return policies
}

var writeVerbRE = regexp.MustCompile(`audit\.verb\s*(==\s*'(create|update|patch|delete)'|in\s*\[[^\]]*'(create|update|patch|delete)')`)

// verbOf returns the first write verb a rule's match targets, or "".
func verbOf(match string) string {
	m := writeVerbRE.FindStringSubmatch(match)
	if m == nil {
		return ""
	}
	if m[2] != "" {
		return m[2]
	}
	return m[3]
}

// newEnv mirrors the activity processor's audit environment.
func newEnv(t *testing.T) *cel.Env {
	t.Helper()
	env, err := cel.NewEnv(cel.Variable("audit", cel.MapType(cel.StringType, cel.DynType)))
	if err != nil {
		t.Fatalf("cel env: %v", err)
	}
	return env
}

func evalMatch(t *testing.T, env *cel.Env, r auditRule, audit map[string]any) bool {
	t.Helper()
	ast, iss := env.Compile(r.Match)
	if iss != nil && iss.Err() != nil {
		t.Fatalf("compile %s: %v", r.Name, iss.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		t.Fatalf("program %s: %v", r.Name, err)
	}
	out, _, err := prg.Eval(map[string]any{"audit": withDefaults(audit)})
	if err != nil {
		t.Errorf("%s errored (the event would go to the DLQ): %v", r.Name, err)
		return false
	}
	b, ok := out.Value().(bool)
	if !ok {
		t.Fatalf("%s did not evaluate to bool, got %T", r.Name, out.Value())
	}
	return b
}

// withDefaults mirrors the processor's BuildAuditVars, which sets absent
// top-level objects to empty maps before evaluating a rule.
func withDefaults(audit map[string]any) map[string]any {
	out := make(map[string]any, len(audit))
	for k, v := range audit {
		out[k] = v
	}
	for _, field := range []string{"objectRef", "user", "responseStatus", "responseObject", "requestObject"} {
		if _, ok := out[field]; !ok {
			out[field] = map[string]any{}
		}
	}
	return out
}

func auditEvent(verb string, code int, uri string, request any) map[string]any {
	audit := map[string]any{
		"verb":           verb,
		"user":           map[string]any{"username": "alice@example.com"},
		"objectRef":      map[string]any{"name": "obj-1", "namespace": "default"},
		"requestURI":     uri,
		"responseStatus": map[string]any{"code": code},
	}
	if request != nil {
		audit["requestObject"] = request
	}
	if code >= 200 && code < 300 {
		audit["responseObject"] = map[string]any{
			"metadata": map[string]any{"name": "obj-1", "namespace": "default"},
			"spec":     map[string]any{},
			"status":   map[string]any{},
		}
	} else {
		audit["responseObject"] = map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "code": code}
	}
	return audit
}

const objectURI = "/apis/example.miloapis.com/v1alpha1/namespaces/default/objects/obj-1"

func successCode(verb string) int {
	if verb == "create" {
		return 201
	}
	return 200
}

// Structural guard: every write rule names the 2xx and dry-run conditions.
func TestWriteRulesGateOnOutcome(t *testing.T) {
	for _, pol := range loadPolicies(t) {
		for _, r := range pol.Spec.AuditRules {
			if verbOf(r.Match) == "" {
				continue
			}
			t.Run(pol.Metadata.Name+"/"+r.Name, func(t *testing.T) {
				if !strings.Contains(r.Match, "has(audit.responseStatus.code) && audit.responseStatus.code >= 200") || !strings.Contains(r.Match, "audit.responseStatus.code < 300") {
					t.Errorf("not gated on a 2xx response:\n  %s", r.Match)
				}
				if !strings.Contains(r.Match, "audit.requestURI.contains('dryRun=')") {
					t.Errorf("does not skip dry-run requests:\n  %s", r.Match)
				}
			})
		}
	}
}

// Semantic guard: no write rule matches a rejected or dry-run request.
func TestWriteRulesIgnoreFailedAndDryRunRequests(t *testing.T) {
	env := newEnv(t)
	for _, pol := range loadPolicies(t) {
		for _, r := range pol.Spec.AuditRules {
			verb := verbOf(r.Match)
			if verb == "" {
				continue
			}
			t.Run(pol.Metadata.Name+"/"+r.Name, func(t *testing.T) {
				request := map[string]any{"spec": map[string]any{}}
				for _, code := range []int{400, 403, 404, 409, 422, 500} {
					if evalMatch(t, env, r, auditEvent(verb, code, objectURI, request)) {
						t.Errorf("matched a rejected %s (code %d)", verb, code)
					}
				}
				if evalMatch(t, env, r, auditEvent(verb, successCode(verb), objectURI+"?dryRun=All", request)) {
					t.Errorf("matched a dry-run %s", verb)
				}
			})
		}
	}
}

// Shape guard: no rule errors on the request bodies the API server records for
// a successful write: JSON Patch arrays, DeleteOptions, Status delete responses,
// and Metadata-level events with no bodies at all.
func TestAuditRulesTolerateRequestShapes(t *testing.T) {
	env := newEnv(t)
	shapes := []struct {
		name, verb string
		request    any
		response   any
		noResponse bool
	}{
		{name: "JSON Patch", verb: "patch", request: []any{map[string]any{"op": "replace", "path": "/spec/x", "value": 1}}},
		{name: "JSON Patch on metadata", verb: "patch", request: []any{map[string]any{"op": "add", "path": "/metadata/labels/a", "value": "b"}}},
		{name: "metadata-level patch", verb: "patch", noResponse: true},
		{name: "metadata-level update", verb: "update", noResponse: true},
		{name: "delete with DeleteOptions", verb: "delete", request: map[string]any{"kind": "DeleteOptions", "apiVersion": "v1"}},
		{name: "delete returning Status", verb: "delete", response: map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Success", "details": map[string]any{"name": "obj-1"}}},
		{name: "metadata-level delete", verb: "delete", noResponse: true},
	}
	for _, pol := range loadPolicies(t) {
		for _, s := range shapes {
			t.Run(pol.Metadata.Name+"/"+s.name, func(t *testing.T) {
				audit := auditEvent(s.verb, successCode(s.verb), objectURI, s.request)
				switch {
				case s.noResponse:
					delete(audit, "responseObject")
				case s.response != nil:
					audit["responseObject"] = s.response
				}
				// The processor stops at the first match or error; mirror that.
				for _, r := range pol.Spec.AuditRules {
					if evalMatch(t, env, r, audit) {
						return
					}
				}
			})
		}
	}
}
