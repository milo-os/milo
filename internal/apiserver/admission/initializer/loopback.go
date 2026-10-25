// milo/pkg/apiserver/admission/initializer/loopback.go
package initializer

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/client-go/rest"
)

// Local duck-typed interface: any plugin with this method will match.
type wantsLoopback interface {
	SetLoopbackConfig(*rest.Config)
}

type LoopbackInitializer struct {
	Loopback *rest.Config
}

func (i LoopbackInitializer) Initialize(p admission.Interface) {
	if w, ok := p.(wantsLoopback); ok && i.Loopback != nil {
		w.SetLoopbackConfig(i.Loopback)
	}
}

// Local duck-typed interface: any plugin with this method will match.
type wantsObjectConvertor interface {
	SetObjectConvertor(runtime.ObjectConvertor)
}

// ObjectConvertorInitializer injects an ObjectConvertor into admission plugins
// that want one. Milo passes the legacy scheme so the quota plugin can convert
// internal native types to their external versioned form before policy
// evaluation.
type ObjectConvertorInitializer struct {
	Convertor runtime.ObjectConvertor
}

func (i ObjectConvertorInitializer) Initialize(p admission.Interface) {
	if w, ok := p.(wantsObjectConvertor); ok && i.Convertor != nil {
		w.SetObjectConvertor(i.Convertor)
	}
}
