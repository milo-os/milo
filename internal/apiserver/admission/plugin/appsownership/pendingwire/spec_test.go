package pendingwire

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile("testdata/spec.json")
	require.NoError(t, err)
	var spec map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &spec))
	return spec
}
func TestPinnedSpecPrecision(t *testing.T) {
	want, err := os.ReadFile("testdata/spec.sha256")
	require.NoError(t, err)
	got, err := ValidateSpec(fixture(t), "project-uid")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(string(want)), got)
}
func TestPendingWireFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]interface{})
	}{
		{"foreign Project", func(s map[string]interface{}) {
			s["templateRef"].(map[string]interface{})["projectRevision"].(map[string]interface{})["projectUID"] = "other"
		}},
		{"unknown field", func(s map[string]interface{}) { s["inlineDefinition"] = "bad" }},
		{"null inputs", func(s map[string]interface{}) { s["inputsJSON"] = nil }},
		{"wrong digest", func(s map[string]interface{}) { s["inputsDigest"] = "sha256:" + strings.Repeat("0", 64) }},
		{"policy absent", func(s map[string]interface{}) { delete(s, "uninstallPolicy") }},
		{"case alias", func(s map[string]interface{}) { s["InputsJSON"] = s["inputsJSON"]; delete(s, "inputsJSON") }},
		{"published", func(s map[string]interface{}) {
			s["templateRef"] = map[string]interface{}{"publishedVersion": map[string]interface{}{"name": "public"}}
		}},
		{"UID whitespace", func(s map[string]interface{}) {
			s["templateRef"].(map[string]interface{})["projectRevision"].(map[string]interface{})["templateUID"] = "a b"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t)
			tc.mutate(s)
			_, err := ValidateSpec(s, "project-uid")
			require.Error(t, err)
		})
	}
	for _, raw := range []string{`{"x":1,"x":2}`, `{"x":"\ud800"}`, `{"x":1} {}`, `[]`, `{"x":1e1025}`, `{"x":1.0}`, "{\"x\":\"" + string([]byte{0xff}) + "\"}", `{"x":"` + strings.Repeat("x", MaxInputsBytes) + `"}`, strings.Repeat(`{"x":`, 66) + `0` + strings.Repeat(`}`, 66)} {
		s := fixture(t)
		s["inputsJSON"] = raw
		p, err := Parse([]byte(raw))
		if err == nil {
			s["inputsDigest"] = p.Digest()
		}
		_, err = ValidateSpec(s, "project-uid")
		require.Error(t, err)
	}
}
