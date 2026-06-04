package events

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contextWithUser creates a context with the given user info.
func contextWithUser(u authuser.Info) context.Context {
	return apirequest.WithUser(context.Background(), u)
}

func TestInjectScopeAnnotations_ProjectScope(t *testing.T) {
	// User with project scope should have annotations added
	ctx := contextWithUser(&authuser.DefaultInfo{
		Name: "alice",
		Extra: map[string][]string{
			ExtraKeyParentType: {"Project"},
			ExtraKeyParentName: {"project-123"},
		},
	})
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "test-event"},
	}

	err := injectScopeAnnotations(ctx, event)

	require.NoError(t, err)
	assert.Equal(t, "Project", event.Annotations[ScopeTypeAnnotation])
	assert.Equal(t, "project-123", event.Annotations[ScopeNameAnnotation])
}

func TestInjectScopeAnnotations_OrganizationScope(t *testing.T) {
	// User with organization scope should have annotations added
	ctx := contextWithUser(&authuser.DefaultInfo{
		Name: "bob",
		Extra: map[string][]string{
			ExtraKeyParentType: {"Organization"},
			ExtraKeyParentName: {"org-456"},
		},
	})
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "test-event"},
	}

	err := injectScopeAnnotations(ctx, event)

	require.NoError(t, err)
	assert.Equal(t, "Organization", event.Annotations[ScopeTypeAnnotation])
	assert.Equal(t, "org-456", event.Annotations[ScopeNameAnnotation])
}

func TestInjectScopeAnnotations_PlatformScope(t *testing.T) {
	// Platform user (no scope extras) should have no annotations added
	ctx := contextWithUser(&authuser.DefaultInfo{
		Name: "system:serviceaccount:kube-system:default",
	})
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "test-event"},
	}

	err := injectScopeAnnotations(ctx, event)

	require.NoError(t, err)
	assert.Empty(t, event.Annotations)
}

func TestInjectScopeAnnotations_PreservesExistingAnnotations(t *testing.T) {
	// Existing annotations should be preserved, scope annotations added
	ctx := contextWithUser(&authuser.DefaultInfo{
		Name: "carol",
		Extra: map[string][]string{
			ExtraKeyParentType: {"Project"},
			ExtraKeyParentName: {"project-789"},
		},
	})
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-event",
			Annotations: map[string]string{
				"existing-key": "existing-value",
				"another-key":  "another-value",
			},
		},
	}

	err := injectScopeAnnotations(ctx, event)

	require.NoError(t, err)
	assert.Equal(t, "existing-value", event.Annotations["existing-key"])
	assert.Equal(t, "another-value", event.Annotations["another-key"])
	assert.Equal(t, "Project", event.Annotations[ScopeTypeAnnotation])
	assert.Equal(t, "project-789", event.Annotations[ScopeNameAnnotation])
}

func TestInjectScopeAnnotations_NoUserInContext(t *testing.T) {
	// Missing user in context should return error
	ctx := context.Background()
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "test-event"},
	}

	err := injectScopeAnnotations(ctx, event)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no user in context")
}

func TestInjectScopeAnnotations_PartialScope(t *testing.T) {
	// User with only parent type (missing name) should have no annotations added
	ctx := contextWithUser(&authuser.DefaultInfo{
		Name: "dave",
		Extra: map[string][]string{
			ExtraKeyParentType: {"Project"},
			// Missing ExtraKeyParentName
		},
	})
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "test-event"},
	}

	err := injectScopeAnnotations(ctx, event)

	require.NoError(t, err)
	assert.Empty(t, event.Annotations)
}

func TestInjectScopeAnnotations_OverwritesScope(t *testing.T) {
	// If event already has scope annotations, they should be overwritten with user's scope
	ctx := contextWithUser(&authuser.DefaultInfo{
		Name: "eve",
		Extra: map[string][]string{
			ExtraKeyParentType: {"Project"},
			ExtraKeyParentName: {"project-new"},
		},
	})
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-event",
			Annotations: map[string]string{
				ScopeTypeAnnotation: "Organization",
				ScopeNameAnnotation: "org-old",
			},
		},
	}

	err := injectScopeAnnotations(ctx, event)

	require.NoError(t, err)
	assert.Equal(t, "Project", event.Annotations[ScopeTypeAnnotation], "scope type should be overwritten")
	assert.Equal(t, "project-new", event.Annotations[ScopeNameAnnotation], "scope name should be overwritten")
}

func TestGetFirstExtra(t *testing.T) {
	extras := map[string][]string{
		"key1": {"value1", "value2"},
		"key2": {"single-value"},
		"key3": {},
	}

	assert.Equal(t, "value1", getFirstExtra(extras, "key1"))
	assert.Equal(t, "single-value", getFirstExtra(extras, "key2"))
	assert.Equal(t, "", getFirstExtra(extras, "key3"))
	assert.Equal(t, "", getFirstExtra(extras, "nonexistent"))
}

