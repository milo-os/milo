package events

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
)

// Backend defines the interface for event storage operations.
type Backend interface {
	CreateEvent(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error)
	GetEvent(ctx context.Context, namespace, name string) (*corev1.Event, error)
	ListEvents(ctx context.Context, namespace string, opts *metav1.ListOptions) (*corev1.EventList, error)
	UpdateEvent(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error)
	DeleteEvent(ctx context.Context, namespace, name string, opts *metav1.DeleteOptions) error
	WatchEvents(ctx context.Context, namespace string, opts *metav1.ListOptions) (watch.Interface, error)
}

// REST implements the REST endpoint for core/v1 Events
type REST struct {
	backend Backend
}

var _ rest.Scoper = &REST{}
var _ rest.Creater = &REST{}
var _ rest.Getter = &REST{}
var _ rest.Lister = &REST{}
var _ rest.Updater = &REST{}
var _ rest.GracefulDeleter = &REST{}
var _ rest.Watcher = &REST{}
var _ rest.Storage = &REST{}
var _ rest.SingularNameProvider = &REST{}
var _ rest.TableConvertor = &REST{}

// NewREST creates a new REST storage for Events.
func NewREST(backend Backend) *REST {
	return &REST{backend: backend}
}

func (r *REST) GetSingularName() string { return "event" }
func (r *REST) NamespaceScoped() bool   { return true }
func (r *REST) New() runtime.Object     { return &corev1.Event{} }
func (r *REST) NewList() runtime.Object { return &corev1.EventList{} }
func (r *REST) Destroy()                {}

// Create creates a new event with injected scope annotations.
// Validation is delegated to the upstream Activity API server.
func (r *REST) Create(
	ctx context.Context,
	obj runtime.Object,
	_ rest.ValidateObjectFunc,
	_ *metav1.CreateOptions,
) (runtime.Object, error) {
	logger := klog.FromContext(ctx)
	event := obj.(*corev1.Event)

	ns, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("namespace is required")
	}

	if err := injectScopeAnnotations(ctx, event); err != nil {
		logger.Error(err, "Failed to inject scope annotations")
		return nil, apierrors.NewBadRequest(fmt.Sprintf("failed to determine scope: %v", err))
	}

	logger.V(4).Info("Creating event", "namespace", ns, "name", event.Name, "reason", event.Reason)

	result, err := r.backend.CreateEvent(ctx, ns, event)
	if err != nil {
		logger.Error(err, "Failed to create event", "namespace", ns, "name", event.Name)
		return nil, err
	}

	logger.V(4).Info("Created event", "namespace", ns, "name", result.Name)
	return result, nil
}

// Get retrieves an event by name.
func (r *REST) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	logger := klog.FromContext(ctx)

	ns, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("namespace is required")
	}

	logger.V(4).Info("Getting event", "namespace", ns, "name", name)

	result, err := r.backend.GetEvent(ctx, ns, name)
	if err != nil {
		logger.Error(err, "Failed to get event", "namespace", ns, "name", name)
		return nil, err
	}

	logger.V(4).Info("Got event", "namespace", ns, "name", name, "reason", result.Reason)
	return result, nil
}

// List lists events with optional filtering.
func (r *REST) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	logger := klog.FromContext(ctx)

	ns, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("namespace is required")
	}

	logger.V(4).Info("Listing events", "namespace", ns)

	lo := &metav1.ListOptions{}
	if options != nil {
		if err := metainternalversion.Convert_internalversion_ListOptions_To_v1_ListOptions(options, lo, nil); err != nil {
			return nil, err
		}
	}

	result, err := r.backend.ListEvents(ctx, ns, lo)
	if err != nil {
		logger.Error(err, "Failed to list events", "namespace", ns)
		return nil, err
	}

	logger.V(4).Info("Listed events", "namespace", ns, "count", len(result.Items))
	return result, nil
}

