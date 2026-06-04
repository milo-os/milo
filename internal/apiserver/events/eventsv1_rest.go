package events

import (
	"context"
	"fmt"

	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
)

// EventsV1Backend defines the interface for events.k8s.io/v1 event storage operations.
type EventsV1Backend interface {
	CreateEventsV1Event(ctx context.Context, namespace string, event *eventsv1.Event) (*eventsv1.Event, error)
	GetEventsV1Event(ctx context.Context, namespace, name string) (*eventsv1.Event, error)
	ListEventsV1Events(ctx context.Context, namespace string, opts *metav1.ListOptions) (*eventsv1.EventList, error)
	UpdateEventsV1Event(ctx context.Context, namespace string, event *eventsv1.Event) (*eventsv1.Event, error)
	DeleteEventsV1Event(ctx context.Context, namespace, name string, opts *metav1.DeleteOptions) error
	WatchEventsV1Events(ctx context.Context, namespace string, opts *metav1.ListOptions) (watch.Interface, error)
}

// EventsV1REST implements the REST endpoint for events.k8s.io/v1 Events
type EventsV1REST struct {
	backend EventsV1Backend
}

var _ rest.Scoper = &EventsV1REST{}
var _ rest.Creater = &EventsV1REST{}
var _ rest.Getter = &EventsV1REST{}
var _ rest.Lister = &EventsV1REST{}
var _ rest.Updater = &EventsV1REST{}
var _ rest.GracefulDeleter = &EventsV1REST{}
var _ rest.Watcher = &EventsV1REST{}
var _ rest.Storage = &EventsV1REST{}
var _ rest.SingularNameProvider = &EventsV1REST{}
var _ rest.TableConvertor = &EventsV1REST{}

// NewEventsV1REST creates a new REST storage for events.k8s.io/v1 Events.
func NewEventsV1REST(backend EventsV1Backend) *EventsV1REST {
	return &EventsV1REST{backend: backend}
}

func (r *EventsV1REST) GetSingularName() string { return "event" }
func (r *EventsV1REST) NamespaceScoped() bool   { return true }
func (r *EventsV1REST) New() runtime.Object     { return &eventsv1.Event{} }
func (r *EventsV1REST) NewList() runtime.Object { return &eventsv1.EventList{} }
func (r *EventsV1REST) Destroy()                {}

// Create creates a new event with injected scope annotations.
// Validation is delegated to the upstream Activity API server.
func (r *EventsV1REST) Create(
	ctx context.Context,
	obj runtime.Object,
	_ rest.ValidateObjectFunc,
	_ *metav1.CreateOptions,
) (runtime.Object, error) {
	logger := klog.FromContext(ctx)
	event := obj.(*eventsv1.Event)

	ns, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("namespace is required")
	}

	if err := injectEventsV1ScopeAnnotations(ctx, event); err != nil {
		logger.Error(err, "Failed to inject scope annotations")
		return nil, apierrors.NewBadRequest(fmt.Sprintf("failed to determine scope: %v", err))
	}

	logger.V(4).Info("Creating eventsv1 event", "namespace", ns, "name", event.Name, "reason", event.Reason)

	result, err := r.backend.CreateEventsV1Event(ctx, ns, event)
	if err != nil {
		logger.Error(err, "Failed to create eventsv1 event", "namespace", ns, "name", event.Name)
		return nil, err
	}

	logger.V(4).Info("Created eventsv1 event", "namespace", ns, "name", result.Name)
	return result, nil
}

// Get retrieves an event by name.
func (r *EventsV1REST) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	logger := klog.FromContext(ctx)

	ns, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("namespace is required")
	}

	logger.V(4).Info("Getting eventsv1 event", "namespace", ns, "name", name)

	result, err := r.backend.GetEventsV1Event(ctx, ns, name)
	if err != nil {
		logger.Error(err, "Failed to get eventsv1 event", "namespace", ns, "name", name)
		return nil, err
	}

	logger.V(4).Info("Got eventsv1 event", "namespace", ns, "name", name, "reason", result.Reason)
	return result, nil
}

// List lists events with optional filtering.
func (r *EventsV1REST) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	logger := klog.FromContext(ctx)

	ns, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("namespace is required")
	}

	logger.V(4).Info("Listing eventsv1 events", "namespace", ns)

	lo := &metav1.ListOptions{}
	if options != nil {
		if err := metainternalversion.Convert_internalversion_ListOptions_To_v1_ListOptions(options, lo, nil); err != nil {
			return nil, err
		}
	}

	result, err := r.backend.ListEventsV1Events(ctx, ns, lo)
	if err != nil {
		logger.Error(err, "Failed to list eventsv1 events", "namespace", ns)
		return nil, err
	}

	logger.V(4).Info("Listed eventsv1 events", "namespace", ns, "count", len(result.Items))
	return result, nil
}

