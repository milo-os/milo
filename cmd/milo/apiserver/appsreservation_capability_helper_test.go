package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	"go.miloapis.com/milo/internal/apiserver/storage/etcdshared"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiservercompat "k8s.io/apiserver/pkg/util/compatibility"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	basecompatibility "k8s.io/component-base/compatibility"
)

// Only this test binary accepts commands on inherited anonymous pipes. No
// production HTTP route, environment switch or credential grants this capability.
type reservationProbeRequest struct {
	Operation, Name, UID, RV, Reservation string
	LoseReply                             bool
}
type reservationProbeReply struct {
	Held  etcdshared.ProjectReservation
	Error string
}

func TestReservationCapabilityChild(t *testing.T) {
	if os.Getenv(serverChildEnv) != "1" {
		return
	}
	var args []string
	require.NoError(t, json.Unmarshal([]byte(os.Getenv(serverArgsEnv)), &args))
	_ = NewCommand() // initialize the same process-wide flag defaults
	s := NewOptions()
	s.GenericServerRunOptions.ComponentGlobalsRegistry = basecompatibility.NewComponentGlobalsRegistry()
	s.GenericServerRunOptions.ComponentGlobalsRegistry.ComponentGlobalsOrRegister(basecompatibility.DefaultKubeComponent, apiservercompat.DefaultBuildEffectiveVersion(), utilfeature.DefaultMutableFeatureGate)
	fs := pflag.NewFlagSet("reservation-fixture", pflag.ContinueOnError)
	s.GenericServerRunOptions.AddUniversalFlags(fs)
	s.Etcd.AddFlags(fs)
	s.SecureServing.AddFlags(fs)
	s.Authentication.AddFlags(fs)
	s.Authorization.AddFlags(fs)
	fs.StringVar(&s.Admission.GenericAdmission.ConfigFile, "admission-control-config-file", "", "fixture admission config")
	fs.StringVar(&s.ServiceAccountSigningKeyFile, "service-account-signing-key-file", "", "test signing key")
	require.NoError(t, fs.Parse(args))
	s.SystemNamespaces = []string{metav1.NamespaceSystem, metav1.NamespaceDefault, SystemNamespace}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts, err := s.Complete(ctx, []string{}, []net.IP{})
	require.NoError(t, err)
	require.Empty(t, opts.Validate())
	require.NoError(t, applyStorageFlags())
	config, err := NewConfig(opts)
	require.NoError(t, err)
	completed, err := config.Complete()
	require.NoError(t, err)
	server, err := CreateServerChain(completed)
	require.NoError(t, err)
	prepared, err := server.PrepareRun()
	require.NoError(t, err)
	capability := config.ProjectCommitFence.ProjectReservations()
	input := os.NewFile(3, "reservation-test-requests")
	output := os.NewFile(4, "reservation-test-replies")
	go func() {
		defer cancel()
		decoder, encoder := json.NewDecoder(input), json.NewEncoder(output)
		for {
			var request reservationProbeRequest
			if decoder.Decode(&request) != nil {
				return
			}
			var result etcdshared.ProjectReservation
			var err error
			switch request.Operation {
			case "acquire":
				result, err = capability.Acquire(ctx, request.Name, request.UID, request.RV, request.Reservation)
			case "read":
				result, err = capability.ReadHeld(ctx, request.Name, request.UID, request.Reservation)
			default:
				err = fmt.Errorf("unknown test command")
			}
			reply := reservationProbeReply{Held: result}
			if err != nil {
				reply.Error = err.Error()
			} else if request.LoseReply {
				reply = reservationProbeReply{Error: "test caller lost successful reply"}
			}
			if encoder.Encode(reply) != nil {
				return
			}
		}
	}()
	require.NoError(t, prepared.Run(ctx))
}

func startReservationCapabilityReplica(t *testing.T, ctx context.Context, endpoint string, ownership appsownership.Configuration) (string, *http.Client, map[string]string, func(reservationProbeRequest) reservationProbeReply) {
	t.Helper()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "tokens.csv")
	tokens := map[string]string{}
	writeTokenFile(t, tokenFile, tokens)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyPath := filepath.Join(dir, "sa.key")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600))
	config, err := json.Marshal(map[string]any{"apiVersion": "apiserver.config.k8s.io/v1", "kind": "AdmissionConfiguration", "plugins": []any{map[string]any{"name": appsownership.PluginName, "configuration": ownership}}})
	require.NoError(t, err)
	configPath := filepath.Join(dir, "admission.json")
	require.NoError(t, os.WriteFile(configPath, config, 0o600))
	port := freeTCPPort(t)
	flags, err := json.Marshal([]string{"--etcd-servers=" + endpoint, "--bind-address=127.0.0.1", fmt.Sprintf("--secure-port=%d", port), "--cert-dir=" + filepath.Join(dir, "certs"), "--token-auth-file=" + tokenFile, "--anonymous-auth=false", "--authorization-mode=AlwaysAllow", "--service-account-issuer=https://milo.apps-status.test", "--api-audiences=milo-apps-status-test", "--service-account-signing-key-file=" + keyPath, "--service-account-key-file=" + keyPath, "--storage-media-type=application/json", "--watch-cache=false", "--admission-control-config-file=" + configPath})
	require.NoError(t, err)
	serverCtx, stop := context.WithCancel(ctx)
	command := exec.CommandContext(serverCtx, os.Args[0], "-test.run=^TestReservationCapabilityChild$")
	command.Env = append(os.Environ(), serverChildEnv+"=1", serverArgsEnv+"="+string(flags))
	requestsRead, requestsWrite, err := os.Pipe()
	require.NoError(t, err)
	repliesRead, repliesWrite, err := os.Pipe()
	require.NoError(t, err)
	command.ExtraFiles = []*os.File{requestsRead, repliesWrite}
	t.Cleanup(func() { requestsWrite.Close(); repliesRead.Close() })
	var logs lockedBuffer
	command.Stdout = &logs
	command.Stderr = &logs
	require.NoError(t, command.Start())
	requestsRead.Close()
	repliesWrite.Close()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	exited := false
	t.Cleanup(func() {
		stop()
		if !exited {
			<-done
		}
	})
	root := fmt.Sprintf("https://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	t.Cleanup(client.CloseIdleConnections) // isolated self-signed fixture only
	deadline := time.Now().Add(60 * time.Second)
	for {
		select {
		case err := <-done:
			exited = true
			t.Fatalf("Milo exited: %v; %s", err, logs.String())
		default:
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/healthz", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+tokens["customer"])
		response, err := client.Do(req)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		require.Truef(t, time.Now().Before(deadline), "Milo startup timed out: %s", logs.String())
		time.Sleep(100 * time.Millisecond)
	}
	return root, client, tokens, func(request reservationProbeRequest) reservationProbeReply {
		require.NoError(t, json.NewEncoder(requestsWrite).Encode(request))
		var reply reservationProbeReply
		require.NoError(t, json.NewDecoder(repliesRead).Decode(&reply))
		return reply
	}
}
