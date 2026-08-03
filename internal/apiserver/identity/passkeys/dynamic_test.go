package passkeys

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
)

// --- helpers: copied verbatim from sessions/dynamic_test.go (writePEM,
// makeSelfSignedServerAndCA) — provider-agnostic TLS test scaffolding. ---

func writePEM(dir, name string, b []byte) (string, error) {
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", err
	}
	return p, nil
}

// makeSelfSignedServerAndCA spins up a TLS server with a self-signed cert,
// returns the server and a CA PEM that trusts it. Also captures the SNI the
// client sent in the handshake via a channel.
func makeSelfSignedServerAndCA(t *testing.T, handler http.Handler) (*httptest.Server, []byte, <-chan string) {
	t.Helper()

	// Generate a CA
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "test-ca",
			Organization: []string{"test"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	// Server cert (issued by CA)
	srvKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	host := "127.0.0.1"
	ip := net.ParseIP(host)

	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "test-server",
		},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(24 * time.Hour),
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
		IPAddresses: []net.IP{ip},
		DNSNames:    []string{"localhost", "test.internal"},
	}
	caCert, _ := x509.ParseCertificate(caDER)
	srvDER, _ := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)

	srvCert := tls.Certificate{
		Certificate: [][]byte{srvDER, caDER}, // include chain
		PrivateKey:  srvKey,
	}

	// Capture SNI
	sniCh := make(chan string, 1)

	ts := httptest.NewUnstartedServer(handler)
	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{srvCert},
		// Observe SNI
		GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
			select {
			case sniCh <- chi.ServerName:
			default:
			}
			// return nil to use the default config above
			return nil, nil
		},
	}
	ts.StartTLS()

	return ts, caPEM, sniCh
}

// minimalUnstructuredList encodes an empty list for a resource.
func minimalUnstructuredList() *unstructured.UnstructuredList {
	ul := &unstructured.UnstructuredList{}
	ul.SetAPIVersion("example.com/v1")
	ul.SetKind("WidgetList")
	return ul
}

// --- tests ---

func TestNewDynamicProvider_TLSConfig(t *testing.T) {
	tmp := t.TempDir()
	caFile, _ := writePEM(tmp, "ca.crt", []byte("-----BEGIN CERTIFICATE-----\nMIIB...dummy\n-----END CERTIFICATE-----\n"))
	providerURL := "https://api.staging.env.datum.net:30443"

	dp, err := NewDynamicProvider(Config{
		ProviderURL: providerURL,
		CAFile:      caFile,
		Timeout:     15 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewDynamicProvider: %v", err)
	}
	if dp.base.Host != providerURL {
		t.Fatalf("Host = %q, want %q", dp.base.Host, providerURL)
	}
	if got, want := dp.base.TLSClientConfig.ServerName, "api.staging.env.datum.net"; got != want {
		t.Fatalf("ServerName = %q, want %q", got, want)
	}
	if dp.gvr.Resource != "passkeys" {
		t.Fatalf("gvr.Resource = %q, want %q", dp.gvr.Resource, "passkeys")
	}
}

func TestDynForUser_SendsAuthProxyHeadersAndTLS(t *testing.T) {
	gvr := identityv1alpha1.SchemeGroupVersion.WithResource("passkeys")

	var gotUser string
	mux := http.NewServeMux()
	path := "/apis/" + gvr.Group + "/" + gvr.Version + "/" + gvr.Resource
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		gotUser = r.Header.Get("X-Remote-User")
		ul := minimalUnstructuredList()
		w.Header().Set("Content-Type", "application/json")
		_ = unstructured.UnstructuredJSONScheme.Encode(ul, w)
	})

	ts, caPEM, sniCh := makeSelfSignedServerAndCA(t, mux)
	defer ts.Close()

	tmp := t.TempDir()
	caFile, _ := writePEM(tmp, "ca.crt", caPEM)

	dp, err := NewDynamicProvider(Config{ProviderURL: ts.URL, CAFile: caFile, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewDynamicProvider: %v", err)
	}
	dp.base.TLSClientConfig.ServerName = "test.internal"

	u := &authuser.DefaultInfo{Name: "jane@example.com", UID: "abcd-1234"}
	ctx := apirequest.WithUser(context.Background(), u)

	if _, err := dp.ListPasskeys(ctx, u, nil); err != nil {
		t.Fatalf("ListPasskeys error: %v", err)
	}
	if gotUser != "jane@example.com" {
		t.Fatalf("X-Remote-User = %q, want %q", gotUser, "jane@example.com")
	}
	select {
	case gotSNI := <-sniCh:
		if gotSNI != "test.internal" {
			t.Fatalf("SNI = %q, want test.internal", gotSNI)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not observe ClientHello/SNI")
	}
}

func TestListPasskeys_Retries(t *testing.T) {
	gvr := identityv1alpha1.SchemeGroupVersion.WithResource("passkeys")
	var hits int
	mux := http.NewServeMux()
	path := "/apis/" + gvr.Group + "/" + gvr.Version + "/" + gvr.Resource
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			http.Error(w, "try again", http.StatusInternalServerError)
			return
		}
		ul := minimalUnstructuredList()
		w.Header().Set("Content-Type", "application/json")
		_ = unstructured.UnstructuredJSONScheme.Encode(ul, w)
	})

	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})
	tmp := t.TempDir()
	caFile, _ := writePEM(tmp, "ca.crt", caPEM)

	dp, err := NewDynamicProvider(Config{ProviderURL: ts.URL, CAFile: caFile, Retries: 1})
	if err != nil {
		t.Fatalf("NewDynamicProvider: %v", err)
	}
	u := &authuser.DefaultInfo{Name: "x"}
	ctx := apirequest.WithUser(context.Background(), u)

	if _, err := dp.ListPasskeys(ctx, u, nil); err != nil {
		t.Fatalf("ListPasskeys error after retry: %v", err)
	}
	if hits != 2 {
		t.Fatalf("expected 2 hits (1 error + 1 success), got %d", hits)
	}
}
