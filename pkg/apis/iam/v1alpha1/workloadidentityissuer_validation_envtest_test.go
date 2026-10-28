package v1alpha1_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

// These tests run the generated CRD in a real API server, so its defaults, OpenAPI bounds and CEL rules are
// checked as a client meets them. They need envtest's binaries (KUBEBUILDER_ASSETS).

var (
	envOnce   sync.Once
	envErr    error
	k8sClient client.Client
)

// testClient starts one API server with milo's iam CRDs for every test in this file. Without
// KUBEBUILDER_ASSETS the tests are skipped, except in CI.
func testClient(t *testing.T) client.Client {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("KUBEBUILDER_ASSETS must be set in CI; this test needs an envtest API server")
		}
		t.Skip("KUBEBUILDER_ASSETS is not set; run setup-envtest to provide an envtest API server")
	}
	envOnce.Do(func() {
		env := &envtest.Environment{
			CRDDirectoryPaths:     []string{filepath.Join(repoRoot(t), "config", "crd", "bases", "iam")},
			ErrorIfCRDPathMissing: true,
		}
		cfg, err := env.Start()
		if err != nil {
			envErr = fmt.Errorf("starting the test API server: %w", err)
			return
		}
		scheme := runtime.NewScheme()
		if err := iamv1alpha1.AddToScheme(scheme); err != nil {
			envErr = err
			return
		}
		k8sClient, envErr = client.New(cfg, client.Options{Scheme: scheme})
	})
	if envErr != nil {
		t.Fatal(envErr)
	}
	return k8sClient
}

// A two-key JSON Web Key Set built from the public example keys of RFC 8037 and RFC 7517.
const twoKeySet = `{"keys":[` +
	`{"kty":"OKP","crv":"Ed25519","kid":"k1","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"},` +
	`{"kty":"EC","crv":"P-256","kid":"k2","x":"MKBCTNIcKUSDii11ySs3526iDZ8AiTo7Tu6KPAqv7D4",` +
	`"y":"4Etl6SRW2YiLUrN5vfvVHuhp7x8PxltmWWlbbM4IFyM"}]}`

func create(t *testing.T, obj client.Object) {
	t.Helper()
	if err := testClient(t).Create(context.Background(), obj); err != nil {
		t.Fatalf("create %s: %v", obj.GetName(), err)
	}
	c := testClient(t)
	t.Cleanup(func() { _ = c.Delete(context.Background(), obj) })
}

func get(t *testing.T, name string) *iamv1alpha1.WorkloadIdentityIssuer {
	t.Helper()
	got := &iamv1alpha1.WorkloadIdentityIssuer{}
	if err := testClient(t).Get(context.Background(), client.ObjectKey{Name: name}, got); err != nil {
		t.Fatalf("get %s: %v", name, err)
	}
	return got
}

func oidcIssuer(name, issuerURI string, jwks iamv1alpha1.JWKSSource) *iamv1alpha1.WorkloadIdentityIssuer {
	return &iamv1alpha1.WorkloadIdentityIssuer{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: iamv1alpha1.WorkloadIdentityIssuerSpec{
			OIDC: &iamv1alpha1.OIDCIssuer{IssuerURI: issuerURI, JWKS: jwks},
		},
	}
}

// expectInvalid asserts that err is a validation failure that mentions want, so a case cannot pass on a
// different rule than the one it targets.
func expectInvalid(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("accepted; want a refusal mentioning %q", want)
	}
	if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), want) {
		t.Fatalf("refused with %v; want an invalid error mentioning %q", err, want)
	}
}

