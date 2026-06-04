package discovery

import (
	"k8s.io/apimachinery/pkg/runtime"

	"go.miloapis.com/milo/pkg/apis/discovery/v1alpha1"
)

func Install(scheme *runtime.Scheme) {
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
}
