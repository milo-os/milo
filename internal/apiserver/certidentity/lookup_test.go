package certidentity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/client-go/rest"
)

func TestLoopbackLookupReadsTheProjectControlPlane(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/apis/resourcemanager.miloapis.com/v1alpha1/projects/datum-cloud/control-plane/apis/iam.miloapis.com/v1alpha1/serviceaccounts":
			w.Header().Set("Content-Type", "application/json")
			sa := serviceAccount(saUID)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiVersion": "iam.miloapis.com/v1alpha1",
				"kind":       "ServiceAccountList",
				"metadata":   map[string]any{},
				"items":      []any{sa.Object},
			})
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`))
		}
	}))
	defer srv.Close()

	lookup := NewLoopbackLookup(&rest.Config{Host: srv.URL})
	accounts, err := lookup(context.Background(), "datum-cloud")
	if err != nil {
		t.Fatal(err)
	}
	if Decide(accounts, saUID) != ReasonAllowed {
		t.Errorf("accounts = %v, want the opted-in service account", accounts)
	}

	accounts, err = lookup(context.Background(), "")
	if err != nil {
		t.Fatalf("root control plane without ServiceAccounts: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("root accounts = %v, want none", accounts)
	}
	if len(paths) != 2 || paths[1] != "/apis/iam.miloapis.com/v1alpha1/serviceaccounts" {
		t.Errorf("paths = %v", paths)
	}
}