func TestNewDynamicProvider_RequiresURL(t *testing.T) {
	_, err := NewDynamicProvider(Config{
		// Missing ProviderURL
		CAFile:         "/path/to/ca.crt",
		ClientCertFile: "/path/to/client.crt",
		ClientKeyFile:  "/path/to/client.key",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ProviderURL is required")
}

func TestNewDynamicProvider_ValidConfig(t *testing.T) {
	cfg := Config{
		ProviderURL:    "https://activity-apiserver.activity-system.svc:443",
		CAFile:         "/etc/milo/activity/ca.crt",
		ClientCertFile: "/etc/milo/activity/tls.crt",
		ClientKeyFile:  "/etc/milo/activity/tls.key",
		Timeout:        30 * time.Second,
		Retries:        3,
		ExtrasAllow: map[string]struct{}{
			"iam.miloapis.com/parent-type": {},
			"iam.miloapis.com/parent-name": {},
		},
	}

	provider, err := NewDynamicProvider(cfg)

	require.NoError(t, err)
	assert.NotNil(t, provider)
	assert.Equal(t, "https://activity-apiserver.activity-system.svc:443", provider.base.Host)
	assert.Equal(t, "activity-apiserver.activity-system.svc", provider.base.ServerName)
	assert.Equal(t, "/etc/milo/activity/ca.crt", provider.base.CAFile)
	assert.Equal(t, "/etc/milo/activity/tls.crt", provider.base.CertFile)
	assert.Equal(t, "/etc/milo/activity/tls.key", provider.base.KeyFile)
	assert.False(t, provider.base.Insecure)
	assert.Equal(t, 3, provider.retries)
	assert.Equal(t, 30*time.Second, provider.to)
}

func TestFilterExtras(t *testing.T) {
	provider := &DynamicProvider{
		allowExtras: map[string]struct{}{
			"iam.miloapis.com/parent-type": {},
			"iam.miloapis.com/parent-name": {},
		},
	}

	src := map[string][]string{
		"iam.miloapis.com/parent-type": {"Project"},
		"iam.miloapis.com/parent-name": {"project-123"},
		"other-key":                    {"should-be-filtered"},
		"another-key":                  {"also-filtered"},
	}

	result := provider.filterExtras(src)

	assert.Len(t, result, 2)
	assert.Equal(t, []string{"Project"}, result["iam.miloapis.com/parent-type"])
	assert.Equal(t, []string{"project-123"}, result["iam.miloapis.com/parent-name"])
	assert.NotContains(t, result, "other-key")
	assert.NotContains(t, result, "another-key")
}

func TestFilterExtras_EmptyAllowList(t *testing.T) {
	provider := &DynamicProvider{
		allowExtras: map[string]struct{}{},
	}

	src := map[string][]string{
		"key1": {"value1"},
		"key2": {"value2"},
	}

	result := provider.filterExtras(src)

	assert.Nil(t, result)
}

func TestFilterExtras_EmptySource(t *testing.T) {
	provider := &DynamicProvider{
		allowExtras: map[string]struct{}{
			"key1": {},
		},
	}

	result := provider.filterExtras(map[string][]string{})

	assert.Nil(t, result)
}

func TestIsTransient(t *testing.T) {
	gr := schema.GroupResource{Group: "", Resource: "events"}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error is not transient",
			err:      nil,
			expected: false,
		},
		{
			name:     "timeout error is transient",
			err:      apierrors.NewTimeoutError("timeout", 5),
			expected: true,
		},
		{
			name:     "server timeout is transient",
			err:      apierrors.NewServerTimeout(gr, "get", 5),
			expected: true,
		},
		{
			name:     "service unavailable is transient",
			err:      apierrors.NewServiceUnavailable("service down"),
			expected: true,
		},
		{
			name:     "internal error is transient",
			err:      apierrors.NewInternalError(errors.New("internal error")),
			expected: true,
		},
		{
			name:     "too many requests is transient",
			err:      apierrors.NewTooManyRequests("rate limited", 30),
			expected: true,
		},
		{
			name:     "not found error is not transient",
			err:      apierrors.NewNotFound(gr, "event-name"),
			expected: false,
		},
		{
			name:     "bad request error is not transient",
			err:      apierrors.NewBadRequest("invalid input"),
			expected: false,
		},
		{
			name:     "forbidden error is not transient",
			err:      apierrors.NewForbidden(gr, "event-name", errors.New("denied")),
			expected: false,
		},
		{
			name:     "unauthorized error is not transient",
			err:      apierrors.NewUnauthorized("not authenticated"),
			expected: false,
		},
		{
			name:     "conflict error is not transient",
			err:      apierrors.NewConflict(gr, "event-name", errors.New("conflict")),
			expected: false,
		},
		{
			name: "network timeout error is transient",
			err: &net.OpError{
				Op:  "read",
				Err: &timeoutError{},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTransient(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// timeoutError is a mock net.Error that returns true for Timeout().
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }
