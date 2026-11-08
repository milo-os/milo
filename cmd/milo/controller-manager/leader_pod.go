package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

// leaderPodDeletionCost is the leader pod's deletion cost. Any value above the
// standbys' default of 0 makes a rollout delete a standby's old pod first, so
// the lease moves once instead of twice.
const leaderPodDeletionCost = "1"

// leaderPodPatchTimeout bounds one patch, so a slow cluster cannot hold up
// the controllers or the campaign.
const leaderPodPatchTimeout = 10 * time.Second

// leaderPodMarker marks the pod this process runs in while it holds the lease.
// The mark is best effort, as the annotation itself is: a nil marker does
// nothing.
type leaderPodMarker struct {
	pods corev1client.PodInterface
	name string
}

func newLeaderPodMarker(pods corev1client.PodInterface, name string) *leaderPodMarker {
	return &leaderPodMarker{pods: pods, name: name}
}

// leaderPodMarkerFromEnv returns a marker for the pod POD_NAME in POD_NAMESPACE,
// reached through inCluster, or nil when either variable is unset.
func leaderPodMarkerFromEnv(getenv func(string) string, inCluster func() (*restclient.Config, error)) (*leaderPodMarker, error) {
	name, namespace := getenv("POD_NAME"), getenv("POD_NAMESPACE")
	if name == "" || namespace == "" {
		return nil, nil
	}
	config, err := inCluster()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config for the leader pod's mark: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("client for the leader pod's mark: %w", err)
	}
	return newLeaderPodMarker(client.CoreV1().Pods(namespace), name), nil
}

// startLeaderPodMarker returns the marker for this process's pod, after it
// clears a mark a previous process in the pod left, or nil when the pod's name
// is not set. The mark is best effort, so a failure is logged, not returned.
func startLeaderPodMarker(ctx context.Context, logger klog.Logger) *leaderPodMarker {
	marker, err := leaderPodMarkerFromEnv(os.Getenv, restclient.InClusterConfig)
	if err != nil {
		logger.Error(err, "The leader's pod will not be marked")
		return nil
	}
	return clearOnStart(ctx, logger, marker)
}

// clearOnStart clears the mark a previous process in this pod may have left,
// so a restarted standby does not keep the leader's cost, and returns marker.
func clearOnStart(ctx context.Context, logger klog.Logger, marker *leaderPodMarker) *leaderPodMarker {
	if err := marker.clear(ctx); err != nil {
		logger.Error(err, "Could not clear this pod's leader mark")
	}
	return marker
}

func (m *leaderPodMarker) mark(ctx context.Context) error {
	cost := leaderPodDeletionCost
	return m.set(ctx, &cost)
}

// clear removes the mark a previous process in this pod may have left.
func (m *leaderPodMarker) clear(ctx context.Context) error {
	return m.set(ctx, nil)
}

func (m *leaderPodMarker) set(ctx context.Context, cost *string) error {
	if m == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, leaderPodPatchTimeout)
	defer cancel()
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{"annotations": map[string]*string{corev1.PodDeletionCost: cost}},
	})
	if err != nil {
		return err
	}
	_, err = m.pods.Patch(ctx, m.name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// markWhileLeading returns an OnStartedLeading that marks the pod beside
// start, without waiting for the mark.
func markWhileLeading(m *leaderPodMarker, logger klog.Logger, start func(context.Context)) func(context.Context) {
	return func(ctx context.Context) {
		go func() {
			if err := m.mark(ctx); err != nil {
				logger.Error(err, "Could not mark the leader's pod; a rollout may move the lease twice")
			}
		}()
		start(ctx)
	}
}
