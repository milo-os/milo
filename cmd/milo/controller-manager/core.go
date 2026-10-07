package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/pflag"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	restclient "k8s.io/client-go/rest"
	"k8s.io/controller-manager/controller"
	"k8s.io/kubernetes/cmd/kube-controller-manager/names"

	gccontroller "go.miloapis.com/milo/internal/controllers/garbagecollector"
	namespacecontroller "go.miloapis.com/milo/internal/controllers/namespace"

	"go.miloapis.com/milo/internal/controllers/projectprovider"
)

func newNamespaceControllerDescriptor() *ControllerDescriptor {
	return &ControllerDescriptor{
		name:     names.NamespaceController,
		aliases:  []string{"namespace"},
		initFunc: startNamespaceController,
	}
}

func startNamespaceController(ctx context.Context, controllerContext ControllerContext, controllerName string) (controller.Interface, bool, error) {
	// the namespace cleanup controller is very chatty.  It makes lots of discovery calls and then it makes lots of delete calls
	// the ratelimiter negatively affects its speed.  Deleting 100 total items in a namespace (that's only a few of each resource
	// including events), takes ~10 seconds by default.
	nsKubeconfig := controllerContext.ClientBuilder.ConfigOrDie("namespace-controller")
	nsKubeconfig.QPS *= 20
	nsKubeconfig.Burst *= 100
	namespaceKubeClient := clientset.NewForConfigOrDie(nsKubeconfig)
	return startModifiedNamespaceController(ctx, controllerContext, namespaceKubeClient, nsKubeconfig)
}

func startModifiedNamespaceController(ctx context.Context, controllerContext ControllerContext, namespaceKubeClient clientset.Interface, nsKubeconfig *restclient.Config) (controller.Interface, bool, error) {
	metadataClient, err := metadata.NewForConfig(nsKubeconfig)
	if err != nil {
		return nil, true, err
	}

	discoverResourcesFn := namespaceKubeClient.Discovery().ServerPreferredNamespacedResources

	// ROOT: same as before (this registers the "root" cluster in your forked controller)
	namespaceController := namespacecontroller.NewNamespaceController(
		ctx,
		namespaceKubeClient,
		metadataClient,
		discoverResourcesFn,
		controllerContext.InformerFactory.Core().V1().Namespaces(),
		controllerContext.ComponentConfig.NamespaceController.NamespaceSyncPeriod.Duration,
		v1.FinalizerKubernetes,
	)
	go namespaceController.Run(ctx, int(controllerContext.ComponentConfig.NamespaceController.ConcurrentNamespaceSyncs))

	sink := &namespacecontroller.NMSink{
		NM:        namespaceController,
		Resync:    controllerContext.ComponentConfig.NamespaceController.NamespaceSyncPeriod.Duration,
		Finalizer: v1.FinalizerKubernetes,
	}

	prov, err := projectprovider.New(nsKubeconfig, sink)
	if err != nil {
		return nil, true, err
	}
	go prov.Run(ctx)

	// nothing to return to kube-controller-manager; controller runs in goroutines
	return nil, true, nil
}

func newGarbageCollectorControllerDescriptor() *ControllerDescriptor {
	return &ControllerDescriptor{
		name:     names.GarbageCollectorController,
		aliases:  []string{"garbagecollector"},
		initFunc: startGarbageCollectorController,
	}
}

func startGarbageCollectorController(ctx context.Context, controllerContext ControllerContext, controllerName string) (controller.Interface, bool, error) {
	if !controllerContext.ComponentConfig.GarbageCollectorController.EnableGarbageCollector {
		return nil, false, nil
	}

	gcClientset := controllerContext.ClientBuilder.ClientOrDie("generic-garbage-collector")
	discoveryClient := controllerContext.ClientBuilder.DiscoveryClientOrDie("generic-garbage-collector")

	cfg := controllerContext.ClientBuilder.ConfigOrDie("generic-garbage-collector")
	// each deletion ~ two API calls
	cfg.QPS *= 2

	metadataClient, err := metadata.NewForConfig(cfg)
	if err != nil {
		return nil, true, err
	}

	ignored := make(map[schema.GroupResource]struct{})
	for _, r := range controllerContext.ComponentConfig.GarbageCollectorController.GCIgnoredResources {
		ignored[schema.GroupResource{Group: r.Group, Resource: r.Resource}] = struct{}{}
	}

	// Build GC with a root graph builder (constructed inside), using the composite informer factory and informersStarted.
	gc, err := gccontroller.NewGarbageCollector(
		ctx,
		gcClientset,
		metadataClient,
		controllerContext.RESTMapper, // reuse the same mapper across partitions
		ignored,
		controllerContext.ObjectOrMetadataInformerFactory, // composite (core + metadata) factory
		controllerContext.InformersStarted,
	)
	if err != nil {
		return nil, true, fmt.Errorf("failed to start the generic garbage collector: %w", err)
	}

	// Start GC workers
	workers := int(controllerContext.ComponentConfig.GarbageCollectorController.ConcurrentGCSyncs)
	const syncPeriod = 30 * time.Second
	go gc.Run(ctx, workers, syncPeriod)

	// Periodic discovery sync for the root partition
	go gc.Sync(ctx, discoveryClient, 30*time.Second)

	// Hook dynamic projects into GC via a sink
	gcSink := &gccontroller.GCSink{
		GC:                gc,
		RootRESTMapper:    controllerContext.RESTMapper, // same API surface across projects
		Ignored:           projectIgnoredResources(ignored, projectGCIgnoredResources),
		InformersStarted:  controllerContext.InformersStarted,
		InitialSyncPeriod: 30 * time.Second,
	}
	prov, err := projectprovider.New(cfg, gcSink)
	if err != nil {
		return nil, true, fmt.Errorf("failed to start project provider for GC: %w", err)
	}
	go prov.Run(ctx)

	return gc, true, nil
}

var projectGCIgnoredResources = []string{
	"flowschemas.flowcontrol.apiserver.k8s.io",
	"prioritylevelconfigurations.flowcontrol.apiserver.k8s.io",
}

func addProjectGarbageCollectorFlags(fs *pflag.FlagSet) {
	fs.StringSliceVar(&projectGCIgnoredResources, "project-gc-ignored-resources", projectGCIgnoredResources,
		"Resources, as resource.group (core resources as resource), that the garbage collector does not watch in project control planes. "+
			"Each entry saves one watch per project. Only list resources whose objects in a project control plane never carry ownerReferences and are never named as owners.")
}

func projectIgnoredResources(base map[schema.GroupResource]struct{}, extra []string) map[schema.GroupResource]struct{} {
	out := make(map[schema.GroupResource]struct{}, len(base)+len(extra))
	for gr := range base {
		out[gr] = struct{}{}
	}
	for _, entry := range extra {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		out[schema.ParseGroupResource(entry)] = struct{}{}
	}
	return out
}
