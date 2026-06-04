package events

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventLifecycle tests the complete lifecycle of an event through the REST API.
func TestEventLifecycle(t *testing.T) {
	backend := &mockBackend{
		createFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
			// Simulate backend storing the event
			return event.DeepCopy(), nil
		},
		getFunc: func(ctx context.Context, namespace, name string) (*corev1.Event, error) {
			return &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
					Annotations: map[string]string{
						ScopeTypeAnnotation: "Project",
						ScopeNameAnnotation: "test-project",
					},
				},
				Reason:  "Started",
				Message: "Container started",
			}, nil
		},
		updateFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
			return event.DeepCopy(), nil
		},
		deleteFunc: func(ctx context.Context, namespace, name string, opts *metav1.DeleteOptions) error {
			return nil
		},
	}

	r := NewREST(backend)

	user := &authuser.DefaultInfo{
		Name: "test-user",
		Extra: map[string][]string{
			ExtraKeyParentType: {"Project"},
			ExtraKeyParentName: {"test-project"},
		},
	}
	ctx := apirequest.WithUser(context.Background(), user)
	ctx = apirequest.WithNamespace(ctx, "default")

	// 1. Create event
	createEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: "lifecycle-test-event",
		},
		Reason:  "Started",
		Message: "Container started",
	}

	created, err := r.Create(ctx, createEvent, nil, &metav1.CreateOptions{})
	require.NoError(t, err)
	createdEvent := created.(*corev1.Event)
	assert.Equal(t, "Project", createdEvent.Annotations[ScopeTypeAnnotation])
	assert.Equal(t, "test-project", createdEvent.Annotations[ScopeNameAnnotation])

	// 2. Get event
	retrieved, err := r.Get(ctx, "lifecycle-test-event", &metav1.GetOptions{})
	require.NoError(t, err)
	retrievedEvent := retrieved.(*corev1.Event)
	assert.Equal(t, "lifecycle-test-event", retrievedEvent.Name)
	assert.Equal(t, "Started", retrievedEvent.Reason)

	// 3. Update event
	updatedEvent := retrievedEvent.DeepCopy()
	updatedEvent.Message = "Container updated"
	objInfo := &mockUpdatedObjectInfo{obj: updatedEvent}

	updated, wasCreated, err := r.Update(ctx, "lifecycle-test-event", objInfo, nil, nil, false, &metav1.UpdateOptions{})
	require.NoError(t, err)
	assert.False(t, wasCreated)
	updatedResult := updated.(*corev1.Event)
	assert.Equal(t, "Container updated", updatedResult.Message)
	// Verify scope was re-injected
	assert.Equal(t, "Project", updatedResult.Annotations[ScopeTypeAnnotation])

	// 4. Delete event
	deleted, immediate, err := r.Delete(ctx, "lifecycle-test-event", nil, &metav1.DeleteOptions{})
	require.NoError(t, err)
	assert.True(t, immediate)
	assert.NotNil(t, deleted)
}

