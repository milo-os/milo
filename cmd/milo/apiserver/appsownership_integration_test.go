package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	serverChildEnv = "MILO_APPS_OWNERSHIP_HTTP_CHILD"
	serverArgsEnv  = "MILO_APPS_OWNERSHIP_HTTP_ARGS"
)

// TestAppsOwnershipHTTPChild runs Milo in a disposable child test process. The
// test process itself is killed on timeout so the production signal-context
// setup cannot leak an apiserver goroutine into the parent test process.
func TestAppsOwnershipHTTPChild(t *testing.T) {
	if os.Getenv(serverChildEnv) != "1" {
		return
	}
	var args []string
	require.NoError(t, json.Unmarshal([]byte(os.Getenv(serverArgsEnv)), &args))
	command := NewCommand()
	command.SetArgs(args)
	if err := command.Execute(); err != nil {
		t.Fatalf("Milo apiserver exited: %v", err)
	}
}

// TestAppsOwnershipHTTPIntegration exercises the registered bootstrap guard
// through Milo's actual public project API router and HTTP admission chain.
// This is a local integration probe, not an IAM or deployment acceptance test:
// authorization is deliberately AlwaysAllow so each bearer identity reaches
// the guard without needing OpenFGA or project role fixtures.
func TestAppsOwnershipHTTPIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("starts etcd and the full Milo API server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 105*time.Second)
	defer cancel()

	etcdPath, err := findEtcdBinary()
	require.NoError(t, err)
	etcd := &envtest.Etcd{Path: etcdPath, StartTimeout: 20 * time.Second, StopTimeout: 10 * time.Second}
	require.NoError(t, etcd.Start())
	t.Cleanup(func() { require.NoError(t, etcd.Stop()) })

	serverPort := freeTCPPort(t)
	tempDir := t.TempDir()
	tokenFile := filepath.Join(tempDir, "tokens.csv")
	tokens := map[string]string{}
	writeTokenFile(t, tokenFile, tokens)
	serviceAccountKey := filepath.Join(tempDir, "service-account.key")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(serviceAccountKey, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600))

	flags, err := json.Marshal([]string{
		"--etcd-servers=" + etcd.URL.String(),
		"--bind-address=127.0.0.1",
		fmt.Sprintf("--secure-port=%d", serverPort),
		"--cert-dir=" + filepath.Join(tempDir, "certs"),
		"--token-auth-file=" + tokenFile,
		"--anonymous-auth=false",
		"--authorization-mode=AlwaysAllow",
		"--service-account-issuer=https://milo.apps-ownership.test",
		"--api-audiences=milo-apps-ownership-test",
		"--service-account-signing-key-file=" + serviceAccountKey,
		"--service-account-key-file=" + serviceAccountKey,
		"--storage-media-type=application/json",
		"--watch-cache=false",
	})
	require.NoError(t, err)

	serverCtx, stopServer := context.WithCancel(ctx)
	defer stopServer()
	command := exec.CommandContext(serverCtx, os.Args[0], "-test.run=^TestAppsOwnershipHTTPChild$")
	command.Env = append(os.Environ(), serverChildEnv+"=1", serverArgsEnv+"="+string(flags))
	var serverLogs lockedBuffer
	command.Stdout = &serverLogs
	command.Stderr = &serverLogs
	require.NoError(t, command.Start())
	childExited := false
	childDone := make(chan error, 1)
	go func() { childDone <- command.Wait() }()
	t.Cleanup(func() {
		stopServer()
		if !childExited {
			select {
			case <-childDone:
			case <-time.After(3 * time.Second):
				_ = command.Process.Kill()
				<-childDone
			}
		}
	})

	project := fmt.Sprintf("apps-ownership-%d", time.Now().UnixNano())
	base := fmt.Sprintf("https://127.0.0.1:%d/apis/resourcemanager.miloapis.com/v1alpha1/projects/%s/control-plane", serverPort, project)
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, // local self-signed test cert only
	}
	t.Cleanup(client.CloseIdleConnections)
	readyURL := base + "/healthz"
	ready := false
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	for !ready {
		select {
		case err := <-childDone:
			childExited = true
			t.Fatalf("Milo API server exited before readiness: %v; logs: %s", err, serverLogs.String())
		case <-ctx.Done():
			t.Fatalf("probe context ended before Milo readiness: %v; logs: %s", ctx.Err(), serverLogs.String())
		case <-deadline.C:
			t.Fatalf("Milo API server did not become healthy; logs: %s", serverLogs.String())
		default:
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
		if err != nil {
			t.Fatalf("build health request: %v", err)
		}
		request.Header.Set("Authorization", "Bearer "+tokens["customer"])
		response, err := client.Do(request)
		if err != nil {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		ready = response.StatusCode == http.StatusOK
		if !ready {
			time.Sleep(250 * time.Millisecond)
		}
	}

	namespace := fmt.Sprintf("guard-%d", time.Now().UnixNano())
	status, body := requestJSON(t, ctx, client, tokens["customer"], http.MethodPost, base+"/api/v1/namespaces", map[string]any{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace},
	}, nil)
	require.Equalf(t, http.StatusCreated, status, "create project namespace: %s", body)
	identities := []string{"customer", "apps-controller", "release-worker", "system-admin"}
	for _, identity := range identities {
		t.Run(identity+" cannot create markers", func(t *testing.T) {
			name := "create-" + identity
			object := configMap(namespace, name)
			object["metadata"].(map[string]any)["labels"] = map[string]string{appsownership.ProjectUIDLabel: "probe-project"}
			status, body := requestJSON(t, ctx, client, tokens[identity], http.MethodPost, base+"/api/v1/namespaces/"+namespace+"/configmaps", object, nil)
			require.Equalf(t, http.StatusForbidden, status, "marker create body: %s", body)
			requireAppsOwnershipDenial(t, body)
		})

		t.Run(identity+" cannot patch marker onto an unmarked resource", func(t *testing.T) {
			name := "patch-" + identity
			status, body := requestJSON(t, ctx, client, tokens[identity], http.MethodPost, base+"/api/v1/namespaces/"+namespace+"/configmaps", configMap(namespace, name), nil)
			require.Equalf(t, http.StatusCreated, status, "unmarked create body: %s", body)
			patchURL := base + "/api/v1/namespaces/" + namespace + "/configmaps/" + name
			for _, metadata := range []map[string]any{
				{"labels": map[string]string{appsownership.ProjectUIDLabel: "probe-project"}},
				{"annotations": map[string]string{appsownership.AppUIDLabel: "probe-app"}},
			} {
				status, body = requestJSON(t, ctx, client, tokens[identity], http.MethodPatch, patchURL, map[string]any{"metadata": metadata}, map[string]string{"Content-Type": "application/merge-patch+json"})
				require.Equalf(t, http.StatusForbidden, status, "marker patch body: %s", body)
				requireAppsOwnershipDenial(t, body)
			}
		})
	}

	t.Run("impersonated Apps service account cannot create markers", func(t *testing.T) {
		name := "impersonated-apps-controller"
		object := configMap(namespace, name)
		object["metadata"].(map[string]any)["labels"] = map[string]string{appsownership.AppUIDLabel: "probe-app"}
		status, body := requestJSON(t, ctx, client, tokens["system-admin"], http.MethodPost, base+"/api/v1/namespaces/"+namespace+"/configmaps", object, map[string]string{
			"Impersonate-User":  "system:serviceaccount:apps-system:controller",
			"Impersonate-Uid":   "impersonated-apps-controller",
			"Impersonate-Group": "system:serviceaccounts",
		})
		require.Equalf(t, http.StatusForbidden, status, "impersonated marker create body: %s", body)
		requireAppsOwnershipDenial(t, body)
	})

	// Seed an old marked object by locating the actual ConfigMap value written by
	// Milo in this disposable etcd, then compare-and-swap its JSON after changing
	// only marker metadata. This avoids assuming the resource key layout.
	seededName := "preexisting-marked"
	status, body = requestJSON(t, ctx, client, tokens["customer"], http.MethodPost, base+"/api/v1/namespaces/"+namespace+"/configmaps", configMap(namespace, seededName), nil)
	require.Equalf(t, http.StatusCreated, status, "create seed ConfigMap: %s", body)
	seededKey, seededValue, seededRevision := findStoredConfigMap(t, ctx, etcd.URL.Host, project, namespace, seededName)
	markedValue, err := withOwnershipMarker(seededValue)
	require.NoError(t, err)
	seedConfigMapValue(t, ctx, etcd.URL.Host, seededKey, seededRevision, markedValue)
	status, body = requestJSON(t, ctx, client, tokens["customer"], http.MethodGet, base+"/api/v1/namespaces/"+namespace+"/configmaps/"+seededName, nil, nil)
	require.Equalf(t, http.StatusOK, status, "GET raw-seeded ConfigMap: %s", body)
	require.Contains(t, string(body), appsownership.ProjectUIDLabel)
	namespaceKey, namespaceValue, namespaceRevision := findStoredNamespace(t, ctx, etcd.URL.Host, namespace)
	markedNamespace, err := withOwnershipMarker(namespaceValue)
	require.NoError(t, err)
	seedConfigMapValue(t, ctx, etcd.URL.Host, namespaceKey, namespaceRevision, markedNamespace)
	status, body = requestJSON(t, ctx, client, tokens["customer"], http.MethodGet, base+"/api/v1/namespaces/"+namespace, nil, nil)
	require.Equalf(t, http.StatusOK, status, "GET raw-seeded Namespace: %s", body)
	require.Contains(t, string(body), appsownership.ProjectUIDLabel)
	for _, identity := range identities {
		t.Run(identity+" cannot update, strip, or delete an old marked object", func(t *testing.T) {
			objectURL := base + "/api/v1/namespaces/" + namespace + "/configmaps/" + seededName
			operations := []struct {
				name    string
				method  string
				body    any
				headers map[string]string
			}{
				{"put update", http.MethodPut, configMap(namespace, seededName), nil},
				{"patch update", http.MethodPatch, map[string]any{"data": map[string]string{"value": "changed"}}, map[string]string{"Content-Type": "application/merge-patch+json"}},
				{"strip label", http.MethodPatch, map[string]any{"metadata": map[string]any{"labels": map[string]any{appsownership.ProjectUIDLabel: nil}}}, map[string]string{"Content-Type": "application/merge-patch+json"}},
				{"strip annotation", http.MethodPatch, map[string]any{"metadata": map[string]any{"annotations": map[string]any{appsownership.ProjectUIDLabel: nil}}}, map[string]string{"Content-Type": "application/merge-patch+json"}},
				{"server-side apply update", http.MethodPatch, map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": seededName, "namespace": namespace, "labels": map[string]string{"ordinary": "value"}}}, map[string]string{"Content-Type": "application/apply-patch+yaml"}},
				{"delete", http.MethodDelete, nil, nil},
			}
			for _, operation := range operations {
				t.Run(operation.name, func(t *testing.T) {
					endpoint := objectURL
					if operation.name == "server-side apply update" {
						endpoint += "?fieldManager=apps-ownership-probe&force=true"
					}
					status, body := requestJSON(t, ctx, client, tokens[identity], operation.method, endpoint, operation.body, operation.headers)
					require.Equalf(t, http.StatusForbidden, status, "%s body: %s", operation.name, body)
					requireAppsOwnershipDenial(t, body)
				})
			}
			statusURL := base + "/api/v1/namespaces/" + namespace + "/status"
			status, body := requestJSON(t, ctx, client, tokens[identity], http.MethodPatch, statusURL, map[string]any{"status": map[string]any{"phase": "Active"}}, map[string]string{"Content-Type": "application/merge-patch+json"})
			require.Equalf(t, http.StatusForbidden, status, "marked Namespace status patch body: %s", body)
			requireAppsOwnershipDenial(t, body)
		})
	}

	// Verify failed writes did not persist any marked object and that the ordinary
	// resource remains usable; a rejected admission request must not partially
	// mutate storage.
	for _, identity := range identities {
		name := "patch-" + identity
		status, body := requestJSON(t, ctx, client, tokens[identity], http.MethodGet, base+"/api/v1/namespaces/"+namespace+"/configmaps/"+name, nil, nil)
		require.Equalf(t, http.StatusOK, status, "read unmarked resource body: %s", body)
		var got struct {
			Metadata struct {
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		require.NoError(t, json.Unmarshal(body, &got))
		require.NotContains(t, got.Metadata.Labels, appsownership.ProjectUIDLabel)
		require.NotContains(t, got.Metadata.Annotations, appsownership.AppUIDLabel)
		status, body = requestJSON(t, ctx, client, tokens[identity], http.MethodDelete, base+"/api/v1/namespaces/"+namespace+"/configmaps/"+name, nil, nil)
		require.Equalf(t, http.StatusOK, status, "delete unmarked ConfigMap body: %s", body)
	}

	require.NotContains(t, serverLogs.String(), "Bearer ")
}

func findEtcdBinary() (string, error) {
	if path := os.Getenv("TEST_ASSET_ETCD"); path != "" {
		return path, nil
	}
	if assets := os.Getenv("KUBEBUILDER_ASSETS"); assets != "" {
		return filepath.Join(assets, "etcd"), nil
	}
	return exec.LookPath("etcd")
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func writeTokenFile(t *testing.T, path string, tokenByIdentity map[string]string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	writer := csv.NewWriter(file)
	rows := [][]string{
		{"customer", "apps-probe-customer", "customer-uid", "system:authenticated"},
		{"apps-controller", "system:serviceaccount:apps-system:controller", "apps-controller-uid", "system:serviceaccounts,system:serviceaccounts:apps-system,system:authenticated"},
		{"release-worker", "system:serviceaccount:apps-system:release", "release-worker-uid", "system:serviceaccounts,system:serviceaccounts:apps-system,system:authenticated"},
		{"system-admin", "apps-probe-admin", "system-admin-uid", "system:masters,system:authenticated"},
	}
	for _, row := range rows {
		var raw [32]byte
		_, err := rand.Read(raw[:])
		require.NoError(t, err)
		token := hex.EncodeToString(raw[:])
		tokenByIdentity[row[0]] = token
		require.NoError(t, writer.Write([]string{token, row[1], row[2], row[3]}))
	}
	writer.Flush()
	require.NoError(t, writer.Error())
	require.NoError(t, file.Close())
}

func configMap(namespace, name string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"namespace": namespace, "name": name},
		"data":       map[string]string{"value": "ordinary"},
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func requestJSON(t *testing.T, ctx context.Context, client *http.Client, token, method, endpoint string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, requestBody)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	require.NoError(t, err)
	return response.StatusCode, responseBody
}

func requireAppsOwnershipDenial(t *testing.T, body []byte) {
	t.Helper()
	var status struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(body, &status), "decode admission status body")
	require.Contains(t, status.Message, "Apps ownership is reserved; managed resource writes remain disabled")
}

// findStoredConfigMap searches only the disposable project's actual etcd
// records, matches decoded object identity, and returns key/value/revision.
// It intentionally never logs object contents.
func findStoredConfigMap(t *testing.T, ctx context.Context, endpoint, project, namespace, name string) (string, []byte, int64) {
	t.Helper()
	client := newEtcdClient(t, endpoint)
	response, err := client.Get(ctx, "/projects/"+project+"/", clientv3.WithPrefix())
	require.NoError(t, err)
	var foundKey string
	var foundValue []byte
	var foundRevision int64
	for _, kv := range response.Kvs {
		var object map[string]any
		if json.Unmarshal(kv.Value, &object) != nil {
			continue
		}
		metadata, _ := object["metadata"].(map[string]any)
		if metadata["name"] != name || metadata["namespace"] != namespace || object["kind"] != "ConfigMap" {
			continue
		}
		require.Empty(t, foundKey, "multiple stored objects matched the ConfigMap identity")
		foundKey = string(kv.Key)
		foundValue = append([]byte(nil), kv.Value...)
		foundRevision = kv.ModRevision
	}
	require.NotEmpty(t, foundKey, "Milo did not persist the created ConfigMap as JSON under the project prefix")
	return foundKey, foundValue, foundRevision
}

func findStoredNamespace(t *testing.T, ctx context.Context, endpoint, name string) (string, []byte, int64) {
	t.Helper()
	client := newEtcdClient(t, endpoint)
	response, err := client.Get(ctx, "/", clientv3.WithPrefix())
	require.NoError(t, err)
	var foundKey string
	var foundValue []byte
	var foundRevision int64
	for _, kv := range response.Kvs {
		var object map[string]any
		if json.Unmarshal(kv.Value, &object) != nil || object["kind"] != "Namespace" {
			continue
		}
		metadata, _ := object["metadata"].(map[string]any)
		if metadata["name"] != name {
			continue
		}
		require.Empty(t, foundKey, "multiple stored Namespace objects matched the name")
		foundKey = string(kv.Key)
		foundValue = append([]byte(nil), kv.Value...)
		foundRevision = kv.ModRevision
	}
	require.NotEmpty(t, foundKey, "Milo did not persist the created Namespace as JSON")
	return foundKey, foundValue, foundRevision
}

func withOwnershipMarker(value []byte) ([]byte, error) {
	var object map[string]any
	if err := json.Unmarshal(value, &object); err != nil {
		return nil, err
	}
	metadata, ok := object["metadata"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("stored ConfigMap metadata is not an object")
	}
	labels, ok := metadata["labels"].(map[string]any)
	if !ok {
		labels = map[string]any{}
		metadata["labels"] = labels
	}
	labels[appsownership.ProjectUIDLabel] = "probe-project"
	annotations, ok := metadata["annotations"].(map[string]any)
	if !ok {
		annotations = map[string]any{}
		metadata["annotations"] = annotations
	}
	annotations[appsownership.AppUIDLabel] = "probe-app"
	return json.Marshal(object)
}

func seedConfigMapValue(t *testing.T, ctx context.Context, endpoint, key string, revision int64, value []byte) {
	t.Helper()
	client := newEtcdClient(t, endpoint)
	result, err := client.Txn(ctx).
		If(clientv3.Compare(clientv3.ModRevision(key), "=", revision)).
		Then(clientv3.OpPut(key, string(value))).Commit()
	require.NoError(t, err)
	require.True(t, result.Succeeded, "stored ConfigMap changed while preparing isolated marker seed")
}

func newEtcdClient(t *testing.T, endpoint string) *clientv3.Client {
	t.Helper()
	endpoint = strings.TrimPrefix(endpoint, "http://")
	client, err := clientv3.New(clientv3.Config{Endpoints: []string{endpoint}, DialTimeout: 3 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client
}