func TestAcceptedIssuersAndDefaults(t *testing.T) {
	t.Run("an OIDC issuer defaults to discovery", func(t *testing.T) {
		obj := oidcIssuer("accept-discovery", "https://token.actions.githubusercontent.com", iamv1alpha1.JWKSSource{})
		create(t, obj)
		if got := get(t, obj.Name).Spec.OIDC.JWKS.Source; got != iamv1alpha1.JWKSSourceDiscovery {
			t.Fatalf("jwks.source = %q, want %q", got, iamv1alpha1.JWKSSourceDiscovery)
		}
	})
	accepted := []struct {
		name string
		obj  *iamv1alpha1.WorkloadIdentityIssuer
	}{
		{"an inline key set", oidcIssuer("accept-inline", "https://kubernetes.default.svc.cluster.local",
			iamv1alpha1.JWKSSource{Source: iamv1alpha1.JWKSSourceInline, Inline: twoKeySet})},
		{"an explicit key-set URL", oidcIssuer("accept-explicit", "https://idp.example.com",
			iamv1alpha1.JWKSSource{Source: iamv1alpha1.JWKSSourceExplicitURL, ExplicitURL: "https://keys.example.com/jwks.json"})},
		{"an issuer with a path", oidcIssuer("accept-path", "https://login.example.com/tenant/v2.0", iamv1alpha1.JWKSSource{})},
		{"an explicit port 443", oidcIssuer("accept-port", "https://idp.example.com:443", iamv1alpha1.JWKSSource{})},
		{"a host that starts with digits", oidcIssuer("accept-digits", "https://1password.example.com", iamv1alpha1.JWKSSource{})},
		{"an explicit key-set URL with a query", oidcIssuer("accept-explicit-query", "https://idp.example.com",
			iamv1alpha1.JWKSSource{Source: iamv1alpha1.JWKSSourceExplicitURL, ExplicitURL: "https://keys.example.com/jwks?tenant=a"})},
		{"an inline key whose kid is d", oidcIssuer("accept-kid-d", "https://idp.example.com",
			iamv1alpha1.JWKSSource{Source: iamv1alpha1.JWKSSourceInline, Inline: `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"d","x":"AA"}]}`})},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) { create(t, tc.obj) })
	}
	t.Run("a 24h maximum token lifetime", func(t *testing.T) {
		obj := oidcIssuer("accept-24h", "https://idp.example.com", iamv1alpha1.JWKSSource{})
		obj.Spec.MaxTokenLifetime = &metav1.Duration{Duration: 24 * time.Hour}
		create(t, obj)
	})
}

// issuer builds an object from raw fields, so a case can send what a typed client would omit.
func issuer(name string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "iam.miloapis.com/v1alpha1",
		"kind":       "WorkloadIdentityIssuer",
		"metadata":   map[string]any{"name": name},
		"spec":       spec,
	}}
}

func oidcSpec(issuerURI string, jwks map[string]any) map[string]any {
	return map[string]any{"oidc": map[string]any{"issuerUri": issuerURI, "jwks": jwks}}
}

func with(spec map[string]any, field string, value any) map[string]any {
	spec[field] = value
	return spec
}