// Update updates an existing event.
// Validation is delegated to the upstream Activity API server.
func (r *REST) Update(
	ctx context.Context,
	name string,
	objInfo rest.UpdatedObjectInfo,
	_ rest.ValidateObjectFunc,
	_ rest.ValidateObjectUpdateFunc,
	_ bool,
	_ *metav1.UpdateOptions,
) (runtime.Object, bool, error) {
	logger := klog.FromContext(ctx)

	ns, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		return nil, false, apierrors.NewBadRequest("namespace is required")
	}

	obj, err := objInfo.UpdatedObject(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	event := obj.(*corev1.Event)

	if err := injectScopeAnnotations(ctx, event); err != nil {
		logger.Error(err, "Failed to inject scope annotations")
		return nil, false, apierrors.NewBadRequest(fmt.Sprintf("failed to determine scope: %v", err))
	}

	logger.V(4).Info("Updating event", "namespace", ns, "name", name)

	result, err := r.backend.UpdateEvent(ctx, ns, event)
	if err != nil {
		logger.Error(err, "Failed to update event", "namespace", ns, "name", name)
		return nil, false, err
	}

	logger.V(4).Info("Updated event", "namespace", ns, "name", name)
	return result, false, nil
}

// Delete deletes an event.
// Validation is delegated to the upstream Activity API server.
func (r *REST) Delete(
	ctx context.Context,
	name string,
	_ rest.ValidateObjectFunc,
	options *metav1.DeleteOptions,
) (runtime.Object, bool, error) {
	logger := klog.FromContext(ctx)

	ns, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		return nil, false, apierrors.NewBadRequest("namespace is required")
	}

	logger.V(4).Info("Deleting event", "namespace", ns, "name", name)

	if err := r.backend.DeleteEvent(ctx, ns, name, options); err != nil {
		logger.Error(err, "Failed to delete event", "namespace", ns, "name", name)
		return nil, false, err
	}

	logger.V(4).Info("Deleted event", "namespace", ns, "name", name)
	return &metav1.Status{Status: metav1.StatusSuccess}, true, nil
}

// Watch establishes a watch connection for events
func (r *REST) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	logger := klog.FromContext(ctx)

	ns, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("namespace is required")
	}

	logger.V(4).Info("Watching events", "namespace", ns)

	lo := &metav1.ListOptions{}
	if options != nil {
		if err := metainternalversion.Convert_internalversion_ListOptions_To_v1_ListOptions(options, lo, nil); err != nil {
			return nil, err
		}
	}

	return r.backend.WatchEvents(ctx, ns, lo)
}

// ConvertToTable provides a table representation for kubectl output
func (r *REST) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Last Seen", Type: "string", Description: "Time when this Event was last observed"},
			{Name: "Type", Type: "string", Description: "Type of this event (Normal, Warning)"},
			{Name: "Reason", Type: "string", Description: "Short machine understandable string"},
			{Name: "Object", Type: "string", Description: "The object this Event is about"},
			{Name: "Message", Type: "string", Description: "Human-readable description"},
		},
	}

	appendRow := func(event *corev1.Event) {
		lastSeen := "<unknown>"
		if !event.LastTimestamp.IsZero() {
			lastSeen = event.LastTimestamp.Format("2006-01-02T15:04:05Z")
		} else if !event.EventTime.IsZero() {
			lastSeen = event.EventTime.Format("2006-01-02T15:04:05Z")
		}

		objectRef := fmt.Sprintf("%s/%s", event.InvolvedObject.Kind, event.InvolvedObject.Name)
		if event.InvolvedObject.Namespace != "" && event.InvolvedObject.Namespace != event.Namespace {
			objectRef = event.InvolvedObject.Namespace + "/" + objectRef
		}

		table.Rows = append(table.Rows, metav1.TableRow{
			Cells: []interface{}{
				lastSeen,
				event.Type,
				event.Reason,
				objectRef,
				event.Message,
			},
			Object: runtime.RawExtension{Object: event},
		})
	}

	switch obj := object.(type) {
	case *corev1.EventList:
		for i := range obj.Items {
			appendRow(&obj.Items[i])
		}
	case *corev1.Event:
		appendRow(obj)
	default:
		return nil, nil
	}

	return table, nil
}
