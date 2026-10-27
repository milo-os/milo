package app

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/fake"
	restclient "k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/klog/v2"
)

func podNamed(name string, annotations map[string]string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "milo-system", Annotations: annotations}}
}

func deletionCost(t *testing.T, client *fake.Clientset, name string) (string, bool) {
	t.Helper()
	pod, err := client.CoreV1().Pods("milo-system").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get %s: %v", name, err)
	}
	cost, ok := pod.Annotations[corev1.PodDeletionCost]
	return cost, ok
}

func patches(client *fake.Clientset) int {
	n := 0
	for _, action := range client.Actions() {
		if action.GetVerb() == "patch" && action.GetResource().Resource == "pods" {
			n++
		}
	}
	return n
}

func TestLeaderPodMarkSetsTheCostOnItsOwnPodOnly(t *testing.T) {
	client := fake.NewSimpleClientset(podNamed("leader", nil), podNamed("standby", nil))
	marker := newLeaderPodMarker(client.CoreV1().Pods("milo-system"), "leader")

	if err := marker.mark(context.Background()); err != nil {
		t.Fatalf("mark: %v", err)
	}

	if cost, _ := deletionCost(t, client, "leader"); cost != leaderPodDeletionCost {
		t.Errorf("the leader's pod has cost %q, want %q", cost, leaderPodDeletionCost)
	}
	if cost, ok := deletionCost(t, client, "standby"); ok {
		t.Errorf("the standby's pod was marked with %q", cost)
	}
}

func TestLeaderPodClearRemovesAStaleMark(t *testing.T) {
	stale := map[string]string{corev1.PodDeletionCost: leaderPodDeletionCost, "keep": "me"}
	client := fake.NewSimpleClientset(podNamed("restarted", stale))
	marker := newLeaderPodMarker(client.CoreV1().Pods("milo-system"), "restarted")

	if err := marker.clear(context.Background()); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if cost, ok := deletionCost(t, client, "restarted"); ok {
		t.Errorf("the restarted pod still carries cost %q", cost)
	}
	pod, _ := client.CoreV1().Pods("milo-system").Get(context.Background(), "restarted", metav1.GetOptions{})
	if pod.Annotations["keep"] != "me" {
		t.Errorf("clear removed another annotation: %v", pod.Annotations)
	}
}

func TestLeaderPodMarkerIsOffWithoutThePodsName(t *testing.T) {
	inCluster := func() (*restclient.Config, error) { return &restclient.Config{Host: "https://example.invalid"}, nil }
	for name, env := range map[string]map[string]string{
		"no name":      {"POD_NAMESPACE": "milo-system"},
		"no namespace": {"POD_NAME": "leader"},
	} {
		marker, err := leaderPodMarkerFromEnv(func(key string) string { return env[key] }, inCluster)
		if err != nil || marker != nil {
			t.Errorf("%s: got marker %v and error %v, want neither", name, marker, err)
		}
	}

	var off *leaderPodMarker
	if err := off.mark(context.Background()); err != nil {
		t.Errorf("an off marker's mark returned %v", err)
	}
	if err := off.clear(context.Background()); err != nil {
		t.Errorf("an off marker's clear returned %v", err)
	}
}

func TestLeaderPodMarkerFromEnvTargetsTheNamedPod(t *testing.T) {
	env := map[string]string{"POD_NAME": "leader", "POD_NAMESPACE": "datum-system"}
	inCluster := func() (*restclient.Config, error) { return &restclient.Config{Host: "https://example.invalid"}, nil }

	marker, err := leaderPodMarkerFromEnv(func(key string) string { return env[key] }, inCluster)

	if err != nil || marker == nil || marker.name != "leader" {
		t.Fatalf("got marker %+v and error %v, want one for the pod leader", marker, err)
	}
}

func TestLeaderPodMarkingLeadershipRunsTheControllersWhenThePatchFails(t *testing.T) {
	client := fake.NewSimpleClientset(podNamed("leader", nil))
	client.PrependReactor("patch", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	marker := newLeaderPodMarker(client.CoreV1().Pods("milo-system"), "leader")
	started := make(chan struct{})

	markWhileLeading(marker, klog.Background(), func(context.Context) { close(started) })(context.Background())

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the controllers did not start after the mark failed")
	}
}

func TestLeaderPodMarkingLeadershipPatchesOncePerStart(t *testing.T) {
	client := fake.NewSimpleClientset(podNamed("leader", nil))
	marker := newLeaderPodMarker(client.CoreV1().Pods("milo-system"), "leader")

	markWhileLeading(marker, klog.Background(), func(context.Context) {})(context.Background())

	err := wait.PollUntilContextTimeout(context.Background(), 10*time.Millisecond, 5*time.Second, true,
		func(context.Context) (bool, error) {
			cost, _ := deletionCost(t, client, "leader")
			return cost == leaderPodDeletionCost, nil
		})
	if err != nil {
		t.Fatalf("the leader's pod was not marked: %v", err)
	}
	if n := patches(client); n != 1 {
		t.Errorf("%d patches for one leadership start, want 1", n)
	}
}
