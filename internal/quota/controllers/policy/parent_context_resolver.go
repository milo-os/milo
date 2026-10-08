// Package policy implements controllers for quota policy management.
// It contains controllers for ClaimCreationPolicy and GrantCreationPolicy resources
// that validate policy configurations and manage grant creation workflows.
package policy

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

const (
	// DefaultClientTTL is the default TTL for cached clients (1 hour)
	DefaultClientTTL = 1 * time.Hour
)

// ParentContextResolver manages REST configurations for target control planes
// with minimal memory footprint for cross-cluster grant creation.
type ParentContextResolver struct {
	clientCache    map[string]*CachedClient
	mu             sync.RWMutex
	baseRestConfig *rest.Config
	scheme         *runtime.Scheme
	clientTTL      time.Duration
	cleanupCancel  context.CancelFunc
}

// CachedClient represents a cached client with TTL.
type CachedClient struct {
	client    client.Client
	createdAt time.Time
	ttl       time.Duration
}

// ParentContextResolverOptions configures the ParentContextResolver.
type ParentContextResolverOptions struct {
	ClientTTL time.Duration
}

// NewParentContextResolver creates a new ParentContextResolver.
// Scheme must include all resource types for parent context creation.
// Pass empty ParentContextResolverOptions{} for defaults.
func NewParentContextResolver(baseRestConfig *rest.Config, scheme *runtime.Scheme, opts ParentContextResolverOptions) *ParentContextResolver {
	if opts.ClientTTL == 0 {
		opts.ClientTTL = DefaultClientTTL
	}

	resolver := &ParentContextResolver{
		clientCache:    make(map[string]*CachedClient),
		baseRestConfig: baseRestConfig,
		scheme:         scheme,
		clientTTL:      opts.ClientTTL,
	}

	// Start background cleanup task
	resolver.startCleanupTask()

	return resolver
}

// ResolveClient resolves a client for the given parent context.
// It supports dynamic parent context types through registered handlers.
func (r *ParentContextResolver) ResolveClient(
	ctx context.Context,
	parentContext *ParentContextSpec,
	triggerObj *unstructured.Unstructured,
) (client.Client, error) {
	logger := log.FromContext(ctx).WithValues(
		"parentContextKind", parentContext.Kind,
		"parentContextAPIGroup", parentContext.APIGroup,
		"parentContextName", parentContext.Name,
	)

	// Create cache key
	cacheKey := fmt.Sprintf("%s/%s/%s",
		parentContext.APIGroup,
		parentContext.Kind,
		parentContext.Name,
	)

	// Check cache first
	r.mu.RLock()
	cachedClient, exists := r.clientCache[cacheKey]
	r.mu.RUnlock()

	if exists && time.Since(cachedClient.createdAt) < cachedClient.ttl {
		logger.V(2).Info("Using cached client for parent context")
		return cachedClient.client, nil
	}

	// Need to create or refresh the client
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check pattern - another goroutine might have updated the cache
	if cachedClient, exists := r.clientCache[cacheKey]; exists &&
		time.Since(cachedClient.createdAt) < cachedClient.ttl {
		return cachedClient.client, nil
	}

	// Create new client. A parent context that cannot be reached is an error
	// for the caller to report, not a reason to write into the local control
	// plane instead: a grant that lands there never reaches the project, and
	// the silent substitution would hide the resolution failure behind
	// whatever the local write happens to return.
	newClient, err := r.createClientForParentContext(ctx, parentContext, triggerObj)
	if err != nil {
		return nil, fmt.Errorf("failed to create client for parent context %s/%s %q: %w",
			parentContext.APIGroup, parentContext.Kind, parentContext.Name, err)
	}

	// Cache the client
	r.clientCache[cacheKey] = &CachedClient{
		client:    newClient,
		createdAt: time.Now(),
		ttl:       r.clientTTL,
	}

	logger.V(1).Info("Created and cached new client for parent context")
	return newClient, nil
}

