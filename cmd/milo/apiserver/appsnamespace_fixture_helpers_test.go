package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const namespaceRaceName = "ns-reserve-wins"

var namespaceFixtureNames = []string{"ns-held-annotation", "ns-held-label", "ns-held-empty", "ns-terminating-held", namespaceRaceName, "ns-delete-wins", "ns-reacquire"}

func namespaceSetupPermitted(a admission.Attributes) bool {
	if a.GetResource() != corev1.SchemeGroupVersion.WithResource("namespaces") || (a.GetNamespace() != "" && a.GetNamespace() != a.GetName()) || (a.GetSubresource() != "" && a.GetSubresource() != "finalize") {
		return false
	}
	for _, name := range namespaceFixtureNames {
		if a.GetName() == name {
			return true
		}
	}
	return false
}

// The pause is test-binary-only admission instrumentation, not a storage hook.
// It can delay one fixed customer's Namespace DELETE after successful validation.
// Host stdin resumes it; no HTTP request can open setup or release this pause.
type namespaceAdmissionPause struct {
	ackPath  string
	resumed  chan struct{}
	once     sync.Once
	attempts atomic.Int32
}

func newNamespaceAdmissionPause(path string) *namespaceAdmissionPause {
	return &namespaceAdmissionPause{ackPath: path, resumed: make(chan struct{})}
}
func (p *namespaceAdmissionPause) release() { p.once.Do(func() { close(p.resumed) }) }
func (p *namespaceAdmissionPause) validate(ctx context.Context, a admission.Attributes, validate func() error) error {
	selected := a.GetOperation() == admission.Delete && a.GetResource() == corev1.SchemeGroupVersion.WithResource("namespaces") && a.GetName() == namespaceRaceName && a.GetUserInfo() != nil && a.GetUserInfo().GetName() == "customer"
	var attempt int32
	if selected {
		attempt = p.attempts.Add(1)
		if err := os.WriteFile(p.ackPath+".delete-attempts", []byte(strconv.Itoa(int(attempt))), 0600); err != nil {
			return err
		}
	}
	if err := validate(); err != nil {
		return err
	}
	if selected && attempt == 1 {
		if err := os.WriteFile(p.ackPath+".delete-validated", []byte("validated"), 0600); err != nil {
			return err
		}
		select {
		case <-p.resumed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// This helper launches the same ordinary Milo process as #868. Reusing its
// normal server arguments lets two HTTP replicas share one ephemeral backend;
// no test client can read its records or call an internal registry/store.
func startAPIFixtureProcess(t *testing.T, ctx context.Context, serverArgs []string, token string) (*rest.Config, func(byte), string) {
	t.Helper()
	args, err := json.Marshal(serverArgs)
	require.NoError(t, err)
	var port string
	for _, arg := range serverArgs {
		if strings.HasPrefix(arg, "--secure-port=") {
			port = strings.TrimPrefix(arg, "--secure-port=")
		}
	}
	require.NotEmpty(t, port)
	serverCtx, stop := context.WithCancel(ctx)
	command := exec.CommandContext(serverCtx, os.Args[0], "-test.run=^TestAppsOwnershipAPIFixtureChild$")
	ackPath := filepath.Join(t.TempDir(), "sealed")
	command.Env = append(os.Environ(), apiFixtureChildEnv+"=1", apiFixtureArgsEnv+"="+string(args), apiFixtureSealEnv+"="+ackPath)
	var logs fixtureLog
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Milo child diagnostics: %s", logs.String())
		}
	})
	command.Stdout = &logs
	command.Stderr = &logs
	pipe, err := command.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, command.Start())
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() { stop(); _ = pipe.Close(); <-done })
	cfg := &rest.Config{Host: "https://127.0.0.1:" + port, BearerToken: token, TLSClientConfig: rest.TLSClientConfig{Insecure: true}, Timeout: 5 * time.Second}
	httpClient, err := rest.HTTPClientFor(cfg)
	require.NoError(t, err)
	t.Cleanup(httpClient.CloseIdleConnections)
	deadline := time.Now().Add(60 * time.Second)
	for {
		select {
		case err := <-done:
			done <- err
			t.Fatalf("Milo child exited: %v; %s", err, logs.String())
		default:
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Host+"/healthz", nil)
		response, err := httpClient.Do(req)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Milo did not start: %s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	return cfg, func(signal byte) { _, err := pipe.Write([]byte{signal}); require.NoError(t, err) }, ackPath
}

func startAPIFixtureReplica(t *testing.T, first *apiFixture) *apiFixture {
	t.Helper()
	args := append([]string(nil), first.serverArgs...)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	certDir := filepath.Join(t.TempDir(), "certs")
	for i, arg := range args {
		if strings.HasPrefix(arg, "--secure-port=") {
			args[i] = fmt.Sprintf("--secure-port=%d", port)
		}
		if strings.HasPrefix(arg, "--cert-dir=") {
			args[i] = "--cert-dir=" + certDir
		}
	}
	cfg, signal, ack := startAPIFixtureProcess(t, first.ctx, args, first.tokens[apiFixtureSetupName])
	copy := *first
	copy.serverArgs = args
	copy.signal = signal
	copy.ackPath = ack
	copy.root, err = client.New(cfg, client.Options{Scheme: first.scheme})
	require.NoError(t, err)
	copy.projectConfig = rest.CopyConfig(cfg)
	copy.projectConfig.Host = cfg.Host + "/apis/resourcemanager.miloapis.com/v1alpha1/projects/" + first.project.Name + "/control-plane"
	copy.seed, err = client.New(copy.projectConfig, client.Options{Scheme: first.scheme})
	require.NoError(t, err)
	customerConfig := rest.CopyConfig(copy.projectConfig)
	customerConfig.BearerToken = first.tokens["customer"]
	copy.customer, err = client.New(customerConfig, client.Options{Scheme: first.scheme})
	require.NoError(t, err)
	copy.seal = func() {
		signal('S')
		require.Eventually(t, func() bool { data, err := os.ReadFile(ack); return err == nil && string(data) == "sealed" }, 5*time.Second, 20*time.Millisecond)
	}
	return &copy
}
