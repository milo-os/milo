// Package projectstatetest builds Projects and a running projectstate.Cache
// for tests of the cache and of its readers.
package projectstatetest

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/utils/clock"

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

// NewOwnedProject returns an active Project owned by the named Organization.
func NewOwnedProject(name, organization string) *unstructured.Unstructured {
	p := NewProject(name, false)
	p.Object["spec"] = map[string]interface{}{
		"ownerRef": map[string]interface{}{
			"kind": "Organization",
			"name": organization,
		},
	}
	return p
}

// NewProjectWithUID returns an active Project named name with the given UID.
func NewProjectWithUID(name string, uid types.UID) *unstructured.Unstructured {
	p := NewProject(name, false)
	p.SetUID(uid)
	return p
}

// StorageAnswers makes every live GET of a Project return obj, or err when obj
// is nil, and counts the GETs. A started cache keeps what its reflector
// listed, so storage and the cache's view can differ the way a lagging cache
// does.
func StorageAnswers(client *fake.FakeDynamicClient, obj runtime.Object, err error) *atomic.Int32 {
	var gets atomic.Int32
	client.PrependReactor("get", "projects", func(clienttesting.Action) (bool, runtime.Object, error) {
		gets.Add(1)
		return true, obj, err
	})
	return &gets
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
	return NewCacheWithClock(t, clock.RealClock{}, objects...)
}

// NewCacheWithClock is NewCache with the cache's force-live list on clk.
func NewCacheWithClock(t *testing.T, clk clock.Clock, objects ...runtime.Object) (*projectstate.Cache, *fake.FakeDynamicClient) {
	t.Helper()
	client := NewClient(t, objects...)
	c := projectstate.NewCacheWithClock(client, clk)
	Start(t, c, client)
	return c, client
}

// Start runs c, a cache on client, waits for its initial List and for its
// watch to open, and stops it when the test ends.
//
// The fake client sends an event only to the watches open at the time of the
// write, and it never sends a delete again. So a test that deletes a Project
// between the initial List and the watch would lose the delete. client-go's
// own fake-client example waits for the watch the same way
// (k8s.io/client-go v0.37.1, examples/fake-client/main_test.go).
func Start(t *testing.T, c *projectstate.Cache, client *fake.FakeDynamicClient) {
	t.Helper()
	watching := make(chan struct{})
	var opened sync.Once
	client.PrependWatchReactor(projectstate.ProjectGVR.Resource, func(action clienttesting.Action) (bool, watch.Interface, error) {
		var opts metav1.ListOptions
		if a, ok := action.(clienttesting.WatchActionImpl); ok {
			opts = a.ListOptions
		}
		w, err := client.Tracker().Watch(action.GetResource(), action.GetNamespace(), opts)
		if err != nil {
			return true, nil, err
		}
		opened.Do(func() { close(watching) })
		return true, w, nil
	})
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
	select {
	case <-watching:
	case <-time.After(time.Until(deadline)):
		t.Fatalf("project state cache did not open its watch")
	}
}