// createClientForParentContext creates a new client for the given parent context.
// Currently supports Project parent contexts with room for future extension.
func (r *ParentContextResolver) createClientForParentContext(
	ctx context.Context,
	parentContext *ParentContextSpec,
	triggerObj *unstructured.Unstructured,
) (client.Client, error) {
	logger := log.FromContext(ctx).WithValues(
		"parentContextKind", parentContext.Kind,
		"parentContextAPIGroup", parentContext.APIGroup,
		"parentContextName", parentContext.Name,
		"triggerObject", triggerObj.GetName(),
	)

	// Validate parent context
	if err := r.validateParentContext(parentContext); err != nil {
		return nil, fmt.Errorf("parent context validation failed: %w", err)
	}

	cfg, err := r.createRestConfigForParent(parentContext)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config: %w", err)
	}

	targetClient, err := client.New(cfg, client.Options{
		Scheme: r.scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	logger.V(1).Info("Successfully created client for parent context")
	return targetClient, nil
}

// validateParentContext validates that the parent context is supported.
// Currently only supports Project parent contexts.
func (r *ParentContextResolver) validateParentContext(parentContext *ParentContextSpec) error {
	// Check if this is a Project
	if parentContext.APIGroup == resourcemanagerv1alpha1.GroupVersion.Group && parentContext.Kind == "Project" {
		// Basic validation - just ensure name is not empty
		if parentContext.Name == "" {
			return fmt.Errorf("project name cannot be empty")
		}
		return nil
	}

	// Future parent context types can be added here as needed
	return fmt.Errorf("unsupported parent context type: %s/%s (currently only Project is supported)", parentContext.APIGroup, parentContext.Kind)
}

// createRestConfigForParent creates a REST config for connecting to the parent context's control plane.
func (r *ParentContextResolver) createRestConfigForParent(parentContext *ParentContextSpec) (*rest.Config, error) {
	cfg := rest.CopyConfig(r.baseRestConfig)

	// Handle different parent context types
	if parentContext.APIGroup == resourcemanagerv1alpha1.GroupVersion.Group && parentContext.Kind == "Project" {
		return r.createProjectRestConfig(cfg, parentContext)
	}

	return nil, fmt.Errorf("unsupported parent context type: %s/%s", parentContext.APIGroup, parentContext.Kind)
}

// createProjectRestConfig creates a REST config for connecting to a Project's control plane.
func (r *ParentContextResolver) createProjectRestConfig(cfg *rest.Config, parentContext *ParentContextSpec) (*rest.Config, error) {
	// Parse the current host to modify the path
	apiHost, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("failed to parse host from rest config: %w", err)
	}

	// For Project resources, modify the path to point to the project's control plane
	// The host remains the same but the URL path becomes the project control plane path
	apiHost.Path = fmt.Sprintf("/apis/resourcemanager.miloapis.com/v1alpha1/projects/%s/control-plane", parentContext.Name)

	cfg.Host = apiHost.String()
	return cfg, nil
}

// startCleanupTask starts a background goroutine to periodically clean up expired clients.
func (r *ParentContextResolver) startCleanupTask() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cleanupCancel = cancel

	go func() {
		ticker := time.NewTicker(r.clientTTL)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.cleanupExpiredClients()
			}
		}
	}()
}

// cleanupExpiredClients removes expired clients from the cache.
func (r *ParentContextResolver) cleanupExpiredClients() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	for key, cachedClient := range r.clientCache {
		if now.Sub(cachedClient.createdAt) >= cachedClient.ttl {
			delete(r.clientCache, key)
		}
	}
}

// Close stops the background cleanup task and cleans up resources.
// Should be called when the resolver is no longer needed.
func (r *ParentContextResolver) Close() {
	if r.cleanupCancel != nil {
		r.cleanupCancel()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Clear the cache
	r.clientCache = make(map[string]*CachedClient)
}
