package events

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockBackend implements the Backend interface for testing.
type mockBackend struct {
	createFunc func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error)
	getFunc    func(ctx context.Context, namespace, name string) (*corev1.Event, error)
	listFunc   func(ctx context.Context, namespace string, opts *metav1.ListOptions) (*corev1.EventList, error)
	updateFunc func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error)
	deleteFunc func(ctx context.Context, namespace, name string, opts *metav1.DeleteOptions) error
	watchFunc  func(ctx context.Context, namespace string, opts *metav1.ListOptions) (watch.Interface, error)
}

func (m *mockBackend) CreateEvent(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, namespace, event)
	}
	return event, nil
}

func (m *mockBackend) GetEvent(ctx context.Context, namespace, name string) (*corev1.Event, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, namespace, name)
	}
	return &corev1.Event{}, nil
}

func (m *mockBackend) ListEvents(ctx context.Context, namespace string, opts *metav1.ListOptions) (*corev1.EventList, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, namespace, opts)
	}
	return &corev1.EventList{}, nil
}

func (m *mockBackend) UpdateEvent(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, namespace, event)
	}
	return event, nil
}

func (m *mockBackend) DeleteEvent(ctx context.Context, namespace, name string, opts *metav1.DeleteOptions) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, namespace, name, opts)
	}
	return nil
}

func (m *mockBackend) WatchEvents(ctx context.Context, namespace string, opts *metav1.ListOptions) (watch.Interface, error) {
	if m.watchFunc != nil {
		return m.watchFunc(ctx, namespace, opts)
	}
	return nil, errors.New("watch not implemented in mock")
}

func TestREST_Create_InjectsScopeAnnotations(t *testing.T) {
	tests := []struct {
		name                 string
		user                 authuser.Info
		event                *corev1.Event
		wantScopeType        string
		wantScopeName        string
		wantNoScopeInjection bool
	}{
		{
			name: "project-scoped user creates event",
			user: &authuser.DefaultInfo{
				Name: "alice",
				Extra: map[string][]string{
					ExtraKeyParentType: {"Project"},
					ExtraKeyParentName: {"project-123"},
				},
			},
			event: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-started.abc"},
			},
			wantScopeType: "Project",
			wantScopeName: "project-123",
		},
		{
			name: "organization-scoped user creates event",
			user: &authuser.DefaultInfo{
				Name: "bob",
				Extra: map[string][]string{
					ExtraKeyParentType: {"Organization"},
					ExtraKeyParentName: {"org-456"},
				},
			},
			event: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{Name: "deployment-scaled.def"},
			},
			wantScopeType: "Organization",
			wantScopeName: "org-456",
		},
		{
			name: "platform user creates event without scope",
			user: &authuser.DefaultInfo{
				Name: "system:serviceaccount:kube-system:default",
			},
			event: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{Name: "node-ready.ghi"},
			},
			wantNoScopeInjection: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedEvent *corev1.Event
			backend := &mockBackend{
				createFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
					capturedEvent = event.DeepCopy()
					return event, nil
				},
			}

			r := NewREST(backend)

			ctx := apirequest.WithUser(context.Background(), tt.user)
			ctx = apirequest.WithNamespace(ctx, "default")

			result, err := r.Create(ctx, tt.event, nil, &metav1.CreateOptions{})

			require.NoError(t, err)
			assert.NotNil(t, result)
			require.NotNil(t, capturedEvent, "backend should have been called")

			if tt.wantNoScopeInjection {
				assert.Empty(t, capturedEvent.Annotations[ScopeTypeAnnotation])
				assert.Empty(t, capturedEvent.Annotations[ScopeNameAnnotation])
			} else {
				assert.Equal(t, tt.wantScopeType, capturedEvent.Annotations[ScopeTypeAnnotation])
				assert.Equal(t, tt.wantScopeName, capturedEvent.Annotations[ScopeNameAnnotation])
			}
		})
	}
}

