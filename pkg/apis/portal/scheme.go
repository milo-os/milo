package portal

import (
	"k8s.io/apimachinery/pkg/runtime"

	"go.miloapis.com/milo/pkg/apis/portal/v1alpha1"
)

func Install(scheme *runtime.Scheme) {
	v1alpha1.AddToScheme(scheme)
}
