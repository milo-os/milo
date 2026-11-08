// Package gctest runs Milo's garbage collector against a test API server, so a test can prove what the collector
// deletes. It follows the setup of Kubernetes' own garbage collector integration tests
// (test/integration/garbagecollector/garbage_collector_test.go, setupWithServer).
package gctest

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/controller-manager/pkg/informerfactory"

	"go.miloapis.com/milo/internal/controllers/garbagecollector"
)

// Options sets how the collector runs. The zero value runs it as Kubernetes' controller manager does by default.
type Options struct {
	// Workers is the number of delete and orphan workers; 0 means 20, kube-controller-manager's --concurrent-gc-syncs.
	Workers int
	// SyncPeriod is how often the REST mapper is reset and discovery is read again; 0 means 5 s, as the upstream tests.
	SyncPeriod time.Duration
}

// Start runs the garbage collector against cfg until ctx is done. It returns once the collector is constructed; the
// collector's own first sync waits for its informers.
func Start(ctx context.Context, cfg *rest.Config, opts Options) error {
	workers, period := opts.Workers, opts.SyncPeriod
	if workers == 0 {
		workers = 20
	}
	if period == 0 {
		period = 5 * time.Second
	}
	clientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("garbage collector clientset: %w", err)
	}
	metadataClient, err := metadata.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("garbage collector metadata client: %w", err)
	}
	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(clientSet.Discovery()))
	restMapper.Reset()
	started := make(chan struct{})
	close(started)
	gc, err := garbagecollector.NewGarbageCollector(ctx, clientSet, metadataClient, restMapper,
		garbagecollector.DefaultIgnoredResources(),
		informerfactory.NewInformerFactory(informers.NewSharedInformerFactory(clientSet, 0),
			metadatainformer.NewSharedInformerFactory(metadataClient, 0)),
		started)
	if err != nil {
		return fmt.Errorf("garbage collector: %w", err)
	}
	go wait.Until(restMapper.Reset, period, ctx.Done())
	go gc.Run(ctx, workers, period)
	go gc.Sync(ctx, clientSet.Discovery(), period)
	return nil
}
