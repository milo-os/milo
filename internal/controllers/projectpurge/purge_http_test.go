package projectpurge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// Exercise real client transport/status decoding and recovery. This is a fault
// fixture, not evidence of admission or a running project control plane.
func TestStatusHTTPFailureRecovery(t *testing.T) {
	var failure atomic.Int32
	var remaining atomic.Bool
	remaining.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body any
		switch r.URL.Path {
		case "/api/v1/namespaces":
			if code := failure.Load(); code != 0 {
				w.WriteHeader(int(code))
				reason := metav1.StatusReasonServiceUnavailable
				if code == http.StatusNotFound {
					reason = metav1.StatusReasonNotFound
				}
				_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: metav1.StatusFailure, Reason: reason, Code: code})
				return
			}
			body = &corev1.NamespaceList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "NamespaceList"}, Items: []corev1.Namespace{{ObjectMeta: metav1.ObjectMeta{Name: "default"}}}}
		case "/api":
			body = &metav1.APIVersions{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "APIVersions"}, Versions: []string{"v1"}}
		case "/apis":
			body = &metav1.APIGroupList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "APIGroupList"}}
		case "/api/v1":
			body = &metav1.APIResourceList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "APIResourceList"}, GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "configmaps", Namespaced: true, Kind: "ConfigMap", Verbs: []string{"list", "deletecollection"}}}}
		case "/api/v1/namespaces/default/configmaps":
			list := &corev1.ConfigMapList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMapList"}}
			if remaining.Load() {
				list.Items = []corev1.ConfigMap{{ObjectMeta: metav1.ObjectMeta{Name: "still-owned", Namespace: "default", UID: "original-object", Finalizers: []string{"example.test/cleanup"}}}}
			}
			body = list
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer server.Close()
	cfg := &rest.Config{Host: server.URL}
	purger := New()
	for _, code := range []int32{http.StatusNotFound, http.StatusServiceUnavailable} {
		failure.Store(code)
		status, err := purger.Status(context.Background(), cfg, "project")
		if err == nil || status.Complete {
			t.Fatalf("HTTP %d incorrectly completed cleanup: %+v %v", code, status, err)
		}
	}
	failure.Store(0)
	status, err := purger.Status(context.Background(), cfg, "project")
	if err != nil || status.Complete || len(status.Blockers) != 1 || status.Blockers[0].Name != "still-owned" {
		t.Fatalf("recovered object must block cleanup: %+v %v", status, err)
	}
	remaining.Store(false)
	status, err = purger.Status(context.Background(), cfg, "project")
	if err != nil || !status.Complete {
		t.Fatalf("successful empty reads must complete: %+v %v", status, err)
	}
	server.Close()
	status, err = purger.Status(context.Background(), cfg, "project")
	if err == nil || status.Complete {
		t.Fatalf("closed listener incorrectly completed cleanup: %+v %v", status, err)
	}
}