func TestREST_Create_RequiresNamespace(t *testing.T) {
	backend := &mockBackend{}
	r := NewREST(backend)

	ctx := apirequest.WithUser(context.Background(), &authuser.DefaultInfo{Name: "alice"})
	// No namespace in context

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
	}

	_, err := r.Create(ctx, event, nil, &metav1.CreateOptions{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace is required")
}

func TestREST_Create_RequiresUser(t *testing.T) {
	backend := &mockBackend{}
	r := NewREST(backend)

	ctx := apirequest.WithNamespace(context.Background(), "default")
	// No user in context

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
	}

	_, err := r.Create(ctx, event, nil, &metav1.CreateOptions{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to determine scope")
}

func TestREST_Update_ReInjectsScopeAnnotations(t *testing.T) {
	t.Run("prevents scope tampering", func(t *testing.T) {
		var capturedEvent *corev1.Event
		backend := &mockBackend{
			updateFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
				capturedEvent = event.DeepCopy()
				return event, nil
			},
		}

		r := NewREST(backend)

		// User is scoped to Project A
		ctx := apirequest.WithUser(context.Background(), &authuser.DefaultInfo{
			Name: "alice",
			Extra: map[string][]string{
				ExtraKeyParentType: {"Project"},
				ExtraKeyParentName: {"project-a"},
			},
		})
		ctx = apirequest.WithNamespace(ctx, "default")

		// Event initially claims to be scoped to Project B (malicious client)
		event := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-event",
				Annotations: map[string]string{
					ScopeTypeAnnotation: "Project",
					ScopeNameAnnotation: "project-b",
				},
			},
		}

		objInfo := &mockUpdatedObjectInfo{obj: event}

		result, created, err := r.Update(ctx, "test-event", objInfo, nil, nil, false, &metav1.UpdateOptions{})

		require.NoError(t, err)
		assert.False(t, created)
		assert.NotNil(t, result)
		require.NotNil(t, capturedEvent, "backend should have been called")

		// Verify scope was re-injected from user context (Project A, not Project B)
		assert.Equal(t, "Project", capturedEvent.Annotations[ScopeTypeAnnotation])
		assert.Equal(t, "project-a", capturedEvent.Annotations[ScopeNameAnnotation],
			"scope should be overwritten with user's actual scope")
	})

	t.Run("preserves other annotations", func(t *testing.T) {
		var capturedEvent *corev1.Event
		backend := &mockBackend{
			updateFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
				capturedEvent = event.DeepCopy()
				return event, nil
			},
		}

		r := NewREST(backend)

		ctx := apirequest.WithUser(context.Background(), &authuser.DefaultInfo{
			Name: "alice",
			Extra: map[string][]string{
				ExtraKeyParentType: {"Project"},
				ExtraKeyParentName: {"project-123"},
			},
		})
		ctx = apirequest.WithNamespace(ctx, "default")

		event := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-event",
				Annotations: map[string]string{
					"custom-annotation": "custom-value",
					"another-key":       "another-value",
				},
			},
		}

		objInfo := &mockUpdatedObjectInfo{obj: event}

		_, _, err := r.Update(ctx, "test-event", objInfo, nil, nil, false, &metav1.UpdateOptions{})

		require.NoError(t, err)
		require.NotNil(t, capturedEvent)

		// Verify custom annotations preserved
		assert.Equal(t, "custom-value", capturedEvent.Annotations["custom-annotation"])
		assert.Equal(t, "another-value", capturedEvent.Annotations["another-key"])

		// Verify scope injected
		assert.Equal(t, "Project", capturedEvent.Annotations[ScopeTypeAnnotation])
		assert.Equal(t, "project-123", capturedEvent.Annotations[ScopeNameAnnotation])
	})
}

func TestREST_Get_Success(t *testing.T) {
	expectedEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-event",
			Namespace: "default",
		},
		Reason: "Started",
	}

	backend := &mockBackend{
		getFunc: func(ctx context.Context, namespace, name string) (*corev1.Event, error) {
			assert.Equal(t, "default", namespace)
			assert.Equal(t, "test-event", name)
			return expectedEvent, nil
		},
	}

	r := NewREST(backend)

	ctx := apirequest.WithNamespace(context.Background(), "default")

	result, err := r.Get(ctx, "test-event", &metav1.GetOptions{})

	require.NoError(t, err)
	assert.Equal(t, expectedEvent, result)
}

