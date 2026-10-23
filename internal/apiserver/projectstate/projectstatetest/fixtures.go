// Package projectstatetest builds Projects and a running projectstate.Cache
// for tests of the cache and of its readers.
package projectstatetest

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	"go.miloapis.com/milo/internal/apiserver/projectstate"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// NewProject returns a Project whose Suspended condition is True or False.
func NewProject(name string, suspended bool, reasons ...resourcemanagerv1alpha1.ProjectSuspensionReason) *unstructured.Unstructured {
	status := map[string]interface{}{}

	if suspended {
		status["conditions"] = []interface{}{
			map[string]interface{}{
				"type":   resourcemanagerv1alpha1.ProjectSuspended,
				"status": string(metav1.ConditionTrue),
				"reason": resourcemanagerv1alpha1.ProjectSuspendedReason,
			},
		}

		suspensions := make([]interface{}, 0, len(reasons))
		for _, r := range reasons {
			suspensions = append(suspensions, map[string]interface{}{
				"reason":             string(r),
				"suspendedAt":        metav1.Now().Format(time.RFC3339),
				"reinstateAuthority": string(resourcemanagerv1alpha1.AuthorityOperator),
			})
		}
		status["suspensions"] = suspensions
	} else {
		status["conditions"] = []interface{}{
			map[string]interface{}{
				"type":   resourcemanagerv1alpha1.ProjectSuspended,
				"status": string(metav1.ConditionFalse),
				"reason": resourcemanagerv1alpha1.ProjectActiveReason,
			},
		}
	}

	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "resourcemanager.miloapis.com/v1alpha1",
			"kind":       "Project",
			"metadata": map[string]interface{}{
				"name": name,
			},
			"status": status,
		},
	}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "resourcemanager.miloapis.com",
		Version: "v1alpha1",
		Kind:    "Project",
	})
	return u
}

// NewClient returns a fake dynamic client that serves objects as Projects.
func NewClient(t *testing.T, objects ...runtime.Object) *fake.FakeDynamicClient {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := resourcemanagerv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	return fake.NewSimpleDynamicClient(scheme, objects...)
}

// NewCache starts a projectstate.Cache on a fake client holding objects,
// waits for its initial List, and stops it when the test ends.
func NewCache(t *testing.T, objects ...runtime.Object) (*projectstate.Cache, *fake.FakeDynamicClient) {
	t.Helper()
	client := NewClient(t, objects...)
	c := projectstate.NewCache(client)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	c.Start(stop)

	deadline := time.Now().Add(time.Second)
	for !c.HasSynced() {
		if time.Now().After(deadline) {
			t.Fatalf("project state cache did not sync")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return c, client
}
