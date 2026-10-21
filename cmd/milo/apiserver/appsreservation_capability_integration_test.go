package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestProjectReservationCapabilityHTTPIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("starts real etcd and two Milo API replicas")
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
	rootA, clientA, tokensA, capA := startReservationCapabilityReplica(t, ctx, proxyURL, config)
	rootB, clientB, tokensB, capB := startReservationCapabilityReplica(t, ctx, etcd.URL.String(), config)
	a := rootA + "/apis/resourcemanager.miloapis.com/v1alpha1/projects"
	b := rootB + "/apis/resourcemanager.miloapis.com/v1alpha1/projects"
	pollProjectDiscovery(t, ctx, clientA, tokensA["customer"], rootA+"/apis/resourcemanager.miloapis.com/v1alpha1", "projects", "Project")
	pollProjectDiscovery(t, ctx, clientB, tokensB["customer"], rootB+"/apis/resourcemanager.miloapis.com/v1alpha1", "projects", "Project")
	create := func(name string) map[string]any {
		code, body := requestJSON(t, ctx, clientB, tokensB["customer"], http.MethodPost, b, map[string]any{"apiVersion": "resourcemanager.miloapis.com/v1alpha1", "kind": "Project", "metadata": map[string]any{"name": name, "finalizers": []string{"test.example/hold"}, "annotations": map[string]any{"keep": "unchanged"}, "labels": map[string]any{"probe": name}}, "spec": map[string]any{"ownerRef": map[string]any{"kind": "Organization", "name": "probe"}}}, nil)
		require.Equalf(t, 201, code, "%s", body)
		var obj map[string]any
		require.NoError(t, json.Unmarshal(body, &obj))
		return obj
	}
	get := func(name string) map[string]any {
		code, body := requestJSON(t, ctx, clientB, tokensB["customer"], http.MethodGet, b+"/"+name, nil, nil)
		require.Equalf(t, 200, code, "%s", body)
		var obj map[string]any
		require.NoError(t, json.Unmarshal(body, &obj))
		return obj
	}
	request := func(obj map[string]any) reservationProbeRequest {
		meta := obj["metadata"].(map[string]any)
		return reservationProbeRequest{Operation: "acquire", Name: meta["name"].(string), UID: meta["uid"].(string), RV: meta["resourceVersion"].(string), Reservation: uuid.NewString()}
	}
	merge := map[string]string{"Content-Type": "application/merge-patch+json"}
	t.Run("acquisition changes only annotation; lost response recovers without mutation", func(t *testing.T) {
		initial := create("capability-held")
		req := request(initial)
		req.LoseReply = true
		reply := capB(req)
		require.Equal(t, "test caller lost successful reply", reply.Error)
		held := get(req.Name)
		expected := cloneNativeObject(t, initial)
		expected["metadata"].(map[string]any)["resourceVersion"] = held["metadata"].(map[string]any)["resourceVersion"]
		expected["metadata"].(map[string]any)["annotations"].(map[string]any)[appsownership.ProjectReservationKey] = req.Reservation
		require.Equal(t, expected, held, "internal operation changes exactly the reservation annotation plus storage RV")
		recovery := req
		recovery.Operation = "read"
		recovery.LoseReply = false
		recovered := capA(recovery)
		require.Empty(t, recovered.Error)
		require.Equal(t, req.Reservation, recovered.Held.ReservationUID)
		require.Equal(t, req.UID, recovered.Held.ProjectUID)
		require.NotEmpty(t, capB(req).Error, "stale acquire is not a replay or replacement")
		req.RV = recovered.Held.ResourceVersion
		req.LoseReply = false
		require.NotEmpty(t, capA(req).Error, "even fresh RV cannot adopt an existing hold")
		for _, mode := range []string{"uid", "name", "reservation"} {
			bad := recovery
			switch mode {
			case "uid":
				bad.UID = "replacement"
			case "name":
				bad.Name = "absent"
			case "reservation":
				bad.Reservation = uuid.NewString()
			}
			require.NotEmpty(t, capA(bad).Error)
		}
		require.Equal(t, held, get(req.Name))
		// Every externally authorized identity still hits the mandatory reservation guard.
		for _, operation := range []struct {
			method, url string
			body        any
			headers     map[string]string
		}{
			{http.MethodDelete, a + "/" + req.Name, nil, nil},
			{http.MethodDelete, a + "?labelSelector=probe%3D" + req.Name, nil, nil},
			{http.MethodPatch, a + "/" + req.Name, map[string]any{"metadata": map[string]any{"annotations": map[string]any{appsownership.ProjectReservationKey: nil}}}, merge},
			{http.MethodPatch, a + "/" + req.Name, map[string]any{"metadata": map[string]any{"finalizers": []any{}}}, merge},
			{http.MethodPatch, a + "/" + req.Name + "/status", map[string]any{"status": map[string]any{}}, merge},
		} {
			code, body := requestJSON(t, ctx, clientA, tokensA["system-admin"], operation.method, operation.url, operation.body, operation.headers)
			require.Equalf(t, 403, code, "%s", body)
			require.Equal(t, held, get(req.Name))
		}
	})
	t.Run("invalid bindings cannot write ordinary Project", func(t *testing.T) {
		original := create("capability-invalid")
		req := request(original)
		for _, mode := range []string{"uid", "rv", "name", "reservation", "path"} {
			bad := req
			switch mode {
			case "uid":
				bad.UID = "replacement"
			case "rv":
				bad.RV = "1"
			case "name":
				bad.Name = "absent"
			case "reservation":
				bad.Reservation = "untrusted"
			case "path":
				bad.Name = "../capability-invalid"
			}
			require.NotEmpty(t, capA(bad).Error)
			require.Equal(t, original, get(req.Name))
		}
		code, body := requestJSON(t, ctx, clientA, tokensA["system-admin"], http.MethodPatch, a+"/"+req.Name, map[string]any{"metadata": map[string]any{"annotations": map[string]any{appsownership.ProjectReservationKey: req.Reservation}}}, merge)
		require.Equalf(t, 403, code, "%s", body)
		require.Equal(t, original, get(req.Name))
	})
	for _, deleteMode := range []string{"deletion-timestamp", "physical-delete"} {
		t.Run("acquisition wins before "+deleteMode, func(t *testing.T) {
			original := create("capability-acquire-wins-" + deleteMode)
			if deleteMode == "physical-delete" {
				code, body := requestJSON(t, ctx, clientB, tokensB["customer"], http.MethodPatch, b+"/"+original["metadata"].(map[string]any)["name"].(string), map[string]any{"metadata": map[string]any{"finalizers": []any{}}}, merge)
				require.Equalf(t, 200, code, "%s", body)
				require.NoError(t, json.Unmarshal(body, &original))
			}
			req := request(original)
			key, _, _ := findStatusRootProject(t, ctx, etcd.URL.Host, req.Name)
			pause := proxy.arm(key)
			defer func() {
				select {
				case <-pause.resume:
				default:
					close(pause.resume)
				}
			}()
			done := make(chan int, 1)
			go func() {
				code, _ := requestJSON(t, ctx, clientA, tokensA["system-admin"], http.MethodDelete, a+"/"+req.Name, map[string]any{"preconditions": map[string]any{"uid": req.UID}}, nil)
				done <- code
			}()
			select {
			case <-pause.arrived:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.Empty(t, capB(req).Error)
			held := get(req.Name)
			close(pause.resume)
			select {
			case code := <-done:
				require.Equal(t, 403, code)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.Equal(t, held, get(req.Name))
			require.Nil(t, held["metadata"].(map[string]any)["deletionTimestamp"])
		})
	}

	t.Run("DELETE wins while acquisition is paused; replacement UID remains safe", func(t *testing.T) {
		original := create("capability-delete-wins")
		req := request(original)
		key, _, _ := findStatusRootProject(t, ctx, etcd.URL.Host, req.Name)
		pause := proxy.arm(key)
		defer func() {
			select {
			case <-pause.resume:
			default:
				close(pause.resume)
			}
		}()
		done := make(chan reservationProbeReply, 1)
		go func() { done <- capA(req) }()
		select {
		case <-pause.arrived:
		case result := <-done:
			t.Fatalf("acquire failed before real txn: %+v", result)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		code, body := requestJSON(t, ctx, clientB, tokensB["customer"], http.MethodDelete, b+"/"+req.Name, nil, nil)
		require.Equalf(t, 200, code, "%s", body)
		terminating := get(req.Name)
		close(pause.resume)
		select {
		case result := <-done:
			require.NotEmpty(t, result.Error)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		require.Equal(t, terminating, get(req.Name))
		fresh := req
		fresh.RV = terminating["metadata"].(map[string]any)["resourceVersion"].(string)
		require.NotEmpty(t, capB(fresh).Error, "current RV cannot acquire a terminating Project")
		code, body = requestJSON(t, ctx, clientB, tokensB["customer"], http.MethodPatch, b+"/"+req.Name, map[string]any{"metadata": map[string]any{"finalizers": []any{}}}, merge)
		require.Equalf(t, 200, code, "%s", body)
		replacement := create(req.Name)
		require.NotEqual(t, req.UID, replacement["metadata"].(map[string]any)["uid"])
		require.NotEmpty(t, capA(req).Error)
		fresh.RV = replacement["metadata"].(map[string]any)["resourceVersion"].(string)
		require.NotEmpty(t, capB(fresh).Error)
		recovery := req
		recovery.Operation = "read"
		require.NotEmpty(t, capB(recovery).Error)
		require.Equal(t, replacement, get(req.Name))
	})
	t.Run("duplicate and case-aliased metadata never normalizes into acquisition", func(t *testing.T) {
		for i, field := range []string{
			`"annotations":{"apps.miloapis.com/project-reservation":"first"},"annotations":{}`,
			`"annotations":{"apps.miloapis.com/project-reservation":"first","apps.miloapis.com/project-reservation":""}`,
			`"Annotations":{"apps.miloapis.com/project-reservation":"hold"}`,
			`"annotations":null`,
			`"labels":{},"labels":{},"annotations":{"keep":"unchanged"}`,
		} {
			name := "capability-ambiguous-" + string(rune('a'+i))
			original := create(name)
			req := request(original)
			key, raw, rev := findStatusRootProject(t, ctx, etcd.URL.Host, name)
			needle := []byte(`"annotations":{"keep":"unchanged"}`)
			require.Contains(t, string(raw), string(needle))
			encoded := bytes.Replace(raw, needle, []byte(field), 1)
			seedConfigMapValue(t, ctx, etcd.URL.Host, key, rev, encoded)
			_, before, currentRev := findStatusRootProject(t, ctx, etcd.URL.Host, name)
			req.RV = strconv.FormatInt(currentRev, 10)
			require.NotEmpty(t, capA(req).Error)
			req.Operation = "read"
			require.NotEmpty(t, capB(req).Error)
			_, after, afterRev := findStatusRootProject(t, ctx, etcd.URL.Host, name)
			require.Equal(t, before, after)
			require.Equal(t, currentRev, afterRev)
		}
	})

	t.Run("codec pruning and top-level metadata aliases cannot rewrite stored fields", func(t *testing.T) {
		for _, mode := range []string{"unknown-spec", "unknown-metadata", "metadata-alias"} {
			original := create("capability-normalize-" + mode)
			req := request(original)
			key, raw, rev := findStatusRootProject(t, ctx, etcd.URL.Host, req.Name)
			var obj map[string]any
			require.NoError(t, json.Unmarshal(raw, &obj))
			switch mode {
			case "unknown-spec":
				obj["spec"].(map[string]any)["futureField"] = map[string]any{"preserve": "exact"}
			case "unknown-metadata":
				obj["metadata"].(map[string]any)["futureField"] = "preserve"
			case "metadata-alias":
				obj["Metadata"] = map[string]any{"annotations": map[string]any{appsownership.ProjectReservationKey: req.Reservation}}
			}
			encoded, err := json.Marshal(obj)
			require.NoError(t, err)
			seedConfigMapValue(t, ctx, etcd.URL.Host, key, rev, encoded)
			_, before, currentRev := findStatusRootProject(t, ctx, etcd.URL.Host, req.Name)
			req.RV = strconv.FormatInt(currentRev, 10)
			require.NotEmpty(t, capA(req).Error)
			_, after, afterRev := findStatusRootProject(t, ctx, etcd.URL.Host, req.Name)
			require.Equal(t, before, after)
			require.Equal(t, currentRev, afterRev)
		}
	})

	t.Run("malformed marker fixtures cannot be acquired or repaired", func(t *testing.T) {
		for i, value := range []any{"", nil, int64(1), uuid.NewString()} {
			name := "capability-malformed-" + string(rune('a'+i))
			original := create(name)
			req := request(original)
			key, raw, rev := findStatusRootProject(t, ctx, etcd.URL.Host, name)
			var obj map[string]any
			require.NoError(t, json.Unmarshal(raw, &obj))
			obj["metadata"].(map[string]any)["annotations"].(map[string]any)[appsownership.ProjectReservationKey] = value
			encoded, err := json.Marshal(obj)
			require.NoError(t, err)
			seedConfigMapValue(t, ctx, etcd.URL.Host, key, rev, encoded)
			_, before, currentRev := findStatusRootProject(t, ctx, etcd.URL.Host, name)
			req.RV = strconv.FormatInt(currentRev, 10)
			require.NotEmpty(t, capA(req).Error)
			req.Operation = "read"
			require.NotEmpty(t, capB(req).Error)
			_, after, afterRev := findStatusRootProject(t, ctx, etcd.URL.Host, name)
			require.Equal(t, before, after)
			require.Equal(t, currentRev, afterRev)
		}
	})
}
