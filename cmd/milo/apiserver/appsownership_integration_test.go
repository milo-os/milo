package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
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
	"sigs.k8s.io/yaml"
)

// These fixtures are byte-for-byte upstream CRDs. Their source commits and
// hashes, plus Milo's project-context metadata overlay, are recorded in
// docs/architecture/apps-ownership/native-crd-http-proof.md.
//
//go:embed testdata/appsownership/workloads.compute.datumapis.com.yaml
var workloadCRD []byte

//go:embed testdata/appsownership/httproutes.gateway.networking.k8s.io.yaml
var httpRouteCRD []byte

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

	// Install exact upstream CRDs through Milo's platform/root API, then use
	// their real schemas and the project control-plane paths for ownership
	// checks. This does not start the Workload or Gateway controllers.
	probeNativeResourceOwnership(t, ctx, client, tokens, etcd.URL.Host, serverPort, project, namespace)

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

type nativeResourceCase struct {
	name, group, version, plural, kind string
	object                             func(namespace, name, version string) map[string]any
}

func probeNativeResourceOwnership(t *testing.T, ctx context.Context, client *http.Client, tokens map[string]string, etcdEndpoint string, port int, project, namespace string) {
	t.Helper()
	root := fmt.Sprintf("https://127.0.0.1:%d", port)
	base := root + "/apis/resourcemanager.miloapis.com/v1alpha1/projects/" + project + "/control-plane"
	installNativeCRD(t, ctx, client, tokens["system-admin"], root, workloadCRD, "workloads.compute.datumapis.com")
	installNativeCRD(t, ctx, client, tokens["system-admin"], root, httpRouteCRD, "httproutes.gateway.networking.k8s.io")

	for _, resource := range []nativeResourceCase{
		{
			name: "workload", group: "compute.datumapis.com", version: "v1alpha", plural: "workloads", kind: "Workload",
			object: workloadObject,
		},
		{
			name: "httproute-v1", group: "gateway.networking.k8s.io", version: "v1", plural: "httproutes", kind: "HTTPRoute",
			object: httpRouteObject,
		},
		{
			name: "httproute-v1beta1", group: "gateway.networking.k8s.io", version: "v1beta1", plural: "httproutes", kind: "HTTPRoute",
			object: httpRouteObject,
		},
	} {
		t.Run(resource.name, func(t *testing.T) {
			api := base + "/apis/" + resource.group + "/" + resource.version
			pollProjectDiscovery(t, ctx, client, tokens["customer"], api, resource.plural, resource.kind)
			collection := api + "/namespaces/" + namespace + "/" + resource.plural
			identities := []string{"customer", "apps-controller", "release-worker", "system-admin"}
			for _, identity := range identities {
				t.Run(identity+" cannot create or forge ownership", func(t *testing.T) {
					name := "marked-" + identity
					marked := resource.object(namespace, name, resource.version)
					marked["metadata"].(map[string]any)["labels"] = map[string]string{appsownership.ProjectUIDLabel: "probe-project"}
					status, body := requestJSON(t, ctx, client, tokens[identity], http.MethodPost, collection, marked, nil)
					require.Equalf(t, http.StatusForbidden, status, "marker create body: %s", body)
					requireAppsOwnershipDenial(t, body)

					plainName := "plain-" + identity
					status, body = requestJSON(t, ctx, client, tokens[identity], http.MethodPost, collection, resource.object(namespace, plainName, resource.version), nil)
					require.Equalf(t, http.StatusCreated, status, "valid unmarked %s control create body: %s", resource.kind, body)
					objectURL := collection + "/" + plainName
					for _, metadata := range []map[string]any{
						{"labels": map[string]string{appsownership.ProjectUIDLabel: "probe-project"}},
						{"annotations": map[string]string{appsownership.AppUIDLabel: "probe-app"}},
					} {
						status, body = requestJSON(t, ctx, client, tokens[identity], http.MethodPatch, objectURL, map[string]any{"metadata": metadata}, map[string]string{"Content-Type": "application/merge-patch+json"})
						require.Equalf(t, http.StatusForbidden, status, "marker patch body: %s", body)
						requireAppsOwnershipDenial(t, body)
					}
					status, body = requestJSON(t, ctx, client, tokens[identity], http.MethodDelete, objectURL, nil, nil)
					require.Equalf(t, http.StatusOK, status, "unmarked delete control body: %s", body)
				})
			}

			// Create through Milo, locate by decoded object identity in the isolated
			// project's actual etcd records, and CAS only the marker metadata.
			seedName := "old-" + resource.name
			status, body := requestJSON(t, ctx, client, tokens["customer"], http.MethodPost, collection, resource.object(namespace, seedName, resource.version), nil)
			require.Equalf(t, http.StatusCreated, status, "create marker seed: %s", body)
			key, value, revision := findStoredNativeObject(t, ctx, etcdEndpoint, project, namespace, seedName, resource.kind)
			markedValue, err := withOwnershipMarker(value)
			require.NoError(t, err)
			seedConfigMapValue(t, ctx, etcdEndpoint, key, revision, markedValue)
			objectURL := collection + "/" + seedName
			status, body = requestJSON(t, ctx, client, tokens["customer"], http.MethodGet, objectURL, nil, nil)
			require.Equalf(t, http.StatusOK, status, "raw marker seed not visible through Milo: %s", body)
			require.Contains(t, string(body), appsownership.ProjectUIDLabel)

			for _, identity := range identities {
				t.Run(identity+" cannot mutate old marked "+resource.name, func(t *testing.T) {
					status, body := requestJSON(t, ctx, client, tokens["customer"], http.MethodGet, objectURL, nil, nil)
					require.Equalf(t, http.StatusOK, status, "read old marker seed: %s", body)
					var existing map[string]any
					require.NoError(t, json.Unmarshal(body, &existing))
					before := objectMetadataSnapshot(t, existing)
					operations := []struct {
						name, method, endpoint string
						body                   any
						headers                map[string]string
					}{
						{"PUT update", http.MethodPut, objectURL, existing, nil},
						{"merge patch", http.MethodPatch, objectURL, map[string]any{"metadata": map[string]any{"finalizers": []string{"apps.miloapis.com/probe"}}}, map[string]string{"Content-Type": "application/merge-patch+json"}},
						{"strip ownership marker", http.MethodPatch, objectURL, map[string]any{"metadata": map[string]any{"labels": map[string]any{appsownership.ProjectUIDLabel: nil}, "annotations": map[string]any{appsownership.AppUIDLabel: nil}}}, map[string]string{"Content-Type": "application/merge-patch+json"}},
						{"server-side apply", http.MethodPatch, objectURL + "?fieldManager=apps-ownership-probe&force=true", resource.object(namespace, seedName, resource.version), map[string]string{"Content-Type": "application/apply-patch+yaml"}},
						{"status update", http.MethodPatch, objectURL + "/status", map[string]any{"status": statusFor(resource.kind)}, map[string]string{"Content-Type": "application/merge-patch+json"}},
						{"DELETE", http.MethodDelete, objectURL, map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Background"}, nil},
					}
					for _, operation := range operations {
						t.Run(operation.name, func(t *testing.T) {
							status, body := requestJSON(t, ctx, client, tokens[identity], operation.method, operation.endpoint, operation.body, operation.headers)
							require.Equalf(t, http.StatusForbidden, status, "%s body: %s", operation.name, body)
							requireAppsOwnershipDenial(t, body)
						})
					}
					status, body = requestJSON(t, ctx, client, tokens["customer"], http.MethodGet, objectURL, nil, nil)
					require.Equalf(t, http.StatusOK, status, "read old marker after denied writes: %s", body)
					var afterObject map[string]any
					require.NoError(t, json.Unmarshal(body, &afterObject))
					require.Equal(t, before, objectMetadataSnapshot(t, afterObject), "denied writes changed stored object metadata")
				})
			}
		})
	}
}

func installNativeCRD(t *testing.T, ctx context.Context, client *http.Client, token, root string, source []byte, expectedName string) {
	t.Helper()
	wantHash := map[string]string{
		"workloads.compute.datumapis.com":      "ba38aeadb5d6c7a97c9b05a031d9955099820f5e7edd206a43fe3dd7f031b889",
		"httproutes.gateway.networking.k8s.io": "682203b109baecb18d38073a578f7bc2c17d22fe9b0c27bf030850abe014c370",
	}[expectedName]
	require.NotEmpty(t, wantHash, "unexpected native CRD fixture")
	require.Equal(t, wantHash, fmt.Sprintf("%x", sha256.Sum256(source)), "native CRD fixture differs from the pinned upstream source bytes")
	var object map[string]any
	require.NoError(t, yaml.Unmarshal(source, &object))
	metadata := object["metadata"].(map[string]any)
	require.Equal(t, expectedName, metadata["name"])
	annotations, _ := metadata["annotations"].(map[string]any)
	if annotations == nil {
		annotations = map[string]any{}
		metadata["annotations"] = annotations
	}
	// Gateway API's upstream bundle has no Milo context annotation. This
	// explicit Milo overlay allows project discovery while preserving the
	// complete upstream validation schema.
	if expectedName == "httproutes.gateway.networking.k8s.io" {
		annotations["discovery.miloapis.com/parent-contexts"] = "Project"
	}
	status, body := requestJSON(t, ctx, client, token, http.MethodPost, root+"/apis/apiextensions.k8s.io/v1/customresourcedefinitions", object, nil)
	require.Equalf(t, http.StatusCreated, status, "install exact upstream CRD %s (existing CRD is a test setup error): %s", expectedName, body)
	crdURL := root + "/apis/apiextensions.k8s.io/v1/customresourcedefinitions/" + expectedName
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for CRD %s Established: %v", expectedName, ctx.Err())
		case <-deadline.C:
			t.Fatalf("CRD %s did not become Established", expectedName)
		default:
		}
		status, body = requestJSON(t, ctx, client, token, http.MethodGet, crdURL, nil, nil)
		if status == http.StatusOK {
			var current map[string]any
			if json.Unmarshal(body, &current) == nil {
				statusBlock, _ := current["status"].(map[string]any)
				conditions, _ := statusBlock["conditions"].([]any)
				for _, raw := range conditions {
					condition, _ := raw.(map[string]any)
					if condition["type"] == "Established" && condition["status"] == "True" {
						return
					}
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// pollProjectDiscovery waits for Milo's project-context discovery resource list
// to publish the exact API resource, rather than treating a successful group
// endpoint response as proof that the requested kind is served.
func pollProjectDiscovery(t *testing.T, ctx context.Context, client *http.Client, token, endpoint, plural, kind string) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastStatus int
	var lastBody []byte
	for {
		lastStatus, lastBody = requestJSON(t, ctx, client, token, http.MethodGet, endpoint, nil, nil)
		if lastStatus == http.StatusOK {
			var list struct {
				Resources []struct {
					Name string `json:"name"`
					Kind string `json:"kind"`
				} `json:"resources"`
			}
			if json.Unmarshal(lastBody, &list) == nil {
				for _, resource := range list.Resources {
					if resource.Name == plural && resource.Kind == kind {
						return
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for project discovery %s %s: %v (last HTTP %d: %s)", plural, kind, ctx.Err(), lastStatus, lastBody)
		case <-deadline.C:
			t.Fatalf("project discovery did not expose %s %s at %s (last HTTP %d: %s)", plural, kind, endpoint, lastStatus, lastBody)
		case <-ticker.C:
		}
	}
}

type metadataSnapshot struct {
	UID             string            `json:"uid"`
	ResourceVersion string            `json:"resourceVersion"`
	Labels          map[string]string `json:"labels"`
	Annotations     map[string]string `json:"annotations"`
}

func objectMetadataSnapshot(t *testing.T, object map[string]any) metadataSnapshot {
	t.Helper()
	data, err := json.Marshal(object["metadata"])
	require.NoError(t, err)
	var snapshot metadataSnapshot
	require.NoError(t, json.Unmarshal(data, &snapshot))
	require.NotEmpty(t, snapshot.UID, "seeded object must have a storage UID")
	require.NotEmpty(t, snapshot.ResourceVersion, "seeded object must have a resourceVersion")
	require.Contains(t, snapshot.Labels, appsownership.ProjectUIDLabel)
	return snapshot
}

func workloadObject(namespace, name, version string) map[string]any {
	return map[string]any{
		"apiVersion": "compute.datumapis.com/" + version, "kind": "Workload",
		"metadata": map[string]any{"namespace": namespace, "name": name},
		"spec": map[string]any{
			"placements": []any{map[string]any{"name": "default", "locations": []any{map[string]any{"name": "dfw"}}, "scaleSettings": map[string]any{"minReplicas": 1, "instanceManagementPolicy": "OrderedReady"}}},
			"template":   map[string]any{"spec": map[string]any{"runtime": map[string]any{"resources": map[string]any{"instanceType": "datumcloud-d1-standard-2"}}, "networkInterfaces": []any{map[string]any{"network": map[string]any{"name": "probe-network"}}}}},
		},
	}
}

func statusFor(kind string) map[string]any {
	if kind == "HTTPRoute" {
		condition := map[string]any{"lastTransitionTime": "2026-10-20T00:00:00Z", "message": "probe status payload", "reason": "Probe", "status": "False", "type": "Accepted"}
		return map[string]any{"parents": []any{map[string]any{"parentRef": map[string]any{"name": "probe-gateway"}, "controllerName": "apps.probe/controller", "conditions": []any{condition}}}}
	}
	return map[string]any{"currentReplicas": 0, "deployments": 0, "desiredReplicas": 0, "readyReplicas": 0, "replicas": 0, "updatedReplicas": 0}
}

func httpRouteObject(namespace, name, version string) map[string]any {
	return map[string]any{
		"apiVersion": "gateway.networking.k8s.io/" + version, "kind": "HTTPRoute",
		"metadata": map[string]any{"namespace": namespace, "name": name},
		"spec":     map[string]any{"hostnames": []string{"probe.example.test"}, "rules": []any{map[string]any{"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/"}}}, "backendRefs": []any{map[string]any{"name": "probe-backend", "port": 8080}}}}},
	}
}

func findStoredNativeObject(t *testing.T, ctx context.Context, endpoint, project, namespace, name, kind string) (string, []byte, int64) {
	t.Helper()
	client := newEtcdClient(t, endpoint)
	response, err := client.Get(ctx, "/projects/"+project+"/", clientv3.WithPrefix())
	require.NoError(t, err)
	var foundKey string
	var foundValue []byte
	var foundRevision int64
	for _, kv := range response.Kvs {
		var object map[string]any
		if json.Unmarshal(kv.Value, &object) != nil || object["kind"] != kind {
			continue
		}
		metadata, _ := object["metadata"].(map[string]any)
		if metadata["name"] != name || metadata["namespace"] != namespace {
			continue
		}
		require.Empty(t, foundKey, "multiple records matched the native object identity")
		foundKey = string(kv.Key)
		foundValue = append([]byte(nil), kv.Value...)
		foundRevision = kv.ModRevision
	}
	require.NotEmpty(t, foundKey, "Milo did not persist the native object as JSON under the project prefix")
	return foundKey, foundValue, foundRevision
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

// bytes.Buffer.ReadFrom is promoted through embedding. os/exec uses that
// ReaderFrom fast path when copying child output, bypassing Write's lock unless
// we provide a synchronized method here as well.
func (b *lockedBuffer) ReadFrom(r io.Reader) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.ReadFrom(r)
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
