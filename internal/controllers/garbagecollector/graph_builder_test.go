package garbagecollector

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	metadatafake "k8s.io/client-go/metadata/fake"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/controller-manager/pkg/informerfactory"
	"k8s.io/klog/v2"
)

type resettableMapper struct{ meta.RESTMapper }

func (resettableMapper) Reset() {}

func TestSyncMonitorsSkipsIgnoredResources(t *testing.T) {
	configMaps := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	flowSchemas := schema.GroupVersionResource{Group: "flowcontrol.apiserver.k8s.io", Version: "v1", Resource: "flowschemas"}
	widgets := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}

	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{Group: "flowcontrol.apiserver.k8s.io", Version: "v1", Kind: "FlowSchema"}, meta.RESTScopeRoot)
	mapper.Add(schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}, meta.RESTScopeNamespace)

	metadataClient := metadatafake.NewSimpleMetadataClient(runtime.NewScheme())
	factory := informerfactory.NewInformerFactory(
		informers.NewSharedInformerFactory(fake.NewClientset(), 0),
		metadatainformer.NewSharedInformerFactory(metadataClient, 0),
	)

	ignored := map[schema.GroupResource]struct{}{flowSchemas.GroupResource(): {}}
	gb := NewDependencyGraphBuilder(context.Background(), metadataClient, resettableMapper{mapper}, ignored, factory, make(chan struct{}))

	err := gb.syncMonitors(klog.Background(), map[schema.GroupVersionResource]struct{}{
		configMaps:  {},
		flowSchemas: {},
		widgets:     {},
	})
	if err != nil {
		t.Fatalf("syncMonitors: %v", err)
	}

	if len(gb.monitors) != 2 {
		t.Fatalf("expected 2 monitors, got %d: %v", len(gb.monitors), gb.monitors)
	}
	for _, want := range []schema.GroupVersionResource{configMaps, widgets} {
		if _, ok := gb.monitors[want]; !ok {
			t.Errorf("expected a monitor for %s", want)
		}
	}
	if _, ok := gb.monitors[flowSchemas]; ok {
		t.Errorf("ignored resource %s must not get a monitor", flowSchemas)
	}
}
