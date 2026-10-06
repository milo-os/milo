package v1alpha1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestServiceAccountKeyStatusClientIDJSON(t *testing.T) {
	key := ServiceAccountKey{
		Status: ServiceAccountKeyStatus{
			ClientID:          "349624629",
			AuthProviderKeyID: "625134962",
		},
	}

	encoded, err := json.Marshal(key)
	if err != nil {
		t.Fatalf("marshal ServiceAccountKey: %v", err)
	}

	jsonString := string(encoded)
	if !strings.Contains(jsonString, `"clientID":"349624629"`) {
		t.Fatalf("expected clientID in JSON, got %s", jsonString)
	}
	if strings.Contains(jsonString, `"privateKey"`) {
		t.Fatalf("empty privateKey must not be serialized, got %s", jsonString)
	}
}

func TestServiceAccountKeyStatusClientIDJSONRoundTrip(t *testing.T) {
	const payload = `{"status":{"clientID":"349624629","authProviderKeyID":"625134962"}}`

	var key ServiceAccountKey
	if err := json.Unmarshal([]byte(payload), &key); err != nil {
		t.Fatalf("unmarshal ServiceAccountKey: %v", err)
	}

	if key.Status.ClientID != "349624629" {
		t.Fatalf("expected clientID %q, got %q", "349624629", key.Status.ClientID)
	}
	if key.Status.PrivateKey != "" {
		t.Fatalf("expected privateKey to remain empty, got %q", key.Status.PrivateKey)
	}
}
