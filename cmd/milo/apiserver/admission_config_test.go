package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"go.opentelemetry.io/otel/trace/noop"
	admissionv1 "k8s.io/api/admission/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/admission"
	webhookinit "k8s.io/apiserver/pkg/admission/plugin/webhook/initializer"
	validatingwebhook "k8s.io/apiserver/pkg/admission/plugin/webhook/validating"
	"k8s.io/apiserver/pkg/authentication/user"
	genericapiserver "k8s.io/apiserver/pkg/server"
	apiservercompat "k8s.io/apiserver/pkg/util/compatibility"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/apiserver/pkg/util/webhook"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

// TestAdmissionFlagsRegistered checks the apiserver command accepts
// --admission-control-config-file as a container arg, and registers no flag
// that changes the plugin set.
func TestAdmissionFlagsRegistered(t *testing.T) {
	fs := NewCommand().Flags()
	if fs.Lookup("admission-control-config-file") == nil {
		t.Error("flag --admission-control-config-file isn't registered")
	}
	for _, name := range []string{"enable-admission-plugins", "disable-admission-plugins", "admission-control"} {
		if fs.Lookup(name) != nil {
			t.Errorf("flag --%s is registered; it would let one arg change the enforced plugin set", name)
		}
	}

	if err := fs.Parse([]string{"--admission-control-config-file=/etc/milo/admission.yaml"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := fs.Lookup("admission-control-config-file").Value.String(); got != "/etc/milo/admission.yaml" {
		t.Errorf("--admission-control-config-file = %q, want /etc/milo/admission.yaml", got)
	}
}

// TestAdmissionFlagsUnsetKeepDefaults checks registering the flags without
// passing them leaves the plugin set and order NewOptions builds untouched.
func TestAdmissionFlagsUnsetKeepDefaults(t *testing.T) {
	want := NewOptions().Admission.GenericAdmission
	s := NewOptions()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	addAdmissionFlags(fs, s)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := s.Admission.GenericAdmission
	if got.ConfigFile != "" {
		t.Errorf("ConfigFile = %q, want empty", got.ConfigFile)
	}
	if !slices.Equal(got.EnablePlugins, want.EnablePlugins) {
		t.Errorf("EnablePlugins = %v, want %v", got.EnablePlugins, want.EnablePlugins)
	}
	if !slices.Equal(got.DisablePlugins, want.DisablePlugins) {
		t.Errorf("DisablePlugins = %v, want %v", got.DisablePlugins, want.DisablePlugins)
	}
	if !slices.Equal(got.RecommendedPluginOrder, want.RecommendedPluginOrder) {
		t.Errorf("RecommendedPluginOrder = %v, want %v", got.RecommendedPluginOrder, want.RecommendedPluginOrder)
	}
	if !got.DefaultOffPlugins.Equal(want.DefaultOffPlugins) {
		t.Errorf("DefaultOffPlugins = %v, want %v", sorted(got.DefaultOffPlugins.UnsortedList()), sorted(want.DefaultOffPlugins.UnsortedList()))
	}
}

func sorted(s []string) []string {
	slices.Sort(s)
	return s
}

const (
	webhookService   = "webhook"
	webhookNamespace = "example-system"
	webhookPort      = 443
	clientCommonName = "milo-apiserver"
)

// TestAdmissionConfigFileGivesWebhookClientCertificate checks an
// AdmissionConfiguration passed through --admission-control-config-file
// reaches ValidatingAdmissionWebhook: the plugin presents the client
// certificate its kubeconfig names for the webhook's service host:port, and
// the webhook, which refuses connections without a trusted client
// certificate, admits the request. Without the flag no certificate is
// presented and the request is refused, as before.
func TestAdmissionConfigFileGivesWebhookClientCertificate(t *testing.T) {
	dir := t.TempDir()
	pki := newTestPKI(t, dir)

	var mu sync.Mutex
	var callers []string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		review := &admissionv1.AdmissionReview{}
		if err := json.NewDecoder(r.Body).Decode(review); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		callers = append(callers, r.TLS.PeerCertificates[0].Subject.CommonName)
		mu.Unlock()
		review.Response = &admissionv1.AdmissionResponse{UID: review.Request.UID, Allowed: true}
		review.Request = nil
		_ = json.NewEncoder(w).Encode(review)
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{pki.serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pki.pool,
		MinVersion:   tls.VersionTLS12,
	}
	// The handshake failure without a client certificate is expected.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	serverURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	kubeconfig := writeFile(t, dir, "webhook-kubeconfig.yaml", fmt.Sprintf(`apiVersion: v1
kind: Config
users:
- name: %s.%s.svc:%d
  user:
    client-certificate: %s
    client-key: %s
`, webhookService, webhookNamespace, webhookPort, pki.clientCertFile, pki.clientKeyFile))
	admissionConfig := writeFile(t, dir, "admission.yaml", fmt.Sprintf(`apiVersion: apiserver.config.k8s.io/v1
kind: AdmissionConfiguration
plugins:
- name: ValidatingAdmissionWebhook
  configuration:
    apiVersion: apiserver.config.k8s.io/v1
    kind: WebhookAdmissionConfiguration
    kubeConfigFile: %s
`, kubeconfig))

	tests := []struct {
		name        string
		args        []string
		wantAllowed bool
		wantCallers []string
	}{
		{
			name:        "config file presents the client certificate",
			args:        []string{"--admission-control-config-file=" + admissionConfig},
			wantAllowed: true,
			wantCallers: []string{clientCommonName},
		},
		{
			name:        "no config file presents no certificate",
			wantAllowed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mu.Lock()
			callers = nil
			mu.Unlock()

			err := validateThroughWebhookChain(t, tt.args, serverURL, pki.caPEM)
			if tt.wantAllowed && err != nil {
				t.Fatalf("request refused: %v", err)
			}
			if !tt.wantAllowed && err == nil {
				t.Fatal("request admitted, want refused by a webhook that requires a client certificate")
			}
			if !tt.wantAllowed && !strings.Contains(err.Error(), "tls: certificate required") {
				t.Fatalf("request refused for another reason: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(callers, tt.wantCallers) {
				t.Errorf("webhook callers = %v, want %v", callers, tt.wantCallers)
			}
		})
	}
}

// validateThroughWebhookChain builds Milo's admission options from args
// through the apiserver command's flags, applies them with only
// ValidatingAdmissionWebhook enabled, and validates a ConfigMap create
// against a webhook served at serverURL.
func validateThroughWebhookChain(t *testing.T, args []string, serverURL *url.URL, caPEM []byte) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Parse through the real command so the test fails if NewCommand stops
	// wiring the flag into the options Run uses.
	s := NewOptions()
	if err := newCommand(s).Flags().Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Leave only the webhook plugin on: the others need Milo's initializers
	// and a real server, and aren't under test.
	s.Admission.GenericAdmission.EnablePlugins = []string{validatingwebhook.PluginName}
	s.Admission.GenericAdmission.DisablePlugins = slices.DeleteFunc(s.Admission.GenericAdmission.Plugins.Registered(), func(p string) bool {
		return p == validatingwebhook.PluginName
	})

	client := fake.NewClientset(webhookConfiguration(caPEM))
	factory := informers.NewSharedInformerFactory(client, 0)
	scheme := runtime.NewScheme()
	cfg := genericapiserver.NewConfig(serializer.NewCodecFactory(scheme))
	cfg.APIServerID = "test"

	// The same wrapper the control plane gives the webhook plugins.
	wrapper := webhook.NewDefaultAuthenticationInfoResolverWrapper(nil, nil, &rest.Config{}, noop.NewTracerProvider())
	if err := s.Admission.ApplyTo(cfg, factory, client, dynamicfake.NewSimpleDynamicClient(scheme),
		utilfeature.DefaultFeatureGate, apiservercompat.DefaultBuildEffectiveVersion(),
		webhookinit.NewPluginInitializer(wrapper, staticResolver{serverURL}),
	); err != nil {
		t.Fatalf("apply admission options: %v", err)
	}

	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())
	t.Cleanup(factory.Shutdown)

	cm := &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "default"},
	}
	attrs := admission.NewAttributesRecord(cm, nil, corev1.SchemeGroupVersion.WithKind("ConfigMap"),
		"default", "example", corev1.SchemeGroupVersion.WithResource("configmaps"), "",
		admission.Create, &metav1.CreateOptions{}, false, &user.DefaultInfo{Name: "example-user"})

	validator, ok := cfg.AdmissionControl.(admission.ValidationInterface)
	if !ok {
		t.Fatal("admission chain doesn't validate")
	}
	return validator.Validate(ctx, attrs, admission.NewObjectInterfacesFromScheme(clientgoscheme.Scheme))
}

func webhookConfiguration(caPEM []byte) *admissionregistrationv1.ValidatingWebhookConfiguration {
	fail := admissionregistrationv1.Fail
	none := admissionregistrationv1.SideEffectClassNone
	path := "/validate"
	port := int32(webhookPort)
	return &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "example"},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{{
			Name: "example.example.com",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				Service:  &admissionregistrationv1.ServiceReference{Namespace: webhookNamespace, Name: webhookService, Path: &path, Port: &port},
				CABundle: caPEM,
			},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
				Rule:       admissionregistrationv1.Rule{APIGroups: []string{""}, APIVersions: []string{"v1"}, Resources: []string{"configmaps"}},
			}},
			FailurePolicy:           &fail,
			SideEffects:             &none,
			AdmissionReviewVersions: []string{"v1"},
			NamespaceSelector:       &metav1.LabelSelector{},
			ObjectSelector:          &metav1.LabelSelector{},
		}},
	}
}

// staticResolver sends every webhook service to the test server, standing in
// for cluster DNS. TLS still verifies the service's DNS name.
type staticResolver struct{ u *url.URL }

func (r staticResolver) ResolveEndpoint(_, _ string, _ int32) (*url.URL, error) {
	u := *r.u
	return &u, nil
}

type testPKI struct {
	caPEM          []byte
	pool           *x509.CertPool
	serverCert     tls.Certificate
	clientCertFile string
	clientKeyFile  string
}

// newTestPKI makes one CA that signs both the webhook's serving certificate
// for the service's DNS name and Milo's client certificate.
func newTestPKI(t *testing.T, dir string) testPKI {
	t.Helper()
	caKey := newKey(t)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "example-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)

	issue := func(serial int64, cn string, dnsNames []string, usage x509.ExtKeyUsage) ([]byte, []byte) {
		key := newKey(t)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			DNSNames:     dnsNames,
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
			pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	}

	serverCertPEM, serverKeyPEM := issue(2, "webhook", []string{webhookService + "." + webhookNamespace + ".svc"}, x509.ExtKeyUsageServerAuth)
	serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	clientCertPEM, clientKeyPEM := issue(3, clientCommonName, nil, x509.ExtKeyUsageClientAuth)

	return testPKI{
		caPEM:          pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pool:           pool,
		serverCert:     serverCert,
		clientCertFile: writeFile(t, dir, "client.crt", string(clientCertPEM)),
		clientKeyFile:  writeFile(t, dir, "client.key", string(clientKeyPEM)),
	}
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
