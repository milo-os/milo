package namespace

import (
	"context"
	"errors"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientfeatures "k8s.io/client-go/features"
	clientfeaturestesting "k8s.io/client-go/features/testing"
	"k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	metadatafake "k8s.io/client-go/metadata/fake"
	clienttesting "k8s.io/client-go/testing"
)

// configMapsOnly is a discovery answer with one namespaced kind: upstream's deleter exits the process when
// discovery returns none (namespaced_resources_deleter.go line 166).
func configMapsOnly() ([]*metav1.APIResourceList, error) {
	return []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{
		{Name: "configmaps", Namespaced: true, Kind: "ConfigMap", Verbs: metav1.Verbs{"list", "delete", "deletecollection"}},
	}}}, nil
}

// TestAddProjectGivesUpOnACacheThatNeverSyncs covers the bound: a project whose
// namespace list never succeeds, as a gone Project's does, ends its wait at the
// bound, returns an error, and leaves no registration behind.
func TestAddProjectGivesUpOnACacheThatNeverSyncs(t *testing.T) {
	clientfeaturestesting.SetFeatureDuringTest(t, clientfeatures.WatchListClient, false)
	root := k8sfake.NewClientset()
	nm := NewNamespaceController(context.Background(), root, metadatafake.NewSimpleMetadataClient(runtime.NewScheme()),
		configMapsOnly,
		informers.NewSharedInformerFactory(root, 0).Core().V1().Namespaces(), 0, v1.FinalizerKubernetes)

	project := k8sfake.NewClientset()
	project.PrependReactor("list", "namespaces", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("the project's plane answers NotFound")
	})
	start := time.Now()
	err := nm.AddProject(context.Background(), "gone", project, metadatafake.NewSimpleMetadataClient(runtime.NewScheme()),
		configMapsOnly, 0, v1.FinalizerKubernetes, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error for a cache that never syncs")
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("expected the wait to end at the bound, waited %v", waited)
	}
	nm.mu.RLock()
	defer nm.mu.RUnlock()
	if _, ok := nm.listers["gone"]; ok {
		t.Fatal("expected no registration left for the project")
	}
}
