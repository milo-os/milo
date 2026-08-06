package events

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEventsV1Backend implements the EventsV1Backend interface for testing.
type mockEventsV1Backend struct {
	createFunc func(ctx context.Context, namespace string, event *eventsv1.Event) (*eventsv1.Event, error)
}

func (m *mockEventsV1Backend) CreateEventsV1Event(ctx context.Context, namespace string, event *eventsv1.Event) (*eventsv1.Event, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, namespace, event)
	}
	return event, nil
}

func (m *mockEventsV1Backend) GetEventsV1Event(ctx context.Context, namespace, name string) (*eventsv1.Event, error) {
	return &eventsv1.Event{}, nil
}

func (m *mockEventsV1Backend) ListEventsV1Events(ctx context.Context, namespace string, opts *metav1.ListOptions) (*eventsv1.EventList, error) {
	return &eventsv1.EventList{}, nil
}

func (m *mockEventsV1Backend) UpdateEventsV1Event(ctx context.Context, namespace string, event *eventsv1.Event) (*eventsv1.Event, error) {
	return event, nil
}

func (m *mockEventsV1Backend) DeleteEventsV1Event(ctx context.Context, namespace, name string, opts *metav1.DeleteOptions) error {
	return nil
}

func (m *mockEventsV1Backend) WatchEventsV1Events(ctx context.Context, namespace string, opts *metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func TestInjectEventsV1ScopeAnnotations_RegardingObjectNeverSetsScope(t *testing.T) {
	// Regarding is client-supplied; the request's parent context must win.
	ctx := apirequest.WithUser(context.Background(), &authuser.DefaultInfo{
		Name: "alice",
		Extra: map[string][]string{
			ExtraKeyParentType: {"Project"},
			ExtraKeyParentName: {"project-alice"},
		},
	})
	event := &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "test-event"},
		Regarding: corev1.ObjectReference{
			APIVersion: "resourcemanager.miloapis.com/v1alpha1",
			Kind:       "Project",
			Name:       "project-other",
		},
	}

	err := injectEventsV1ScopeAnnotations(ctx, event)

	require.NoError(t, err)
	assert.Equal(t, "Project", event.Annotations[ScopeTypeAnnotation])
	assert.Equal(t, "project-alice", event.Annotations[ScopeNameAnnotation],
		"scope must come from the request context, never the regarding object")
}

func TestEventsV1REST_Create_LeavesControllerEventUnscoped(t *testing.T) {
	// A controller that does not impersonate a parent context gets no scope
	// annotations, however its Regarding object is populated. Reaching a
	// tenant feed requires impersonation, not inference.
	var capturedEvent *eventsv1.Event
	backend := &mockEventsV1Backend{
		createFunc: func(ctx context.Context, namespace string, event *eventsv1.Event) (*eventsv1.Event, error) {
			capturedEvent = event.DeepCopy()
			return event, nil
		},
	}

	r := NewEventsV1REST(backend)

	ctx := apirequest.WithUser(context.Background(), &authuser.DefaultInfo{
		Name: "system:serviceaccount:milo-system:project-suspension-controller",
	})
	ctx = apirequest.WithNamespace(ctx, "default")

	event := &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "project-suspended.xyz"},
		Regarding: corev1.ObjectReference{
			APIVersion: "resourcemanager.miloapis.com/v1alpha1",
			Kind:       "Project",
			Name:       "project-999",
		},
		Reason: "Suspended",
	}

	result, err := r.Create(ctx, event, nil, &metav1.CreateOptions{})

	require.NoError(t, err)
	assert.NotNil(t, result)
	require.NotNil(t, capturedEvent, "backend should have been called")
	assert.Empty(t, capturedEvent.Annotations[ScopeTypeAnnotation])
	assert.Empty(t, capturedEvent.Annotations[ScopeNameAnnotation])
}

// Ensure the mock backend implements the full EventsV1Backend interface.
var _ EventsV1Backend = (*mockEventsV1Backend)(nil)
