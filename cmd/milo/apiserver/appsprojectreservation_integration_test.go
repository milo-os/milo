package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Test-only raw storage CAS stands in for a future reservation protocol. The
// served API grants no acquisition/release privilege; no Apps drain is claimed.
func TestAppsProjectReservationHTTPIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("starts two real Milo replicas and etcd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	binary, err := findEtcdBinary()
	require.NoError(t, err)
	etcd := &envtest.Etcd{Path: binary, StartTimeout: 20 * time.Second, StopTimeout: 10 * time.Second}
	require.NoError(t, etcd.Start())
	t.Cleanup(func() { require.NoError(t, etcd.Stop()) })
	proxyURL, proxy := startFenceProxy(t, etcd.URL.Host)
	config := appsownership.Configuration{APIVersion: "apiserver.miloapis.com/v1alpha1", Kind: "AppsOwnershipConfiguration"}
	rootA, clientA, tokensA, _ := startNativeProbeReplica(t, ctx, proxyURL, config)
	rootB, clientB, tokensB, _ := startNativeProbeReplica(t, ctx, etcd.URL.String(), config)
	collectionA := rootA + "/apis/resourcemanager.miloapis.com/v1alpha1/projects"
	collectionB := rootB + "/apis/resourcemanager.miloapis.com/v1alpha1/projects"
	pollProjectDiscovery(t, ctx, clientA, tokensA["customer"], rootA+"/apis/resourcemanager.miloapis.com/v1alpha1", "projects", "Project")
	pollProjectDiscovery(t, ctx, clientB, tokensB["customer"], rootB+"/apis/resourcemanager.miloapis.com/v1alpha1", "projects", "Project")
	project := func(name string) map[string]any {
		return map[string]any{"apiVersion": "resourcemanager.miloapis.com/v1alpha1", "kind": "Project", "metadata": map[string]any{"name": name, "labels": map[string]any{"reservation-probe": name}, "finalizers": []string{"test.example/hold"}}, "spec": map[string]any{"ownerRef": map[string]any{"kind": "Organization", "name": "probe"}}}
	}
	create := func(t *testing.T, name string) map[string]any {
		code, body := requestJSON(t, ctx, clientB, tokensB["customer"], http.MethodPost, collectionB, project(name), nil)
		require.Equalf(t, 201, code, "%s", body)
		var obj map[string]any
		require.NoError(t, json.Unmarshal(body, &obj))
		return obj
	}
	get := func(t *testing.T, name string) map[string]any {
		code, body := requestJSON(t, ctx, clientB, tokensB["customer"], http.MethodGet, collectionB+"/"+name, nil, nil)
		require.Equalf(t, 200, code, "%s", body)
		var obj map[string]any
		require.NoError(t, json.Unmarshal(body, &obj))
		return obj
	}
	deny := func(t *testing.T, name, method, url string, payload any, headers map[string]string) {
		before := get(t, name)
		code, body := requestJSON(t, ctx, clientA, tokensA["system-admin"], method, url, payload, headers)
		require.Equalf(t, http.StatusForbidden, code, "%s %s: %s", method, url, body)
		require.Contains(t, string(body), "project reservation")
		require.Equal(t, before, get(t, name), "denied operation must preserve the complete stored Project")
	}
	merge := map[string]string{"Content-Type": "application/merge-patch+json"}
	for _, field := range []string{"labels", "annotations"} {
		for _, value := range []string{"", "hold-uid"} {
			suffix := field + "-value"
			if value == "" {
				suffix = field + "-empty"
			}
			t.Run(suffix, func(t *testing.T) {
				forged := project("forged-" + suffix)
				forged["metadata"].(map[string]any)[field] = map[string]any{appsownership.ProjectReservationKey: value}
				code, body := requestJSON(t, ctx, clientA, tokensA["system-admin"], http.MethodPost, collectionA, forged, nil)
				require.Equalf(t, 403, code, "%s", body)
				require.Contains(t, string(body), "project reservation")
				code, _ = requestJSON(t, ctx, clientB, tokensB["customer"], http.MethodGet, collectionB+"/forged-"+suffix, nil, nil)
				require.Equal(t, 404, code)
				name := "held-" + suffix
				create(t, name)
				deny(t, name, http.MethodPatch, collectionA+"/"+name, map[string]any{"metadata": map[string]any{field: map[string]any{appsownership.ProjectReservationKey: value}}}, merge)
				key, raw, revision := findStatusRootProject(t, ctx, etcd.URL.Host, name)
				held := reservationFixtureValue(t, raw, field, value, false)
				seedConfigMapValue(t, ctx, etcd.URL.Host, key, revision, held)
				current := get(t, name)
				url := collectionA + "/" + name
				stripped := cloneNativeObject(t, current)
				delete(stripped["metadata"].(map[string]any)[field].(map[string]any), appsownership.ProjectReservationKey)
				deny(t, name, http.MethodPut, url, stripped, nil)
				deny(t, name, http.MethodPatch, url, map[string]any{"metadata": map[string]any{field: map[string]any{appsownership.ProjectReservationKey: nil}}}, merge)
				deny(t, name, http.MethodPatch, url, map[string]any{"metadata": map[string]any{"annotations": map[string]any{"unrelated": "change"}}}, merge)
				deny(t, name, http.MethodPatch, url+"?fieldManager=reservation-probe&force=true", project(name), map[string]string{"Content-Type": "application/apply-patch+yaml"})
				status := cloneNativeObject(t, current)
				status["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True", "reason": "Fixture", "message": "test", "lastTransitionTime": "2026-01-01T00:00:00Z"}}}
				deny(t, name, http.MethodPut, url+"/status", status, nil)
				deny(t, name, http.MethodPatch, url+"/status", map[string]any{"status": status["status"]}, merge)
				finalizer := cloneNativeObject(t, current)
				finalizer["metadata"].(map[string]any)["finalizers"] = []any{}
				deny(t, name, http.MethodPut, url, finalizer, nil)
				deny(t, name, http.MethodDelete, url, map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]any{"uid": current["metadata"].(map[string]any)["uid"]}}, nil)
				deny(t, name, http.MethodDelete, collectionA+"?labelSelector=reservation-probe%3D"+name, nil, nil)
				// A reserved terminating fixture cannot escape through implicit deletion
				// when its last finalizer is removed. Termination is not API-authorized here.
				key, raw, revision = findStatusRootProject(t, ctx, etcd.URL.Host, name)
				seedConfigMapValue(t, ctx, etcd.URL.Host, key, revision, reservationFixtureValue(t, raw, field, value, true))
				terminating := get(t, name)
				terminating["metadata"].(map[string]any)["finalizers"] = []any{}
				deny(t, name, http.MethodPut, url, terminating, nil)
			})
		}
	}
	t.Run("ordinary unmarked Project", func(t *testing.T) {
		name := "ordinary-project"
		create(t, name)
		code, body := requestJSON(t, ctx, clientA, tokensA["customer"], http.MethodPatch, collectionA+"/"+name, map[string]any{"metadata": map[string]any{"annotations": map[string]any{"ordinary": "allowed"}}}, merge)
		require.Equalf(t, 200, code, "%s", body)
		code, body = requestJSON(t, ctx, clientA, tokensA["customer"], http.MethodPatch, collectionA+"/"+name+"/status", map[string]any{"status": map[string]any{}}, merge)
		require.Equalf(t, 200, code, "%s", body)
		code, body = requestJSON(t, ctx, clientA, tokensA["customer"], http.MethodPatch, collectionA+"/"+name, map[string]any{"metadata": map[string]any{"finalizers": []any{}}}, merge)
		require.Equalf(t, 200, code, "%s", body)
		code, body = requestJSON(t, ctx, clientA, tokensA["customer"], http.MethodDelete, collectionA+"/"+name, nil, nil)
		require.Equalf(t, 200, code, "%s", body)
		code, _ = requestJSON(t, ctx, clientB, tokensB["customer"], http.MethodGet, collectionB+"/"+name, nil, nil)
		require.Equal(t, 404, code)
	})
	t.Run("reservation CAS wins after DELETE validation", func(t *testing.T) {
		name := "reservation-wins"
		initial := create(t, name)
		key, raw, revision := findStatusRootProject(t, ctx, etcd.URL.Host, name)
		pause := proxy.arm(key)
		released := false
		t.Cleanup(func() {
			if !released {
				close(pause.resume)
			}
		})
		type response struct {
			code int
			body []byte
		}
		done := make(chan response, 1)
		go func() {
			c, b := requestJSON(t, ctx, clientA, tokensA["system-admin"], http.MethodDelete, collectionA+"/"+name, map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]any{"uid": initial["metadata"].(map[string]any)["uid"]}}, nil)
			done <- response{c, b}
		}()
		select {
		case <-pause.arrived:
		case result := <-done:
			t.Fatalf("DELETE did not reach storage: %d %s", result.code, result.body)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		seedConfigMapValue(t, ctx, etcd.URL.Host, key, revision, reservationFixtureValue(t, raw, "annotations", "race-hold", false))
		reserved := get(t, name)
		close(pause.resume)
		released = true
		select {
		case result := <-done:
			require.Equalf(t, 403, result.code, "%s", result.body)
			require.Contains(t, string(result.body), "project reservation")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		require.Equal(t, reserved, get(t, name))
		require.Nil(t, get(t, name)["metadata"].(map[string]any)["deletionTimestamp"])
	})
	t.Run("DELETE wins before reservation CAS and name reuse", func(t *testing.T) {
		name := "deletion-wins"
		initial := create(t, name)
		oldUID := initial["metadata"].(map[string]any)["uid"]
		key, raw, revision := findStatusRootProject(t, ctx, etcd.URL.Host, name)
		held := reservationFixtureValue(t, raw, "annotations", "late-hold", false)
		code, body := requestJSON(t, ctx, clientB, tokensB["customer"], http.MethodDelete, collectionB+"/"+name, nil, nil)
		require.Equalf(t, 200, code, "%s", body)
		kv := newEtcdClient(t, etcd.URL.Host)
		result, err := kv.Txn(ctx).If(clientv3.Compare(clientv3.ModRevision(key), "=", revision)).Then(clientv3.OpPut(key, string(held))).Commit()
		require.NoError(t, err)
		require.False(t, result.Succeeded)
		terminating := get(t, name)
		require.NotNil(t, terminating["metadata"].(map[string]any)["deletionTimestamp"])
		deny(t, name, http.MethodPatch, collectionA+"/"+name, map[string]any{"metadata": map[string]any{"annotations": map[string]any{appsownership.ProjectReservationKey: "late-hold"}}}, merge)
		// Finish the ordinary unmarked test Project, then prove stale operations
		// cannot acquire or delete its same-name replacement.
		code, body = requestJSON(t, ctx, clientB, tokensB["customer"], http.MethodPatch, collectionB+"/"+name, map[string]any{"metadata": map[string]any{"finalizers": []any{}}}, merge)
		require.Equalf(t, 200, code, "%s", body)
		replacement := create(t, name)
		require.NotEqual(t, oldUID, replacement["metadata"].(map[string]any)["uid"])
		result, err = kv.Txn(ctx).If(clientv3.Compare(clientv3.ModRevision(key), "=", revision)).Then(clientv3.OpPut(key, string(held))).Commit()
		require.NoError(t, err)
		require.False(t, result.Succeeded)
		code, body = requestJSON(t, ctx, clientA, tokensA["system-admin"], http.MethodDelete, collectionA+"/"+name, map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]any{"uid": oldUID, "resourceVersion": initial["metadata"].(map[string]any)["resourceVersion"]}}, nil)
		require.Equalf(t, 409, code, "%s", body)
		require.Equal(t, replacement, get(t, name))
	})
}

func reservationFixtureValue(t *testing.T, raw []byte, field, value string, terminating bool) []byte {
	t.Helper()
	var object map[string]any
	require.NoError(t, json.Unmarshal(raw, &object))
	meta := object["metadata"].(map[string]any)
	values, _ := meta[field].(map[string]any)
	if values == nil {
		values = map[string]any{}
	}
	values[appsownership.ProjectReservationKey] = value
	meta[field] = values
	if terminating {
		meta["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
		meta["deletionGracePeriodSeconds"] = 0
	}
	encoded, err := json.Marshal(object)
	require.NoError(t, err)
	return encoded
}
