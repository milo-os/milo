package passkeyregistrationlinks

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
)

func newTestProvider(t *testing.T, handler http.HandlerFunc) *DynamicProvider {
	t.Helper()
	gvr := identityv1alpha1.SchemeGroupVersion.WithResource("passkeyregistrationlinks")
	mux := http.NewServeMux()
	mux.HandleFunc("/apis/"+gvr.Group+"/"+gvr.Version+"/"+gvr.Resource, handler)

	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	dp, err := NewDynamicProvider(Config{ProviderURL: ts.URL, CAFile: caFile})
	if err != nil {
		t.Fatalf("NewDynamicProvider: %v", err)
	}
	return dp
}

func testLink() *identityv1alpha1.PasskeyRegistrationLink {
	return &identityv1alpha1.PasskeyRegistrationLink{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "passkey-recovery-"},
		Spec: identityv1alpha1.PasskeyRegistrationLinkSpec{
			UserRef:     identityv1alpha1.PasskeyRegistrationLinkUserReference{Name: "user-2"},
			RequestedBy: "staff-1",
			Reason:      "ticket-123",
		},
	}
}

func TestCreatePasskeyRegistrationLink_ForwardsIdentityAndDecodes(t *testing.T) {
	var gotUser string
	var gotBody identityv1alpha1.PasskeyRegistrationLink
	dp := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotUser = r.Header.Get("X-Remote-User")
		var body identityv1alpha1.PasskeyRegistrationLink
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotBody = *body.DeepCopy()

		body.APIVersion = identityv1alpha1.SchemeGroupVersion.String()
		body.Kind = "PasskeyRegistrationLink"
		body.Name = "prl-abc"
		body.Status.EmailName = "email-1"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(&body)
	})

	u := &authuser.DefaultInfo{Name: "staff@example.com", UID: "staff-1"}
	ctx := apirequest.WithUser(context.Background(), u)

	got, err := dp.CreatePasskeyRegistrationLink(ctx, u, testLink(), nil)
	if err != nil {
		t.Fatalf("CreatePasskeyRegistrationLink: %v", err)
	}
	if gotUser != "staff@example.com" {
		t.Fatalf("X-Remote-User = %q, want %q", gotUser, "staff@example.com")
	}
	if gotBody.GenerateName != "passkey-recovery-" {
		t.Fatalf("forwarded metadata.generateName = %q, want %q", gotBody.GenerateName, "passkey-recovery-")
	}
	if gotBody.Spec.UserRef.Name != "user-2" {
		t.Fatalf("forwarded spec.userRef.name = %q, want %q", gotBody.Spec.UserRef.Name, "user-2")
	}
	if gotBody.Spec.RequestedBy != "staff-1" {
		t.Fatalf("forwarded spec.requestedBy = %q, want %q", gotBody.Spec.RequestedBy, "staff-1")
	}
	if gotBody.Spec.Reason != "ticket-123" {
		t.Fatalf("forwarded spec.reason = %q, want %q", gotBody.Spec.Reason, "ticket-123")
	}
	if got.Name != "prl-abc" || got.Status.EmailName != "email-1" {
		t.Fatalf("decoded link = %+v", got)
	}
}

func TestCreatePasskeyRegistrationLink_DoesNotRetry(t *testing.T) {
	var hits int
	dp := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		status := metav1.Status{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
			Status:   metav1.StatusFailure,
			Reason:   metav1.StatusReasonInternalError,
			Message:  "provider failed",
			Code:     http.StatusInternalServerError,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(&status)
	})

	u := &authuser.DefaultInfo{Name: "staff@example.com"}
	ctx := apirequest.WithUser(context.Background(), u)

	_, err := dp.CreatePasskeyRegistrationLink(ctx, u, testLink(), nil)
	if hits != 1 {
		t.Fatalf("provider hits = %d, want exactly 1 (create must not be retried)", hits)
	}
	var statusErr *apierrors.StatusError
	if !errors.As(err, &statusErr) || statusErr.Status().Code != http.StatusInternalServerError {
		t.Fatalf("error = %T %v, want the provider's 500 StatusError", err, err)
	}
}
