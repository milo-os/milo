package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	x509request "k8s.io/apiserver/pkg/authentication/request/x509"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/server"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/api/legacyscheme"

	"go.miloapis.com/milo/internal/apiserver/certidentity"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	datumfilters "go.miloapis.com/milo/pkg/server/filters"
)

var certificateUIDOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57683, 2}

const (
	optedInUID    = "03aefc68-de9c-4ae6-90fd-51ec13a1854c"
	notOptedInUID = "5b0c3a52-6a43-4d5c-8a54-0b7f3c2f1e11"
	inactiveUID   = "9e2d7f40-1c55-4b8e-9a0d-3f6b2c7d8e22"
	deletedUID    = "c4a1e9b3-7d22-4f0a-b6c5-8e9f0a1b2c33"
	personUID     = "340583683847097652"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "client-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key}
}

func (ca *testCA) clientCert(t *testing.T, cn, uid string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	subject := pkix.Name{CommonName: cn}
	if uid != "" {
		subject.ExtraNames = []pkix.AttributeTypeAndValue{{Type: certificateUIDOID, Value: uid}}
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      subject,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

type binding struct{ uid, project string }

// projectBindings stands in for the OpenFGA authorizer: it checks the request
// UID, refuses an empty one, and allows a UID bound on the request's project.
type projectBindings map[binding]bool

func (b projectBindings) Authorize(_ context.Context, a authorizer.Attributes) (authorizer.Decision, string, error) {
	u := a.GetUser()
	if u.GetUID() == "" {
		return authorizer.DecisionNoOpinion, "no uid", nil
	}
	extra := u.GetExtra()
	if len(extra[iamv1alpha1.ParentKindExtraKey]) != 1 || extra[iamv1alpha1.ParentKindExtraKey][0] != "Project" {
		return authorizer.DecisionNoOpinion, "not a project request", nil
	}
	if b[binding{u.GetUID(), extra[iamv1alpha1.ParentNameExtraKey][0]}] {
		return authorizer.DecisionAllow, "", nil
	}
	return authorizer.DecisionNoOpinion, "no binding", nil
}

func serviceAccountWith(uid string, annotations map[string]string, state string) unstructured.Unstructured {
	sa := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "iam.miloapis.com/v1alpha1",
		"kind":       "ServiceAccount",
		"metadata":   map[string]any{"name": uid[:8]},
		"spec":       map[string]any{"state": state},
	}}
	sa.SetUID(types.UID(uid))
	sa.SetAnnotations(annotations)
	return sa
}

// TestCertificateUIDsThroughTheHandlerChain sends client certificates through
// Milo's handler chain to an authorizer that, like OpenFGA, grants by UID and
// project. A certificate UID is honoured only for an opted-in, active
// ServiceAccount in the project the request targets.
func TestCertificateUIDsThroughTheHandlerChain(t *testing.T) {
	ca := newTestCA(t)
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)

	optIn := map[string]string{certidentity.OptInAnnotation: certidentity.OptInValue}
	controlPlanes := map[string][]unstructured.Unstructured{
		"datum-cloud": {
			serviceAccountWith(optedInUID, optIn, "Active"),
			serviceAccountWith(notOptedInUID, nil, "Active"),
			serviceAccountWith(inactiveUID, optIn, "Inactive"),
		},
	}
	lookup := func(_ context.Context, project string) ([]unstructured.Unstructured, error) {
		return controlPlanes[project], nil
	}

	bindings := projectBindings{
		{optedInUID, "datum-cloud"}:    true,
		{optedInUID, "other-project"}:  true,
		{notOptedInUID, "datum-cloud"}: true,
		{inactiveUID, "datum-cloud"}:   true,
		{deletedUID, "datum-cloud"}:    true,
		{personUID, "datum-cloud"}:     true,
	}

	c := server.NewConfig(legacyscheme.Codecs)
	c.RequestInfoResolver = server.NewRequestInfoResolver(c)
	c.FeatureGate = utilfeature.DefaultFeatureGate
	c.Authentication.Authenticator = x509request.New(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}, x509request.CommonNameUserConversion)
	c.Authorization.Authorizer = authorizer.AuthorizerFunc(bindings.Authorize)

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	guard := certidentity.NewGuard(lookup, time.Minute)
	handler := datumfilters.ProjectRouterWithRequestInfo(DefaultBuildHandlerChain(ok, c, nil, guard), c.RequestInfoResolver)

	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	srv.StartTLS()
	defer srv.Close()

	get := func(t *testing.T, cert tls.Certificate, project string) int {
		t.Helper()
		transport := srv.Client().Transport.(*http.Transport).Clone()
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
		client := &http.Client{Transport: transport}
		url := srv.URL + "/apis/resourcemanager.miloapis.com/v1alpha1/projects/" + project + "/control-plane/api/v1/namespaces/default/configmaps"
		resp, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	const cn = "system:controller@example.miloapis.com"
	cases := []struct {
		name    string
		uid     string
		project string
		want    int
	}{
		{"opted-in service account in its project", optedInUID, "datum-cloud", http.StatusOK},
		{"opted-in service account in a project bound to its UID", optedInUID, "other-project", http.StatusForbidden},
		{"opted-in service account in an unbound project", optedInUID, "third-project", http.StatusForbidden},
		{"a person's UID", personUID, "datum-cloud", http.StatusForbidden},
		{"service account not opted in", notOptedInUID, "datum-cloud", http.StatusForbidden},
		{"inactive service account", inactiveUID, "datum-cloud", http.StatusForbidden},
		{"deleted service account", deletedUID, "datum-cloud", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := get(t, ca.clientCert(t, cn, tc.uid), tc.project); got != tc.want {
				t.Errorf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
