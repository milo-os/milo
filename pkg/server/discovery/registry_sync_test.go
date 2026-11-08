package discovery

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// TestRunSyncsOnlyAfterApplyingTheInitialCRDs starts Run on a CRD informer that has already synced, as the API server's
// shared apiextensions informer has when the registry's post-start hook runs. The registry must not report the CRD
// side synced before it has applied every CRD the informer listed: until then a tagged type reads as untagged.
func TestRunSyncsOnlyAfterApplyingTheInitialCRDs(t *testing.T) {
	const crds = 2000
	objects := make([]k8sruntime.Object, crds)
	for i := range objects {
		objects[i] = &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf("widgets%d.example.com", i),
				Annotations: map[string]string{ParentContextsAnnotation: string(ContextOrganization)},
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "example.com",
				Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: fmt.Sprintf("widgets%d", i)},
			},
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	factory := apiextensionsinformers.NewSharedInformerFactory(fake.NewClientset(objects...), 0)
	informer := factory.Apiextensions().V1().CustomResourceDefinitions().Informer()
	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		t.Fatal("the CRD informer did not sync")
	}

	r := newRegistry()
	go func() { _ = r.Run(ctx, factory) }()
	for ctx.Err() == nil {
		r.mu.RLock()
		synced, applied := r.hasInit, len(r.crd)
		r.mu.RUnlock()
		if synced {
			if applied != crds {
				t.Fatalf("the registry reported the CRDs synced with %d of %d applied", applied, crds)
			}
			return
		}
		runtime.Gosched()
	}
	t.Fatal("the registry never reported the CRDs synced")
}
