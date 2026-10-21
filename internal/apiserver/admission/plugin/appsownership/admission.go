// Package appsownership reserves the Apps ownership markers before installations
// are enabled. It denies protected writes by default; explicitly configured
// authenticated native controllers can narrowly change status or finalizers on existing ownership.
package appsownership

import (
	"context"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/client-go/dynamic"
)

const (
	PluginName      = "AppsOwnershipEnforcement"
	ProjectUIDLabel = "apps.miloapis.com/project-uid"
	AppUIDLabel     = "apps.miloapis.com/app-uid"
	// NamespaceReservationKey has no authorized acquisition/removal writer.
	// Presence on any kind must also block optional native writer exceptions.
	NamespaceReservationKey = "apps.miloapis.com/namespace-reservation"
)

type Plugin struct {
	*admission.Handler
	statusWriters    []StatusWriter
	finalizerWriters []FinalizerWriter
	dynamicClient    dynamic.Interface
}

var _ admission.ValidationInterface = &Plugin{}

func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(reader io.Reader) (admission.Interface, error) {
		config, err := readConfiguration(reader)
		if err != nil {
			return nil, err
		}
		p := NewPlugin()
		p.statusWriters = config.StatusWriters
		p.finalizerWriters = config.FinalizerWriters
		return p, nil
	})
}

func NewPlugin() *Plugin {
	return &Plugin{Handler: admission.NewHandler(admission.Create, admission.Update, admission.Delete)}
}

// Validate checks both objects after mutation, including materialized PATCH and
// server-side apply requests. Presence, rather than a nonempty value or complete
// pair, is what reserves a marker. A missing ProjectID never bypasses the guard:
// direct internal API endpoints need the same protection as project URL paths.
func (p *Plugin) Validate(ctx context.Context, a admission.Attributes, _ admission.ObjectInterfaces) error {
	if err := reserveGrant(a); err != nil {
		return err
	}
	if a.GetOperation() == admission.Update || a.GetOperation() == admission.Delete {
		if a.GetOldObject() == nil {
			return admission.NewForbidden(a, fmt.Errorf("Apps ownership cannot be checked without the old object"))
		}
	}
	objects := []runtime.Object{a.GetOldObject()}
	if a.GetOperation() != admission.Delete {
		objects = append(objects, a.GetObject())
	}
	accessors := make([]metav1.Object, 0, len(objects))
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		accessor, err := meta.Accessor(obj)
		if err != nil {
			return admission.NewForbidden(a, fmt.Errorf("Apps ownership metadata cannot be inspected"))
		}
		_, labelPresent := accessor.GetLabels()[NamespaceReservationKey]
		_, annotationPresent := accessor.GetAnnotations()[NamespaceReservationKey]
		if labelPresent || annotationPresent {
			return admission.NewForbidden(a, fmt.Errorf("Apps ownership namespace reservation is reserved; acquisition, mutation and removal remain disabled"))
		}
		accessors = append(accessors, accessor)
	}
	if p.allowStatus(ctx, a) || p.allowFinalizer(ctx, a) {
		return nil
	}
	for _, accessor := range accessors {
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
