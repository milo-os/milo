package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestAppsProjectCommitFenceHTTPIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("starts two real Milo replicas and etcd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	path, err := findEtcdBinary()
	require.NoError(t, err)
	etcd := &envtest.Etcd{Path: path, StartTimeout: 20 * time.Second, StopTimeout: 10 * time.Second}
	require.NoError(t, etcd.Start())
	t.Cleanup(func() { require.NoError(t, etcd.Stop()) })
	proxyURL, proxy := startFenceProxy(t, etcd.URL.Host)
	rule := appsownership.StatusWriter{Username: "system:serviceaccount:apps-system:controller", UID: "apps-controller-uid", Group: "compute.datumapis.com", Version: "v1alpha", Resource: "workloads", Kind: "Workload"}
	config := appsownership.Configuration{APIVersion: "apiserver.miloapis.com/v1alpha1", Kind: "AppsOwnershipConfiguration", StatusWriters: []appsownership.StatusWriter{rule}, FinalizerWriters: []appsownership.FinalizerWriter{{StatusWriter: rule, Finalizer: "compute.datumapis.com/workload-controller"}}}
	rootA, clientA, tokensA, _ := startNativeProbeReplica(t, ctx, proxyURL, config)
	rootB, clientB, tokensB, _ := startNativeProbeReplica(t, ctx, etcd.URL.String(), config)
	projectsA := rootA + "/apis/resourcemanager.miloapis.com/v1alpha1/projects"
	projectsB := rootB + "/apis/resourcemanager.miloapis.com/v1alpha1/projects"
	pollProjectDiscovery(t, ctx, clientA, tokensA["customer"], rootA+"/apis/resourcemanager.miloapis.com/v1alpha1", "projects", "Project")
	pollProjectDiscovery(t, ctx, clientB, tokensB["customer"], rootB+"/apis/resourcemanager.miloapis.com/v1alpha1", "projects", "Project")
	installNativeCRD(t, ctx, clientA, tokensA["system-admin"], rootA, workloadCRD, "workloads.compute.datumapis.com")
	for _, action := range []string{"status", "last-finalizer"} {
		for _, mode := range []string{"put", "merge", "ssa"} {
			t.Run(action+"-"+mode, func(t *testing.T) {
				name := "fence-" + action + "-" + mode
				code, body := requestJSON(t, ctx, clientA, tokensA["system-admin"], http.MethodPost, projectsA, map[string]any{"apiVersion": "resourcemanager.miloapis.com/v1alpha1", "kind": "Project", "metadata": map[string]any{"name": name, "finalizers": []string{"test.example/hold"}}, "spec": map[string]any{"ownerRef": map[string]any{"kind": "Organization", "name": "probe"}}}, nil)
				require.Equalf(t, http.StatusCreated, code, "%s", body)
				var rootObject map[string]any
				require.NoError(t, json.Unmarshal(body, &rootObject))
				uid := rootObject["metadata"].(map[string]any)["uid"]
				base := projectsA + "/" + name + "/control-plane"
				code, body = requestJSON(t, ctx, clientA, tokensA["customer"], http.MethodPost, base+"/api/v1/namespaces", map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "probe"}}, nil)
				require.Equalf(t, 201, code, "%s", body)
				pollProjectDiscovery(t, ctx, clientA, tokensA["customer"], base+"/apis/compute.datumapis.com/v1alpha", "workloads", "Workload")
				collection := base + "/apis/compute.datumapis.com/v1alpha/namespaces/probe/workloads"
				code, body = requestJSON(t, ctx, clientA, tokensA["customer"], http.MethodPatch, collection+"/"+name+"?fieldManager=project-fence-create&force=true", workloadObject("probe", name, "v1alpha"), map[string]string{"Content-Type": "application/apply-patch+yaml"})
				require.Equalf(t, 201, code, "%s", body)
				if action == "last-finalizer" && mode == "ssa" {
					// Establish the SSA manager while the object is unmarked, before
					// test-only ownership/termination seeding. Empty apply cannot
					// remove an unowned finalizer from another manager.
					code, body = requestJSON(t, ctx, clientA, tokensA["customer"], http.MethodPatch, collection+"/"+name+"?fieldManager=project-fence-probe&force=true", map[string]any{"apiVersion": "compute.datumapis.com/v1alpha", "kind": "Workload", "metadata": map[string]any{"name": name, "namespace": "probe", "finalizers": []string{"compute.datumapis.com/workload-controller"}}}, map[string]string{"Content-Type": "application/apply-patch+yaml"})
					require.Equalf(t, 200, code, "%s", body)
				}
				key, value, revision := findStoredNativeObject(t, ctx, etcd.URL.Host, name, "probe", name, "Workload")
				var stored map[string]any
				require.NoError(t, json.Unmarshal(value, &stored))
				meta := stored["metadata"].(map[string]any)
				// Storage fixtures only: acquisition and trusted Apps deletion remain disabled.
				meta["labels"] = map[string]any{appsownership.ProjectUIDLabel: uid, appsownership.AppUIDLabel: "fixture-app"}
				if action == "last-finalizer" {
					meta["finalizers"] = []string{"compute.datumapis.com/workload-controller"}
					meta["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
					meta["deletionGracePeriodSeconds"] = 0
				}
				raw, err := json.Marshal(stored)
				require.NoError(t, err)
				seedConfigMapValue(t, ctx, etcd.URL.Host, key, revision, raw)
				objectURL := collection + "/" + name
				code, body = requestJSON(t, ctx, clientA, tokensA["customer"], http.MethodGet, objectURL, nil, nil)
				require.Equal(t, 200, code)
				var current map[string]any
				require.NoError(t, json.Unmarshal(body, &current))
				before := cloneNativeObject(t, current)
				meta = current["metadata"].(map[string]any)
				patch := map[string]any{"apiVersion": "compute.datumapis.com/v1alpha", "kind": "Workload", "metadata": map[string]any{"name": name, "namespace": "probe", "uid": meta["uid"], "resourceVersion": meta["resourceVersion"]}}
				url := objectURL
				if action == "status" {
					patch["status"] = statusFor("Workload")
					current["status"] = patch["status"]
					url += "/status"
				} else {
					patch["metadata"].(map[string]any)["finalizers"] = []any{}
					meta["finalizers"] = []any{}
				}
				method := http.MethodPatch
				payload := any(patch)
				headers := map[string]string{"Content-Type": "application/merge-patch+json"}
				if mode == "put" {
					method = http.MethodPut
					payload = current
					headers = nil
				}
				if mode == "ssa" {
					headers["Content-Type"] = "application/apply-patch+yaml"
					url += "?fieldManager=project-fence-probe&force=true"
				}
				pause := proxy.arm(key)
				resumed := false
				t.Cleanup(func() {
					if !resumed {
						close(pause.resume)
					}
				})
				type reply struct {
					code int
					body []byte
				}
				done := make(chan reply, 1)
				go func() {
					c, b := requestJSON(t, ctx, clientA, tokensA["apps-controller"], method, url, payload, headers)
					done <- reply{c, b}
				}()
				select {
				case <-pause.arrived:
				case r := <-done:
					t.Fatalf("no etcd write reached barrier: %d %s", r.code, r.body)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				// Replica B completes a real root DELETE while A's already-authorized child
				// transaction is paused. Its held root finalizer retains deletionTimestamp.
				code, body = requestJSON(t, ctx, clientB, tokensB["system-admin"], http.MethodDelete, projectsB+"/"+name, map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]any{"uid": uid}}, nil)
				require.Equalf(t, 200, code, "root deletion: %s", body)
				close(pause.resume)
				resumed = true
				var result reply
				select {
				case result = <-done:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				code, body = requestJSON(t, ctx, clientA, tokensA["customer"], http.MethodGet, objectURL, nil, nil)
				require.Equalf(t, http.StatusConflict, result.code, "stale commit: %s", result.body)
				require.Equal(t, 200, code)
				var after map[string]any
				require.NoError(t, json.Unmarshal(body, &after))
				require.Equal(t, before, after)
			})
		}
	}
}
