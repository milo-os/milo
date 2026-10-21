package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
)

const nativeWorkloadFinalizer = "compute.datumapis.com/workload-controller"
const nativeRouteFinalizer = "gateway.networking.datumapis.com/gateway-controller-gc"
const unrelatedFinalizer = "unrelated.example/hold"

// This real Milo/etcd probe enables only exact native finalizer principals.
// Raw storage seeds ownership and termination solely in an isolated fixture;
// neither is claimed to be an authorized Apps acquisition/deletion path.
func TestAppsFinalizerWriterHTTPIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("starts etcd and full Milo API server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	resources := []nativeResourceCase{
		{name: "workload", group: "compute.datumapis.com", version: "v1alpha", plural: "workloads", kind: "Workload", object: workloadObject},
		{name: "route-v1", group: "gateway.networking.k8s.io", version: "v1", plural: "httproutes", kind: "HTTPRoute", object: httpRouteObject},
		{name: "route-v1beta1", group: "gateway.networking.k8s.io", version: "v1beta1", plural: "httproutes", kind: "HTTPRoute", object: httpRouteObject},
	}
	var rules []appsownership.FinalizerWriter
	for _, resource := range resources {
		finalizer := nativeRouteFinalizer
		if resource.kind == "Workload" {
			finalizer = nativeWorkloadFinalizer
		}
		rules = append(rules, appsownership.FinalizerWriter{StatusWriter: appsownership.StatusWriter{Username: "system:serviceaccount:apps-system:controller", UID: "apps-controller-uid", Group: resource.group, Version: resource.version, Resource: resource.plural, Kind: resource.kind}, Finalizer: finalizer})
	}
	root, client, tokens, endpoint := startStatusProbe(t, ctx, rules...)
	project := "apps-finalizer-project"
	projects := root + "/apis/resourcemanager.miloapis.com/v1alpha1/projects"
	deadline := time.Now().Add(15 * time.Second)
	for {
		code, body := requestJSON(t, ctx, client, tokens["system-admin"], http.MethodGet, projects, nil, nil)
		if code == http.StatusOK {
			break
		}
		require.Truef(t, time.Now().Before(deadline), "Project API not ready: %s", body)
		time.Sleep(100 * time.Millisecond)
	}
	code, body := requestJSON(t, ctx, client, tokens["system-admin"], http.MethodPost, projects, map[string]any{"apiVersion": "resourcemanager.miloapis.com/v1alpha1", "kind": "Project", "metadata": map[string]any{"name": project}, "spec": map[string]any{"ownerRef": map[string]any{"kind": "Organization", "name": "probe-organization"}}}, nil)
	require.Equalf(t, http.StatusCreated, code, "Project: %s", body)
	var projectObject map[string]any
	require.NoError(t, json.Unmarshal(body, &projectObject))
	projectUID := projectObject["metadata"].(map[string]any)["uid"].(string)
	base := projects + "/" + project + "/control-plane"
	namespace := "finalizer-probe"
	code, body = requestJSON(t, ctx, client, tokens["customer"], http.MethodPost, base+"/api/v1/namespaces", map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace}}, nil)
	require.Equalf(t, http.StatusCreated, code, "namespace: %s", body)
	installNativeCRD(t, ctx, client, tokens["system-admin"], root, workloadCRD, "workloads.compute.datumapis.com")
	installNativeCRD(t, ctx, client, tokens["system-admin"], root, httpRouteCRD, "httproutes.gateway.networking.k8s.io")
	for _, resource := range resources {
		finalizer := nativeRouteFinalizer
		if resource.kind == "Workload" {
			finalizer = nativeWorkloadFinalizer
		}
		api := base + "/apis/" + resource.group + "/" + resource.version
		pollProjectDiscovery(t, ctx, client, tokens["customer"], api, resource.plural, resource.kind)
		collection := api + "/namespaces/" + namespace + "/" + resource.plural
		for _, mode := range []string{"put", "merge", "apply"} {
			t.Run(resource.name+"/"+mode, func(t *testing.T) {
				name := resource.name + "-" + mode
				objectURL := collection + "/" + name
				code, body := requestJSON(t, ctx, client, tokens["customer"], http.MethodPost, collection, resource.object(namespace, name, resource.version), nil)
				require.Equalf(t, http.StatusCreated, code, "create fixture: %s", body)
				mutateStorage := func(mutate func(map[string]any)) {
					key, value, revision := findStoredNativeObject(t, ctx, endpoint, project, namespace, name, resource.kind)
					var stored map[string]any
					require.NoError(t, json.Unmarshal(value, &stored))
					mutate(stored)
					raw, err := json.Marshal(stored)
					require.NoError(t, err)
					seedConfigMapValue(t, ctx, endpoint, key, revision, raw)
				}
				// Mark and add another controller's finalizer; no trusted acquisition path
				// is enabled by this test.
				mutateStorage(func(o map[string]any) {
					m := o["metadata"].(map[string]any)
					m["labels"] = map[string]any{appsownership.ProjectUIDLabel: projectUID, appsownership.AppUIDLabel: "test-app"}
					m["finalizers"] = []string{unrelatedFinalizer}
				})
				get := func() map[string]any {
					code, body := requestJSON(t, ctx, client, tokens["customer"], http.MethodGet, objectURL, nil, nil)
					require.Equalf(t, 200, code, "get: %s", body)
					var o map[string]any
					require.NoError(t, json.Unmarshal(body, &o))
					return o
				}
				original := get()
				oldUID := original["metadata"].(map[string]any)["uid"].(string)
				update := func(current map[string]any, finalizers []string) (int, []byte) {
					metadata := current["metadata"].(map[string]any)
					switch mode {
					case "put":
						copy := cloneNativeObject(t, current)
						copy["metadata"].(map[string]any)["finalizers"] = finalizers
						return requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodPut, objectURL, copy, nil)
					case "merge":
						return requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodPatch, objectURL, map[string]any{"metadata": map[string]any{"uid": metadata["uid"], "resourceVersion": metadata["resourceVersion"], "finalizers": finalizers}}, map[string]string{"Content-Type": "application/merge-patch+json"})
					default:
						return requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodPatch, objectURL+"?fieldManager=native-finalizer-probe&force=true", map[string]any{"apiVersion": resource.group + "/" + resource.version, "kind": resource.kind, "metadata": map[string]any{"name": name, "namespace": namespace, "uid": metadata["uid"], "resourceVersion": metadata["resourceVersion"], "finalizers": finalizers}}, map[string]string{"Content-Type": "application/apply-patch+yaml"})
					}
				}
				patchHeaders := map[string]string{"Content-Type": "application/merge-patch+json"}
				addPatch := map[string]any{"metadata": map[string]any{"finalizers": []string{unrelatedFinalizer, finalizer}}}
				for _, identity := range []string{"customer", "release-worker", "system-admin"} {
					code, body = requestJSON(t, ctx, client, tokens[identity], http.MethodPatch, objectURL, addPatch, patchHeaders)
					require.Equalf(t, 403, code, "unconfigured %s: %s", identity, body)
					requireAppsOwnershipDenial(t, body)
				}
				for _, headers := range []map[string]string{
					{"Impersonate-User": "system:serviceaccount:apps-system:controller", "Impersonate-Uid": "apps-controller-uid"},
					{"Impersonate-User": ""}, {"Impersonate-Unknown": "ignored"},
				} {
					headers["Content-Type"] = "application/merge-patch+json"
					for _, identity := range []string{"system-admin", "apps-controller"} {
						code, body = requestJSON(t, ctx, client, tokens[identity], http.MethodPatch, objectURL, addPatch, headers)
						require.Equalf(t, 403, code, "impersonation: %s", body)
						requireAppsOwnershipDenial(t, body)
					}
				}
				for label, mutate := range map[string]func(map[string]any){
					"foreign finalizer": func(o map[string]any) {
						o["metadata"].(map[string]any)["finalizers"] = []string{unrelatedFinalizer, finalizer, "foreign.example/finalizer"}
					},
					"remove unrelated": func(o map[string]any) { o["metadata"].(map[string]any)["finalizers"] = []string{finalizer} },
					"labels": func(o map[string]any) {
						o["metadata"].(map[string]any)["labels"] = map[string]any{appsownership.ProjectUIDLabel: projectUID, appsownership.AppUIDLabel: "foreign-app"}
					},
					"owner reference": func(o map[string]any) {
						o["metadata"].(map[string]any)["ownerReferences"] = []any{map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "name": "outside", "uid": "foreign-uid"}}
					},
					"spec": func(o map[string]any) {
						if resource.kind == "Workload" {
							o["spec"].(map[string]any)["placements"].([]any)[0].(map[string]any)["scaleSettings"].(map[string]any)["minReplicas"] = float64(2)
						} else {
							o["spec"].(map[string]any)["hostnames"] = []string{"changed.example.test"}
						}
					},
				} {
					candidate := cloneNativeObject(t, original)
					candidate["metadata"].(map[string]any)["finalizers"] = []string{unrelatedFinalizer, finalizer}
					mutate(candidate)
					code, body = requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodPut, objectURL, candidate, nil)
					require.Equalf(t, 403, code, "%s: %s", label, body)
					requireAppsOwnershipDenial(t, body)
				}
				// Finalizer configuration grants neither status nor marker acquisition.
				code, body = requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodPatch, objectURL+"/status", map[string]any{"status": statusFor(resource.kind)}, patchHeaders)
				require.Equalf(t, 403, code, "status capability leaked: %s", body)
				requireAppsOwnershipDenial(t, body)
				forged := resource.object(namespace, "forged-"+name, resource.version)
				forged["metadata"].(map[string]any)["labels"] = map[string]any{appsownership.ProjectUIDLabel: projectUID, appsownership.AppUIDLabel: "forged-app"}
				code, body = requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodPost, collection, forged, nil)
				require.Equalf(t, 403, code, "marker acquisition: %s", body)
				requireAppsOwnershipDenial(t, body)
				require.Equal(t, original, get(), "denied requests changed full native object")
				code, body = update(original, []string{unrelatedFinalizer, finalizer})
				require.Equalf(t, 200, code, "exact finalizer add: %s", body)
				current := get()
				require.Equal(t, []any{unrelatedFinalizer, finalizer}, current["metadata"].(map[string]any)["finalizers"])
				code, body = update(current, []string{unrelatedFinalizer})
				require.Equalf(t, 403, code, "removal before termination: %s", body)
				requireAppsOwnershipDenial(t, body)
				code, body = requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodDelete, objectURL, nil, nil)
				require.Equalf(t, 403, code, "direct deletion: %s", body)
				requireAppsOwnershipDenial(t, body)
				candidate := cloneNativeObject(t, current)
				candidate["metadata"].(map[string]any)["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
				code, body = requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodPut, objectURL, candidate, nil)
				require.GreaterOrEqualf(t, code, 400, "initiation by UPDATE: %s", body)
				require.Equal(t, current, get(), "writer initiated termination")
				// Termination is seeded directly in isolated etcd. An authorized Apps
				// deletion path is still unavailable and is not claimed by this fixture.
				mutateStorage(func(o map[string]any) {
					m := o["metadata"].(map[string]any)
					m["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
					m["deletionGracePeriodSeconds"] = float64(0)
				})
				current = get()
				code, body = update(current, []string{unrelatedFinalizer})
				require.Equalf(t, 200, code, "exact finalizer remove on terminating object: %s", body)
				current = get()
				require.Equal(t, []any{unrelatedFinalizer}, current["metadata"].(map[string]any)["finalizers"])
				code, body = update(current, []string{unrelatedFinalizer, finalizer})
				require.GreaterOrEqualf(t, code, 400, "readd while terminating: %s", body)
				require.Equal(t, current, get(), "denied readd changed object")
				// Separate seeded last-finalizer state proves native implicit DELETE.
				mutateStorage(func(o map[string]any) { o["metadata"].(map[string]any)["finalizers"] = []string{finalizer} })
				current = get()
				code, body = update(current, []string{})
				require.Equalf(t, 200, code, "remove last finalizer: %s", body)
				code, body = requestJSON(t, ctx, client, tokens["customer"], http.MethodGet, objectURL, nil, nil)
				require.Equalf(t, 404, code, "last removal did not remove exact UID: %s", body)
				code, body = requestJSON(t, ctx, client, tokens["customer"], http.MethodPost, collection, resource.object(namespace, name, resource.version), nil)
				require.Equalf(t, 201, code, "recreate same name: %s", body)
				mutateStorage(func(o map[string]any) {
					o["metadata"].(map[string]any)["labels"] = map[string]any{appsownership.ProjectUIDLabel: projectUID, appsownership.AppUIDLabel: "replacement-app"}
				})
				replacement := get()
				require.NotEqual(t, oldUID, replacement["metadata"].(map[string]any)["uid"])
				code, body = update(current, []string{})
				require.GreaterOrEqualf(t, code, 400, "stale UID updated replacement: %s", body)
				require.Equal(t, replacement, get(), "stale cleanup changed replacement")
				// A foreign Project marker cannot use this routed Project's authority.
				mutateStorage(func(o map[string]any) {
					o["metadata"].(map[string]any)["labels"].(map[string]any)[appsownership.ProjectUIDLabel] = "foreign-project-uid"
				})
				foreign := get()
				code, body = update(foreign, []string{finalizer})
				require.Equalf(t, 403, code, "foreign project marker: %s", body)
				requireAppsOwnershipDenial(t, body)
				require.Equal(t, foreign, get())
			})
		}
	}
	// Restore the chosen replacement's correct Project marker before changing
	// the authoritative Project UID; the subsequent denial must test name reuse,
	// not merely reuse the preceding foreign-marker negative fixture.
	nativeKey, nativeValue, nativeRevision := findStoredNativeObject(t, ctx, endpoint, project, namespace, "workload-put", "Workload")
	var native map[string]any
	require.NoError(t, json.Unmarshal(nativeValue, &native))
	native["metadata"].(map[string]any)["labels"].(map[string]any)[appsownership.ProjectUIDLabel] = projectUID
	nativeRaw, err := json.Marshal(native)
	require.NoError(t, err)
	seedConfigMapValue(t, ctx, endpoint, nativeKey, nativeRevision, nativeRaw)
	// Rebinding the authoritative Project name also invalidates the exception.
	key, value, revision := findStatusRootProject(t, ctx, endpoint, project)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(value, &stored))
	stored["metadata"].(map[string]any)["uid"] = "replacement-project-uid"
	raw, err := json.Marshal(stored)
	require.NoError(t, err)
	seedConfigMapValue(t, ctx, endpoint, key, revision, raw)
	target := base + "/apis/compute.datumapis.com/v1alpha/namespaces/" + namespace + "/workloads/workload-put"
	code, body = requestJSON(t, ctx, client, tokens["apps-controller"], http.MethodPatch, target, map[string]any{"metadata": map[string]any{"finalizers": []string{nativeWorkloadFinalizer}}}, map[string]string{"Content-Type": "application/merge-patch+json"})
	require.Equalf(t, 403, code, "recreated Project: %s", body)
}

func cloneNativeObject(t *testing.T, object map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(object)
	require.NoError(t, err)
	var copy map[string]any
	require.NoError(t, json.Unmarshal(raw, &copy))
	return copy
}