func TestRefusedIssuers(t *testing.T) {
	const (
		noOIDC     = "spec.oidc: Required value"
		issuerRule = "issuerUri must start with https:// in lower case, use port 443 and a DNS hostname"
		sourceRule = "explicitUrl is set only for ExplicitURL, and inline only for Inline"
		lifetime   = "maxTokenLifetime must be more than zero and at most 24h"
		reserved   = "iss, aud, exp, nbf, iat and jti are checked by other fields"
		private    = "inline must hold public keys only"
	)
	discovery := map[string]any{}
	manyClaims := map[string]any{}
	for i := range 17 {
		manyClaims[fmt.Sprintf("claim%d", i)] = "value"
	}
	cases := []struct {
		name string
		spec map[string]any
		want string
	}{
		{"no oidc", map[string]any{}, noOIDC},
		{"plain http", oidcSpec("http://idp.example.com", discovery), issuerRule},
		{"an IPv4 literal", oidcSpec("https://10.0.0.1", discovery), issuerRule},
		{"an IPv6 literal", oidcSpec("https://[::1]", discovery), issuerRule},
		{"a port other than 443", oidcSpec("https://idp.example.com:8443", discovery), issuerRule},
		{"a query", oidcSpec("https://idp.example.com/?q=1", discovery), issuerRule},
		{"a fragment", oidcSpec("https://idp.example.com/#f", discovery), issuerRule},
		{"user information", oidcSpec("https://u@idp.example.com", discovery), issuerRule},
		{"an upper-case scheme", oidcSpec("HTTPS://idp.example.com", discovery), issuerRule},
		{"an upper-case scheme with an IPv6 literal", oidcSpec("HTTPS://[::1]", discovery), issuerRule},
		{"an upper-case scheme with an IPv4 literal", oidcSpec("HTTPS://10.0.0.1", discovery), issuerRule},
		{"an IPv4 address written as one integer", oidcSpec("https://167772161", discovery), issuerRule},
		{"an IPv4 literal with a port and path", oidcSpec("https://10.0.0.1:443/x", discovery), issuerRule},
		{"an explicit key-set URL on an IPv4 literal with a query", oidcSpec("https://idp.example.com",
			map[string]any{"source": "ExplicitURL", "explicitUrl": "https://10.0.0.1?x=1"}), "explicitUrl must start with https:// in lower case"},
		{"ExplicitURL without explicitUrl", oidcSpec("https://idp.example.com", map[string]any{"source": "ExplicitURL"}), sourceRule},
		{"Inline with explicitUrl", oidcSpec("https://idp.example.com",
			map[string]any{"source": "Inline", "inline": twoKeySet, "explicitUrl": "https://keys.example.com/jwks.json"}), sourceRule},
		{"Discovery with inline", oidcSpec("https://idp.example.com", map[string]any{"source": "Discovery", "inline": twoKeySet}), sourceRule},
		{"a 25h maximum token lifetime", with(oidcSpec("https://idp.example.com", discovery), "maxTokenLifetime", "25h"), lifetime},
		{"a zero maximum token lifetime", with(oidcSpec("https://idp.example.com", discovery), "maxTokenLifetime", "0s"), lifetime},
		{"a required iss claim", with(oidcSpec("https://idp.example.com", discovery),
			"requiredClaims", map[string]any{"iss": "https://idp.example.com"}), reserved},
		{"17 required claims", with(oidcSpec("https://idp.example.com", discovery), "requiredClaims", manyClaims), "Too many"},
		{"a 513-character claim value", with(oidcSpec("https://idp.example.com", discovery),
			"requiredClaims", map[string]any{"team": strings.Repeat("v", 513)}), "Too long"},
		{"a 129-character claim name", with(oidcSpec("https://idp.example.com", discovery),
			"requiredClaims", map[string]any{strings.Repeat("c", 129): "value"}), "claim names must be 1 to 128 characters"},
		// The private members are placeholders, not key material.
		{"an inline key with a private member", oidcSpec("https://idp.example.com",
			map[string]any{"source": "Inline", "inline": `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k1","x":"AA","d":"AA"}]}`}), private},
		{"an inline RSA key with a private prime", oidcSpec("https://idp.example.com",
			map[string]any{"source": "Inline", "inline": `{"keys":[{"kty":"RSA","kid":"k1","n":"AA","e":"AQAB","p":"AA"}]}`}), private},
		{"a private member written with space before its colon", oidcSpec("https://idp.example.com",
			map[string]any{"source": "Inline", "inline": `{"keys":[{"kty":"EC","crv":"P-256","kid":"k1","x":"AA","y":"AA","d" : "AA"}]}`}), private},
		{"an inline key set over 32 KiB", oidcSpec("https://idp.example.com",
			map[string]any{"source": "Inline", "inline": strings.Repeat("x", 32769)}), "Too long"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obj := issuer(fmt.Sprintf("refuse-%d", i), tc.spec)
			err := testClient(t).Create(context.Background(), obj)
			if err == nil {
				_ = testClient(t).Delete(context.Background(), obj)
			}
			expectInvalid(t, err, tc.want)
		})
	}
}

func TestIssuerIdentityIsImmutable(t *testing.T) {
	create(t, oidcIssuer("immutable-uri", "https://idp.example.com", iamv1alpha1.JWKSSource{}))
	obj := get(t, "immutable-uri")
	obj.Spec.OIDC.IssuerURI = "https://other.example.com"
	expectInvalid(t, testClient(t).Update(context.Background(), obj), "issuerUri cannot be changed")
}