func TestREST_List_Success(t *testing.T) {
	expectedList := &corev1.EventList{
		Items: []corev1.Event{
			{ObjectMeta: metav1.ObjectMeta{Name: "event-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "event-2"}},
		},
	}

	backend := &mockBackend{
		listFunc: func(ctx context.Context, namespace string, opts *metav1.ListOptions) (*corev1.EventList, error) {
			assert.Equal(t, "default", namespace)
			return expectedList, nil
		},
	}

	r := NewREST(backend)

	ctx := apirequest.WithNamespace(context.Background(), "default")

	result, err := r.List(ctx, nil)

	require.NoError(t, err)
	assert.Equal(t, expectedList, result)
}

func TestREST_Delete_Success(t *testing.T) {
	var capturedNamespace, capturedName string
	backend := &mockBackend{
		deleteFunc: func(ctx context.Context, namespace, name string, opts *metav1.DeleteOptions) error {
			capturedNamespace = namespace
			capturedName = name
			return nil
		},
	}

	r := NewREST(backend)

	ctx := apirequest.WithNamespace(context.Background(), "default")

	result, immediate, err := r.Delete(ctx, "test-event", nil, &metav1.DeleteOptions{})

	require.NoError(t, err)
	assert.True(t, immediate)
	assert.NotNil(t, result)

	status, ok := result.(*metav1.Status)
	require.True(t, ok)
	assert.Equal(t, metav1.StatusSuccess, status.Status)

	assert.Equal(t, "default", capturedNamespace)
	assert.Equal(t, "test-event", capturedName)
}

func TestREST_BackendError_Propagated(t *testing.T) {
	expectedErr := errors.New("backend storage error")

	backend := &mockBackend{
		createFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
			return nil, expectedErr
		},
	}

	r := NewREST(backend)

	ctx := apirequest.WithUser(context.Background(), &authuser.DefaultInfo{
		Name: "alice",
		Extra: map[string][]string{
			ExtraKeyParentType: {"Project"},
			ExtraKeyParentName: {"project-123"},
		},
	})
	ctx = apirequest.WithNamespace(ctx, "default")

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
	}

	_, err := r.Create(ctx, event, nil, &metav1.CreateOptions{})

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

func TestREST_ConvertToTable(t *testing.T) {
	t.Run("converts event list", func(t *testing.T) {
		r := NewREST(&mockBackend{})

		eventList := &corev1.EventList{
			Items: []corev1.Event{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "event-1",
						Namespace: "default",
					},
					Type:   "Normal",
					Reason: "Started",
					InvolvedObject: corev1.ObjectReference{
						Kind: "Pod",
						Name: "my-pod",
					},
					Message:       "Container started",
					LastTimestamp: metav1.Now(),
				},
			},
		}

		table, err := r.ConvertToTable(context.Background(), eventList, nil)

		require.NoError(t, err)
		require.NotNil(t, table)
		assert.Len(t, table.ColumnDefinitions, 5)
		assert.Len(t, table.Rows, 1)
		assert.Equal(t, "Normal", table.Rows[0].Cells[1])
		assert.Equal(t, "Started", table.Rows[0].Cells[2])
	})

	t.Run("converts single event", func(t *testing.T) {
		r := NewREST(&mockBackend{})

		event := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "event-1",
				Namespace: "default",
			},
			Type:   "Warning",
			Reason: "Failed",
			InvolvedObject: corev1.ObjectReference{
				Kind: "Deployment",
				Name: "my-deployment",
			},
			Message:       "Failed to deploy",
			LastTimestamp: metav1.Now(),
		}

		table, err := r.ConvertToTable(context.Background(), event, nil)

		require.NoError(t, err)
		require.NotNil(t, table)
		assert.Len(t, table.Rows, 1)
		assert.Equal(t, "Warning", table.Rows[0].Cells[1])
		assert.Equal(t, "Failed", table.Rows[0].Cells[2])
	})
}

func TestREST_InterfaceImplementation(t *testing.T) {
	r := NewREST(&mockBackend{})

	// Verify interface implementations
	var _ rest.Scoper = r
	var _ rest.Creater = r
	var _ rest.Getter = r
	var _ rest.Lister = r
	var _ rest.Updater = r
	var _ rest.GracefulDeleter = r
	var _ rest.Watcher = r
	var _ rest.Storage = r
	var _ rest.SingularNameProvider = r

	assert.Equal(t, "event", r.GetSingularName())
	assert.True(t, r.NamespaceScoped())
}

// mockUpdatedObjectInfo implements rest.UpdatedObjectInfo for testing.
type mockUpdatedObjectInfo struct {
	obj *corev1.Event
}

func (m *mockUpdatedObjectInfo) Preconditions() *metav1.Preconditions {
	return nil
}

func (m *mockUpdatedObjectInfo) UpdatedObject(ctx context.Context, oldObj runtime.Object) (runtime.Object, error) {
	return m.obj, nil
}
