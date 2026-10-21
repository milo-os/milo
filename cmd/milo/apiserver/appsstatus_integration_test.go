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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// This probe enables only an exact native status principal. AlwaysAllow remains
// test-only so successful generic impersonation reaches, and is denied by, the
// ownership guard. It proves no production IAM or credential-custody contract.
func TestAppsStatusWriterHTTPIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("starts etcd and full Milo API server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 105*time.Second)
	defer cancel()
	root, client, tokens, endpoint := startStatusProbe(t, ctx)
	project := "apps-status-project"
	// Milo installs its own Project CRD during startup. Wait for the real root API.
	var code int
	var body []byte
	projectCollection := root + "/apis/resourcemanager.miloapis.com/v1alpha1/projects"
	deadline := time.Now().Add(15 * time.Second)
	for {
		code, body = requestJSON(t, ctx, client, tokens["system-admin"], http.MethodGet, projectCollection, nil, nil)
		if code == http.StatusOK {
			break
		}
		require.Truef(t, time.Now().Before(deadline), "Project CRD not ready: %s", body)
		time.Sleep(100 * time.Millisecond)
	}
	code, body = requestJSON(t, ctx, client, tokens["system-admin"], http.MethodPost, projectCollection, map[string]any{"apiVersion": "resourcemanager.miloapis.com/v1alpha1", "kind": "Project", "metadata": map[string]any{"name": project}, "spec": map[string]any{"ownerRef": map[string]any{"kind": "Organization", "name": "probe-organization"}}}, nil)
	require.Equalf(t, http.StatusCreated, code, "Project: %s", body)
	var projectObject map[string]any
	require.NoError(t, json.Unmarshal(body, &projectObject))
	projectUID := projectObject["metadata"].(map[string]any)["uid"].(string)
	base := projectCollection + "/" + project + "/control-plane"
	namespace := "status-probe"
	code, body = requestJSON(t, ctx, client, tokens["customer"], http.MethodPost, base+"/api/v1/namespaces", map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace}}, nil)
	require.Equalf(t, http.StatusCreated, code, "Namespace: %s", body)
	installNativeCRD(t, ctx, client, tokens["system-admin"], root, workloadCRD, "workloads.compute.datumapis.com")
	installNativeCRD(t, ctx, client, tokens["system-admin"], root, httpRouteCRD, "httproutes.gateway.networking.k8s.io")
	for _, resource := range []nativeResourceCase{
		{name: "workload", group: "compute.datumapis.com", version: "v1alpha", plural: "workloads", kind: "Workload", object: workloadObject},
		{name: "route-v1", group: "gateway.networking.k8s.io", version: "v1", plural: "httproutes", kind: "HTTPRoute", object: httpRouteObject},
		{name: "route-v1beta1", group: "gateway.networking.k8s.io", version: "v1beta1", plural: "httproutes", kind: "HTTPRoute", object: httpRouteObject},
	} {
		t.Run(resource.name, func(t *testing.T) {
			api := base + "/apis/" + resource.group + "/" + resource.version
			pollProjectDiscovery(t, ctx, client, tokens["customer"], api, resource.plural, resource.kind)
			collection := api + "/namespaces/" + namespace + "/" + resource.plural
			name := resource.name
			code, body := requestJSON(t, ctx, client, tokens["customer"], http.MethodPost, collection, resource.object(namespace, name, resource.version), nil)
			require.Equalf(t, http.StatusCreated, code, "unmarked create: %s", body)
			key, value, revision := findStoredNativeObject(t, ctx, endpoint, project, namespace, name, resource.kind)
			var stored map[string]any
			require.NoError(t, json.Unmarshal(value, &stored))
			stored["metadata"].(map[string]any)["labels"] = map[string]any{appsownership.ProjectUIDLabel: projectUID, appsownership.AppUIDLabel: "absent-test-app"}
			raw, err := json.Marshal(stored)
			require.NoError(t, err)
			seedConfigMapValue(t, ctx, endpoint, key, revision, raw)
			objectURL := collection + "/" + name
			payload := statusFor(resource.kind)
			if resource.kind == "HTTPRoute" {
				parent := payload["parents"].([]any)[0].(map[string]any)["parentRef"].(map[string]any)
				parent["group"] = "gateway.networking.k8s.io"
				parent["kind"] = "Gateway"
			}
			patch := map[string]any{"status": payload}
			headers := map[string]string{"Content-Type": "application/merge-patch+json"}
			// The token's exact authenticated UID is configured for status only.
			code, body = requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodPatch, objectURL+"/status", patch, headers)
			require.Equalf(t, http.StatusOK, code, "configured status: %s", body)
			var accepted map[string]any
			require.NoError(t, json.Unmarshal(body, &accepted))
			wantStatus, err := json.Marshal(patch["status"])
			require.NoError(t, err)
			actualStatus, err := json.Marshal(accepted["status"])
			require.NoError(t, err)
			require.JSONEq(t, string(wantStatus), string(actualStatus))

			// Native CRD status strategy discards non-status fields before
			// admission. Successful PUT/SSA must preserve the stored spec and
			// metadata even when a status payload tries to alter them.
			originalSpec, err := json.Marshal(accepted["spec"])
			require.NoError(t, err)
			originalMeta := objectMetadataSnapshot(t, accepted)
			accepted["spec"] = map[string]any{"attempted": "spec replacement"}
			maliciousMeta := accepted["metadata"].(map[string]any)
			maliciousMeta["labels"] = map[string]any{}
			maliciousMeta["finalizers"] = []string{"external.example/hold"}
			code, body = requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodPut, objectURL+"/status", accepted, nil)
			require.Equalf(t, http.StatusOK, code, "native status PUT: %s", body)
			require.NoError(t, json.Unmarshal(body, &accepted))
			afterMeta := objectMetadataSnapshot(t, accepted)
			originalMeta.ResourceVersion = afterMeta.ResourceVersion
			require.Equal(t, originalMeta, afterMeta)
			require.Empty(t, accepted["metadata"].(map[string]any)["finalizers"])
			unchangedSpec, err := json.Marshal(accepted["spec"])
			require.NoError(t, err)
			require.JSONEq(t, string(originalSpec), string(unchangedSpec))
			applyStatus := map[string]any{"apiVersion": resource.group + "/" + resource.version, "kind": resource.kind, "metadata": map[string]any{"name": name, "namespace": namespace}, "status": payload}
			code, body = requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodPatch, objectURL+"/status?fieldManager=native-status-probe&force=true", applyStatus, map[string]string{"Content-Type": "application/apply-patch+yaml"})
			require.Equalf(t, http.StatusOK, code, "native status SSA: %s", body)
			require.NoError(t, json.Unmarshal(body, &accepted))
			before := objectMetadataSnapshot(t, accepted)
			for _, identity := range []string{"customer", "release-worker", "system-admin"} {
				code, body = requestJSON(t, ctx, client, tokens[identity], http.MethodPatch, objectURL+"/status", patch, headers)
				require.Equalf(t, http.StatusForbidden, code, "unconfigured %s: %s", identity, body)
				requireAppsOwnershipDenial(t, body)
			}
			for _, impersonation := range []map[string]string{
				{"Impersonate-User": "system:serviceaccount:apps-system:controller", "Impersonate-Uid": "apps-controller-uid"},
				{"Impersonate-User": ""},
				{"Impersonate-Unknown": "ignored"},
			} {
				impersonation["Content-Type"] = "application/merge-patch+json"
				for _, identity := range []string{"system-admin", "apps-controller"} {
					code, body = requestJSON(t, ctx, client, tokens[identity], http.MethodPatch, objectURL+"/status", patch, impersonation)
					require.Equalf(t, http.StatusForbidden, code, "impersonation by %s: %s", identity, body)
					requireAppsOwnershipDenial(t, body)
				}
			}
			for _, operation := range []struct {
				method, path string
				body         any
			}{
				{http.MethodPatch, objectURL, map[string]any{"metadata": map[string]any{"finalizers": []string{"native.example/probe"}}}},
				{http.MethodPatch, objectURL, map[string]any{"metadata": map[string]any{"labels": nil}}},
				{http.MethodDelete, objectURL, nil},
				{http.MethodPost, collection, map[string]any{"apiVersion": resource.group + "/" + resource.version, "kind": resource.kind, "metadata": map[string]any{"name": "forged-owner", "namespace": namespace, "labels": map[string]string{appsownership.ProjectUIDLabel: projectUID, appsownership.AppUIDLabel: "absent-test-app"}}, "spec": resource.object(namespace, name, resource.version)["spec"]}},
			} {
				operationHeaders := headers
				if operation.method == http.MethodPost {
					operationHeaders = nil
				}
				code, body = requestJSON(t, ctx, client, tokens["apps-controller"], operation.method, operation.path, operation.body, operationHeaders)
				require.Equalf(t, http.StatusForbidden, code, "protected %s: %s", operation.method, body)
				requireAppsOwnershipDenial(t, body)
			}
			code, body = requestJSON(t, ctx, client, tokens["customer"], http.MethodGet, objectURL, nil, nil)
			require.Equal(t, http.StatusOK, code)
			var unchanged map[string]any
			require.NoError(t, json.Unmarshal(body, &unchanged))
			require.Equal(t, before, objectMetadataSnapshot(t, unchanged))
		})
	}
	// Project name reuse cannot authorize status on markers pinned to the former
	// UID. Use raw CAS only in this isolated store, simulating authoritative UID
	// change without a production teardown controller or adopting new ownership.
	key, value, revision := findStatusRootProject(t, ctx, endpoint, project)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(value, &stored))
	stored["metadata"].(map[string]any)["uid"] = "replacement-project-uid"
	raw, err := json.Marshal(stored)
	require.NoError(t, err)
	seedConfigMapValue(t, ctx, endpoint, key, revision, raw)
	code, body = requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodPatch, base+"/apis/compute.datumapis.com/v1alpha/namespaces/"+namespace+"/workloads/workload/status", map[string]any{"status": statusFor("Workload")}, map[string]string{"Content-Type": "application/merge-patch+json"})
	require.Equalf(t, http.StatusForbidden, code, "changed root Project UID: %s", body)
	requireAppsOwnershipDenial(t, body)
}

func startStatusProbe(t *testing.T, ctx context.Context, finalizers ...appsownership.FinalizerWriter) (string, *http.Client, map[string]string, string) {
	t.Helper()
	etcdPath, err := findEtcdBinary()
	require.NoError(t, err)
	etcd := &envtest.Etcd{Path: etcdPath, StartTimeout: 20 * time.Second, StopTimeout: 10 * time.Second}
	require.NoError(t, etcd.Start())
	t.Cleanup(func() { require.NoError(t, etcd.Stop()) })
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "tokens.csv")
	tokens := map[string]string{}
	writeTokenFile(t, tokenFile, tokens)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyPath := filepath.Join(dir, "sa.key")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600))
	rules := []appsownership.StatusWriter{
		{Username: "system:serviceaccount:apps-system:controller", UID: "apps-controller-uid", Group: "compute.datumapis.com", Version: "v1alpha", Resource: "workloads", Kind: "Workload"},
		{Username: "system:serviceaccount:apps-system:controller", UID: "apps-controller-uid", Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes", Kind: "HTTPRoute"},
		{Username: "system:serviceaccount:apps-system:controller", UID: "apps-controller-uid", Group: "gateway.networking.k8s.io", Version: "v1beta1", Resource: "httproutes", Kind: "HTTPRoute"},
	}
	// A finalizer probe must not gain status authority.
	if len(finalizers) > 0 {
		rules = nil
	}
	config, err := json.Marshal(map[string]any{"apiVersion": "apiserver.config.k8s.io/v1", "kind": "AdmissionConfiguration", "plugins": []any{map[string]any{"name": appsownership.PluginName, "configuration": appsownership.Configuration{APIVersion: "apiserver.miloapis.com/v1alpha1", Kind: "AppsOwnershipConfiguration", StatusWriters: rules, FinalizerWriters: finalizers}}}})
	require.NoError(t, err)
	configPath := filepath.Join(dir, "admission.json")
	require.NoError(t, os.WriteFile(configPath, config, 0o600))
	port := freeTCPPort(t)
	flags, err := json.Marshal([]string{"--etcd-servers=" + etcd.URL.String(), "--bind-address=127.0.0.1", fmt.Sprintf("--secure-port=%d", port), "--cert-dir=" + filepath.Join(dir, "certs"), "--token-auth-file=" + tokenFile, "--anonymous-auth=false", "--authorization-mode=AlwaysAllow", "--service-account-issuer=https://milo.apps-status.test", "--api-audiences=milo-apps-status-test", "--service-account-signing-key-file=" + keyPath, "--service-account-key-file=" + keyPath, "--storage-media-type=application/json", "--watch-cache=false", "--admission-control-config-file=" + configPath})
	require.NoError(t, err)
	serverCtx, stop := context.WithCancel(ctx)
	command := exec.CommandContext(serverCtx, os.Args[0], "-test.run=^TestAppsOwnershipHTTPChild$")
	command.Env = append(os.Environ(), serverChildEnv+"=1", serverArgsEnv+"="+string(flags))
	var logs lockedBuffer
	command.Stdout = &logs
	command.Stderr = &logs
	require.NoError(t, command.Start())
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
	return root, client, tokens, etcd.URL.Host
}

func findStatusRootProject(t *testing.T, ctx context.Context, endpoint, name string) (string, []byte, int64) {
	t.Helper()
	response, err := newEtcdClient(t, endpoint).Get(ctx, "/", clientv3.WithPrefix())
	require.NoError(t, err)
	var key string
	var value []byte
	var revision int64
	for _, kv := range response.Kvs {
		var obj map[string]any
		if json.Unmarshal(kv.Value, &obj) != nil || obj["kind"] != "Project" {
			continue
		}
		metadata, _ := obj["metadata"].(map[string]any)
		if metadata["name"] != name {
			continue
		}
		require.Empty(t, key)
		key = string(kv.Key)
		value = append([]byte(nil), kv.Value...)
		revision = kv.ModRevision
	}
	require.NotEmpty(t, key)
	return key, value, revision
}