// TestMultiTenantScenarios tests various multi-tenant isolation scenarios.
func TestMultiTenantScenarios(t *testing.T) {
	t.Run("different users different scopes", func(t *testing.T) {
		backend := &mockBackend{
			createFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
				return event.DeepCopy(), nil
			},
		}

		r := NewREST(backend)

		// User in Project A
		userA := &authuser.DefaultInfo{
			Name: "user-a",
			Extra: map[string][]string{
				ExtraKeyParentType: {"Project"},
				ExtraKeyParentName: {"project-a"},
			},
		}
		ctxA := apirequest.WithUser(context.Background(), userA)
		ctxA = apirequest.WithNamespace(ctxA, "default")

		// User in Project B
		userB := &authuser.DefaultInfo{
			Name: "user-b",
			Extra: map[string][]string{
				ExtraKeyParentType: {"Project"},
				ExtraKeyParentName: {"project-b"},
			},
		}
		ctxB := apirequest.WithUser(context.Background(), userB)
		ctxB = apirequest.WithNamespace(ctxB, "default")

		// Create event as User A
		eventA := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "event-a"},
		}
		createdA, err := r.Create(ctxA, eventA, nil, &metav1.CreateOptions{})
		require.NoError(t, err)
		resultA := createdA.(*corev1.Event)
		assert.Equal(t, "project-a", resultA.Annotations[ScopeNameAnnotation])

		// Create event as User B
		eventB := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "event-b"},
		}
		createdB, err := r.Create(ctxB, eventB, nil, &metav1.CreateOptions{})
		require.NoError(t, err)
		resultB := createdB.(*corev1.Event)
		assert.Equal(t, "project-b", resultB.Annotations[ScopeNameAnnotation])

		// Verify events have different scopes
		assert.NotEqual(t, resultA.Annotations[ScopeNameAnnotation], resultB.Annotations[ScopeNameAnnotation])
	})

	t.Run("organization vs project scope", func(t *testing.T) {
		backend := &mockBackend{
			createFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
				return event.DeepCopy(), nil
			},
		}

		r := NewREST(backend)

		// Organization-scoped user
		orgUser := &authuser.DefaultInfo{
			Name: "org-user",
			Extra: map[string][]string{
				ExtraKeyParentType: {"Organization"},
				ExtraKeyParentName: {"my-org"},
			},
		}
		orgCtx := apirequest.WithUser(context.Background(), orgUser)
		orgCtx = apirequest.WithNamespace(orgCtx, "default")

		// Project-scoped user
		projUser := &authuser.DefaultInfo{
			Name: "proj-user",
			Extra: map[string][]string{
				ExtraKeyParentType: {"Project"},
				ExtraKeyParentName: {"my-project"},
			},
		}
		projCtx := apirequest.WithUser(context.Background(), projUser)
		projCtx = apirequest.WithNamespace(projCtx, "default")

		// Create event as org user
		orgEvent := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "org-event"},
		}
		createdOrg, err := r.Create(orgCtx, orgEvent, nil, &metav1.CreateOptions{})
		require.NoError(t, err)
		resultOrg := createdOrg.(*corev1.Event)
		assert.Equal(t, "Organization", resultOrg.Annotations[ScopeTypeAnnotation])
		assert.Equal(t, "my-org", resultOrg.Annotations[ScopeNameAnnotation])

		// Create event as project user
		projEvent := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "proj-event"},
		}
		createdProj, err := r.Create(projCtx, projEvent, nil, &metav1.CreateOptions{})
		require.NoError(t, err)
		resultProj := createdProj.(*corev1.Event)
		assert.Equal(t, "Project", resultProj.Annotations[ScopeTypeAnnotation])
		assert.Equal(t, "my-project", resultProj.Annotations[ScopeNameAnnotation])
	})
}

// TestScopeImmutability verifies that scope cannot be tampered with.
func TestScopeImmutability(t *testing.T) {
	t.Run("create with pre-existing scope annotations", func(t *testing.T) {
		backend := &mockBackend{
			createFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
				return event.DeepCopy(), nil
			},
		}

		r := NewREST(backend)

		user := &authuser.DefaultInfo{
			Name: "alice",
			Extra: map[string][]string{
				ExtraKeyParentType: {"Project"},
				ExtraKeyParentName: {"project-real"},
			},
		}
		ctx := apirequest.WithUser(context.Background(), user)
		ctx = apirequest.WithNamespace(ctx, "default")

		// Malicious client tries to set different scope
		event := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name: "tampered-event",
				Annotations: map[string]string{
					ScopeTypeAnnotation: "Organization",
					ScopeNameAnnotation: "malicious-org",
				},
			},
		}

		created, err := r.Create(ctx, event, nil, &metav1.CreateOptions{})
		require.NoError(t, err)

		result := created.(*corev1.Event)
		// Verify scope was overwritten with actual user scope
		assert.Equal(t, "Project", result.Annotations[ScopeTypeAnnotation])
		assert.Equal(t, "project-real", result.Annotations[ScopeNameAnnotation])
	})

	t.Run("update cannot change scope", func(t *testing.T) {
		backend := &mockBackend{
			updateFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
				return event.DeepCopy(), nil
			},
		}

		r := NewREST(backend)

		user := &authuser.DefaultInfo{
			Name: "alice",
			Extra: map[string][]string{
				ExtraKeyParentType: {"Project"},
				ExtraKeyParentName: {"project-real"},
			},
		}
		ctx := apirequest.WithUser(context.Background(), user)
		ctx = apirequest.WithNamespace(ctx, "default")

		// Event with tampered scope in update
		event := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name: "event-to-update",
				Annotations: map[string]string{
					ScopeTypeAnnotation: "Platform", // Trying to escalate
					ScopeNameAnnotation: "",
					"custom-key":        "custom-value",
				},
			},
			Message: "Updated message",
		}

		objInfo := &mockUpdatedObjectInfo{obj: event}

		updated, _, err := r.Update(ctx, "event-to-update", objInfo, nil, nil, false, &metav1.UpdateOptions{})
		require.NoError(t, err)

		result := updated.(*corev1.Event)
		// Verify scope was re-injected correctly
		assert.Equal(t, "Project", result.Annotations[ScopeTypeAnnotation])
		assert.Equal(t, "project-real", result.Annotations[ScopeNameAnnotation])
		// Verify other annotations preserved
		assert.Equal(t, "custom-value", result.Annotations["custom-key"])
	})
}

