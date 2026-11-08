// pkg/controllers/namespace/wiring.go
package namespace

import (
	"context"
	"time"

	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

type NMSink struct {
	NM        *NamespaceController
	Resync    time.Duration
	Finalizer v1.FinalizerName
	// InitialSyncPeriod bounds the wait for a project's namespace cache, as GCSink's bounds its monitors' sync.
	InitialSyncPeriod time.Duration
}

func (s *NMSink) AddProject(ctx context.Context, id string, cfg *rest.Config) error {
	cs := clientset.NewForConfigOrDie(cfg)
	md := metadata.NewForConfigOrDie(cfg)
	// log
	klog.Infof("[namespace] Adding project %q with config: %v", id, cfg)
	return s.NM.AddProject(ctx, id, cs, md, cs.Discovery().ServerPreferredNamespacedResources, s.Resync, s.Finalizer,
		s.InitialSyncPeriod)
}

func (s *NMSink) RemoveProject(id string) {
	klog.Infof("[namespace] Removing project %q", id)
	s.NM.RemoveProject(id)
}
