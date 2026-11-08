package projectprovider

import (
	"context"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/restmapper"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type Sink interface {
	AddProject(ctx context.Context, id string, cfg *rest.Config) error
	RemoveProject(id string)
}

type Provider struct {
	root       *rest.Config
	dyn        dynamic.Interface
	sink       Sink
	projectGVR schema.GroupVersionResource
}

func New(root *rest.Config, sink Sink) (*Provider, error) {
	dyn, err := dynamic.NewForConfig(root)
	if err != nil {
		return nil, err
	}
	gvr, err := resolveProjectGVR(root, "resourcemanager.miloapis.com", "v1alpha1")
	if err != nil {
		return nil, err
	}
	return &Provider{root: root, dyn: dyn, sink: sink, projectGVR: gvr}, nil
}

func (p *Provider) cfgForProject(id string) *rest.Config {
	c := rest.CopyConfig(p.root)
	c.Host = strings.TrimSuffix(p.root.Host, "/") + "/projects/" + id + "/control-plane"
	return c
}

// Run watches Projects and keeps the sink engaged with them until ctx ends. The
// informer's handlers only queue a Project's name; one worker engages and
// removes from what the informer's store holds (engagements).
func (p *Provider) Run(ctx context.Context) error {
	lw := &cache.ListWatch{
		ListFunc: func(lo metav1.ListOptions) (runtime.Object, error) {
			return p.dyn.Resource(p.projectGVR).List(ctx, lo)
		},
		WatchFunc: func(lo metav1.ListOptions) (watch.Interface, error) {
			return p.dyn.Resource(p.projectGVR).Watch(ctx, lo)
		},
	}
	inf := cache.NewSharedIndexInformer(lw, &unstructured.Unstructured{}, 0, nil)

	e := newEngagements(ctx, p, inf.GetStore())
	if _, err := inf.AddEventHandler(e.handlers()); err != nil {
		return err
	}

	go inf.Run(ctx.Done())
	go e.run()
	<-ctx.Done()
	return nil
}

func resolveProjectGVR(cfg *rest.Config, group, preferredVersion string) (schema.GroupVersionResource, error) {
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	rm := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disc))

	// If preferredVersion == "", RESTMapping will pick the preferred version
	mapping, err := rm.RESTMapping(schema.GroupKind{Group: group, Kind: "Project"}, preferredVersion)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	return mapping.Resource, nil
}