// TestEdgeCases tests various edge cases and error conditions.
func TestEdgeCases(t *testing.T) {
	t.Run("event with nil annotations", func(t *testing.T) {
		backend := &mockBackend{
			createFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
				return event.DeepCopy(), nil
			},
		}

		r := NewREST(backend)

		user := &authuser.DefaultInfo{
			Name: "alice",
			Extra: map[string][]string{
				ExtraKeyParentType: {"Project"},
				ExtraKeyParentName: {"project-123"},
			},
		}
		ctx := apirequest.WithUser(context.Background(), user)
		ctx = apirequest.WithNamespace(ctx, "default")

		event := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nil-annotations",
				// Annotations is nil
			},
		}

		created, err := r.Create(ctx, event, nil, &metav1.CreateOptions{})
		require.NoError(t, err)

		result := created.(*corev1.Event)
		assert.NotNil(t, result.Annotations, "annotations map should be initialized")
		assert.Equal(t, "Project", result.Annotations[ScopeTypeAnnotation])
	})

	t.Run("event with empty extra values", func(t *testing.T) {
		backend := &mockBackend{
			createFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
				return event.DeepCopy(), nil
			},
		}

		r := NewREST(backend)

		user := &authuser.DefaultInfo{
			Name: "alice",
			Extra: map[string][]string{
				ExtraKeyParentType: {}, // Empty slice
				ExtraKeyParentName: {"project-123"},
			},
		}
		ctx := apirequest.WithUser(context.Background(), user)
		ctx = apirequest.WithNamespace(ctx, "default")

		event := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "empty-extras"},
		}

		created, err := r.Create(ctx, event, nil, &metav1.CreateOptions{})
		require.NoError(t, err)

		result := created.(*corev1.Event)
		// Should not inject scope if any required field is empty
		assert.Empty(t, result.Annotations[ScopeTypeAnnotation])
		assert.Empty(t, result.Annotations[ScopeNameAnnotation])
	})

	t.Run("service account without scope", func(t *testing.T) {
		backend := &mockBackend{
			createFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
				return event.DeepCopy(), nil
			},
		}

		r := NewREST(backend)

		// Service account with no parent context
		user := &authuser.DefaultInfo{
			Name: "system:serviceaccount:kube-system:default",
			Groups: []string{
				"system:serviceaccounts",
				"system:serviceaccounts:kube-system",
			},
		}
		ctx := apirequest.WithUser(context.Background(), user)
		ctx = apirequest.WithNamespace(ctx, "kube-system")

		event := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "sa-event"},
		}

		created, err := r.Create(ctx, event, nil, &metav1.CreateOptions{})
		require.NoError(t, err)

		result := created.(*corev1.Event)
		// Service accounts without scope should not have annotations
		assert.Empty(t, result.Annotations[ScopeTypeAnnotation])
		assert.Empty(t, result.Annotations[ScopeNameAnnotation])
	})
}

// TestConcurrentOperations tests concurrent event operations.
func TestConcurrentOperations(t *testing.T) {
	backend := &mockBackend{
		createFunc: func(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
			// Simulate some processing time
			time.Sleep(10 * time.Millisecond)
			return event.DeepCopy(), nil
		},
	}

	r := NewREST(backend)

	// Create multiple users with different scopes
	users := []*authuser.DefaultInfo{
		{
			Name: "user-1",
			Extra: map[string][]string{
				ExtraKeyParentType: {"Project"},
				ExtraKeyParentName: {"project-1"},
			},
		},
		{
			Name: "user-2",
			Extra: map[string][]string{
				ExtraKeyParentType: {"Project"},
				ExtraKeyParentName: {"project-2"},
			},
		},
		{
			Name: "user-3",
			Extra: map[string][]string{
				ExtraKeyParentType: {"Organization"},
				ExtraKeyParentName: {"org-1"},
			},
		},
	}

	// Create events concurrently
	type result struct {
		event *corev1.Event
		err   error
	}
	results := make(chan result, len(users))

	for i, user := range users {
		go func(idx int, u authuser.Info) {
			ctx := apirequest.WithUser(context.Background(), u)
			ctx = apirequest.WithNamespace(ctx, "default")

			event := &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name: "concurrent-event-" + u.GetName(),
				},
			}

			created, err := r.Create(ctx, event, nil, &metav1.CreateOptions{})
			if err != nil {
				results <- result{nil, err}
				return
			}
			results <- result{created.(*corev1.Event), nil}
		}(i, user)
	}

	// Collect results
	for i := 0; i < len(users); i++ {
		res := <-results
		require.NoError(t, res.err)
		require.NotNil(t, res.event)

		// Verify each event has the correct scope
		assert.NotEmpty(t, res.event.Annotations[ScopeTypeAnnotation])
		assert.NotEmpty(t, res.event.Annotations[ScopeNameAnnotation])
	}
}

