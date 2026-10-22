// Package appsownership reserves the Apps ownership markers before installations
// are enabled. This bootstrap guard has no trusted writers: neither service
// identities, system:masters, nor an impersonated identity can bypass it.
package appsownership

import (
	"context"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
)

const (
	PluginName      = "AppsOwnershipEnforcement"
	ProjectUIDLabel = "apps.miloapis.com/project-uid"
	AppUIDLabel     = "apps.miloapis.com/app-uid"
)

type Plugin struct{ *admission.Handler }

var _ admission.ValidationInterface = &Plugin{}

func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(_ io.Reader) (admission.Interface, error) {
		return NewPlugin(), nil
	})
}

func NewPlugin() *Plugin {
	return &Plugin{Handler: admission.NewHandler(admission.Create, admission.Update, admission.Delete)}
}

// Validate checks both objects after mutation, including materialized PATCH and
// server-side apply requests. Presence, rather than a nonempty value or complete
// pair, is what reserves a marker. A missing ProjectID never bypasses the guard:
// direct internal API endpoints need the same protection as project URL paths.
func (p *Plugin) Validate(_ context.Context, a admission.Attributes, _ admission.ObjectInterfaces) error {
	if a.GetOperation() == admission.Update || a.GetOperation() == admission.Delete {
		if a.GetOldObject() == nil {
			return admission.NewForbidden(a, fmt.Errorf("Apps ownership cannot be checked without the old object"))
		}
	}
	objects := []runtime.Object{a.GetOldObject()}
	if a.GetOperation() != admission.Delete {
		objects = append(objects, a.GetObject())
	}
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		accessor, err := meta.Accessor(obj)
		if err != nil {
			return admission.NewForbidden(a, fmt.Errorf("Apps ownership metadata cannot be inspected"))
		}
		for _, key := range []string{ProjectUIDLabel, AppUIDLabel} {
			_, labelPresent := accessor.GetLabels()[key]
			_, annotationPresent := accessor.GetAnnotations()[key]
			if labelPresent || annotationPresent {
				return admission.NewForbidden(a, fmt.Errorf("Apps ownership is reserved; managed resource writes remain disabled until ownership and release authorization are available"))
			}
		}
	}
	return nil
}