// Update updates an existing event.
// Validation is delegated to the upstream Activity API server.
func (r *EventsV1REST) Update(
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
	event := obj.(*eventsv1.Event)

	if err := injectEventsV1ScopeAnnotations(ctx, event); err != nil {
		logger.Error(err, "Failed to inject scope annotations")
		return nil, false, apierrors.NewBadRequest(fmt.Sprintf("failed to determine scope: %v", err))
	}

	logger.V(4).Info("Updating eventsv1 event", "namespace", ns, "name", name)

	result, err := r.backend.UpdateEventsV1Event(ctx, ns, event)
	if err != nil {
		logger.Error(err, "Failed to update eventsv1 event", "namespace", ns, "name", name)
		return nil, false, err
	}

	logger.V(4).Info("Updated eventsv1 event", "namespace", ns, "name", name)
	return result, false, nil
}

// Delete deletes an event.
// Validation is delegated to the upstream Activity API server.
func (r *EventsV1REST) Delete(
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

	logger.V(4).Info("Deleting eventsv1 event", "namespace", ns, "name", name)

	if err := r.backend.DeleteEventsV1Event(ctx, ns, name, options); err != nil {
		logger.Error(err, "Failed to delete eventsv1 event", "namespace", ns, "name", name)
		return nil, false, err
	}

	logger.V(4).Info("Deleted eventsv1 event", "namespace", ns, "name", name)
	return &metav1.Status{Status: metav1.StatusSuccess}, true, nil
}

// Watch establishes a watch connection for events
func (r *EventsV1REST) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	logger := klog.FromContext(ctx)

	ns, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("namespace is required")
	}

	logger.V(4).Info("Watching eventsv1 events", "namespace", ns)

	lo := &metav1.ListOptions{}
	if options != nil {
		if err := metainternalversion.Convert_internalversion_ListOptions_To_v1_ListOptions(options, lo, nil); err != nil {
			return nil, err
		}
	}

	return r.backend.WatchEventsV1Events(ctx, ns, lo)
}

// ConvertToTable provides a table representation for kubectl output
func (r *EventsV1REST) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Last Seen", Type: "string", Description: "Time when this Event was last observed"},
			{Name: "Type", Type: "string", Description: "Type of this event (Normal, Warning)"},
			{Name: "Reason", Type: "string", Description: "Short machine understandable string"},
			{Name: "Object", Type: "string", Description: "The object this Event is about"},
			{Name: "Message", Type: "string", Description: "Human-readable description"},
		},
	}

	appendRow := func(event *eventsv1.Event) {
		lastSeen := "<unknown>"
		if !event.EventTime.Time.IsZero() {
			lastSeen = event.EventTime.Time.Format("2006-01-02T15:04:05Z")
		} else if !event.DeprecatedLastTimestamp.IsZero() {
			lastSeen = event.DeprecatedLastTimestamp.Format("2006-01-02T15:04:05Z")
		}

		objectRef := fmt.Sprintf("%s/%s", event.Regarding.Kind, event.Regarding.Name)
		if event.Regarding.Namespace != "" && event.Regarding.Namespace != event.Namespace {
			objectRef = event.Regarding.Namespace + "/" + objectRef
		}

		table.Rows = append(table.Rows, metav1.TableRow{
			Cells: []interface{}{
				lastSeen,
				event.Type,
				event.Reason,
				objectRef,
				event.Note,
			},
			Object: runtime.RawExtension{Object: event},
		})
	}

	switch obj := object.(type) {
	case *eventsv1.EventList:
		for i := range obj.Items {
			appendRow(&obj.Items[i])
		}
	case *eventsv1.Event:
		appendRow(obj)
	default:
		return nil, nil
	}

	return table, nil
}

// injectEventsV1ScopeAnnotations adds scope annotations to an events.k8s.io/v1 Event
func injectEventsV1ScopeAnnotations(ctx context.Context, event *eventsv1.Event) error {
	userInfo, ok := apirequest.UserFrom(ctx)
	if !ok {
		return fmt.Errorf("no user in context")
	}

	extras := userInfo.GetExtra()

	parentType := getFirstExtra(extras, ExtraKeyParentType)
	parentName := getFirstExtra(extras, ExtraKeyParentName)

	// Events without scope context are normal (e.g., system components, platform-level operations)
	if parentType == "" || parentName == "" {
		return nil
	}

	if event.Annotations == nil {
		event.Annotations = make(map[string]string)
	}

	event.Annotations[ScopeTypeAnnotation] = parentType
	event.Annotations[ScopeNameAnnotation] = parentName

	return nil
}