// TestTableConversion tests the table conversion for kubectl output.
func TestTableConversion(t *testing.T) {
	r := NewREST(&mockBackend{})

	t.Run("handles events with various timestamp formats", func(t *testing.T) {
		now := metav1.Now()
		eventTime := metav1.NewMicroTime(time.Now())

		events := &corev1.EventList{
			Items: []corev1.Event{
				{
					ObjectMeta:    metav1.ObjectMeta{Name: "event-1"},
					LastTimestamp: now,
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "event-2"},
					EventTime:  eventTime,
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "event-3"},
					// No timestamp
				},
			},
		}

		table, err := r.ConvertToTable(context.Background(), events, nil)

		require.NoError(t, err)
		require.NotNil(t, table)
		assert.Len(t, table.Rows, 3)

		// First event should have LastTimestamp formatted
		assert.NotEqual(t, "<unknown>", table.Rows[0].Cells[0])

		// Second event should have EventTime formatted
		assert.NotEqual(t, "<unknown>", table.Rows[1].Cells[0])

		// Third event should show unknown
		assert.Equal(t, "<unknown>", table.Rows[2].Cells[0])
	})

	t.Run("handles involved object references correctly", func(t *testing.T) {
		events := &corev1.EventList{
			Items: []corev1.Event{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "same-ns-event",
						Namespace: "default",
					},
					InvolvedObject: corev1.ObjectReference{
						Kind:      "Pod",
						Name:      "my-pod",
						Namespace: "default",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "diff-ns-event",
						Namespace: "default",
					},
					InvolvedObject: corev1.ObjectReference{
						Kind:      "Pod",
						Name:      "other-pod",
						Namespace: "other-namespace",
					},
				},
			},
		}

		table, err := r.ConvertToTable(context.Background(), events, nil)

		require.NoError(t, err)
		assert.Len(t, table.Rows, 2)

		// Same namespace should not include namespace prefix
		assert.Equal(t, "Pod/my-pod", table.Rows[0].Cells[3])

		// Different namespace should include namespace prefix
		assert.Equal(t, "other-namespace/Pod/other-pod", table.Rows[1].Cells[3])
	})
}

// TestREST_MetadataAccessors verifies the REST storage metadata accessors.
func TestREST_MetadataAccessors(t *testing.T) {
	r := NewREST(&mockBackend{})

	t.Run("singular name", func(t *testing.T) {
		assert.Equal(t, "event", r.GetSingularName())
	})

	t.Run("namespace scoped", func(t *testing.T) {
		assert.True(t, r.NamespaceScoped())
	})

	t.Run("new object", func(t *testing.T) {
		obj := r.New()
		_, ok := obj.(*corev1.Event)
		assert.True(t, ok, "New() should return *corev1.Event")
	})

	t.Run("new list", func(t *testing.T) {
		list := r.NewList()
		_, ok := list.(*corev1.EventList)
		assert.True(t, ok, "NewList() should return *corev1.EventList")
	})
}

// Helper function to create a minimal event for testing.
func newTestEvent(name string, opts ...func(*corev1.Event)) *corev1.Event {
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod",
			Name: "test-pod",
		},
		Reason:  "Test",
		Message: "Test event",
		Type:    corev1.EventTypeNormal,
	}

	for _, opt := range opts {
		opt(event)
	}

	return event
}

func withNamespace(ns string) func(*corev1.Event) {
	return func(e *corev1.Event) {
		e.Namespace = ns
	}
}

func withAnnotations(annotations map[string]string) func(*corev1.Event) {
	return func(e *corev1.Event) {
		e.Annotations = annotations
	}
}

func withEventType(eventType string) func(*corev1.Event) {
	return func(e *corev1.Event) {
		e.Type = eventType
	}
}

// Ensure the mock backend implements all required methods.
var _ Backend = (*mockBackend)(nil)

// Ensure REST implements all required interfaces.
var (
	_ rest.Scoper               = (*REST)(nil)
	_ rest.Creater              = (*REST)(nil)
	_ rest.Getter               = (*REST)(nil)
	_ rest.Lister               = (*REST)(nil)
	_ rest.Updater              = (*REST)(nil)
	_ rest.GracefulDeleter      = (*REST)(nil)
	_ rest.Watcher              = (*REST)(nil)
	_ rest.Storage              = (*REST)(nil)
	_ rest.SingularNameProvider = (*REST)(nil)
)

// Ensure mockUpdatedObjectInfo implements rest.UpdatedObjectInfo.
var _ rest.UpdatedObjectInfo = (*mockUpdatedObjectInfo)(nil)
