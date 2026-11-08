package app

import (
	"context"
	"fmt"

	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	restclient "k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	discoveryctx "go.miloapis.com/milo/pkg/server/discovery"
)

// startServedTypes builds the discovery-context registry from the root's CRDs and DiscoveryContextPolicies, as the
// API server builds its own, so the garbage collector reads which types a project serves from the same declarations
// (milo#834). It is built whatever the API server's DiscoveryContextFilter gate says.
func startServedTypes(ctx context.Context, cfg *restclient.Config) (*discoveryctx.Registry, error) {
	client, err := apiextensionsclient.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("served types: apiextensions client: %w", err)
	}
	factory := apiextensionsinformers.NewSharedInformerFactory(client, 0)
	// The informer must exist before the factory starts, or Start runs nothing.
	factory.Apiextensions().V1().CustomResourceDefinitions().Informer()
	registry := discoveryctx.NewRegistry()
	logger := klog.FromContext(ctx)
	go func() {
		if err := registry.Run(ctx, factory); err != nil {
			logger.Error(err, "Served types: the CRD informer stopped")
		}
	}()
	factory.Start(ctx.Done())
	go func() {
		if err := registry.RunPolicyInformer(ctx, cfg); err != nil {
			logger.Error(err, "Served types: the DiscoveryContextPolicy informer stopped")
		}
	}()
	return registry, nil
}
