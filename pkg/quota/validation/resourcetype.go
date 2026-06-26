package validation

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/log"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// claimingRules represents the claiming rules for a specific resource type
type claimingRules struct {
	resourceType      string
	consumerType      quotav1alpha1.ConsumerType
	claimingResources []quotav1alpha1.ClaimingResource
	registrationName  string // For error messages
}

// ResourceTypeValidator provides an interface for validating resource types against ResourceRegistrations.
// The validator uses a shared informer to cache claiming permissions for fast O(1) lookups.
type ResourceTypeValidator interface {
	ValidateResourceType(ctx context.Context, resourceType string) error

	// This performs a cached lookup and validates claiming permissions without exposing the method on the API type.
	// Returns allowed status and detailed error message information for user-friendly feedback.
	IsClaimingResourceAllowed(ctx context.Context, resourceType string, consumerRef quotav1alpha1.ConsumerRef, claimingAPIGroup, claimingKind string) (bool, []string, error)

	// IsResourceTypeRegistered checks if a resourceType is already registered.
	IsResourceTypeRegistered(resourceType string) bool

	// HasSynced returns true if the validator's cache has been synced with the API server.
	// This can be used for readiness checks to ensure the validator is ready before serving traffic.
	HasSynced() bool
}

// resourceTypeValidator implements ResourceTypeValidator using a shared informer for caching.
type resourceTypeValidator struct {
	logger        logr.Logger
	dynamicClient dynamic.Interface
	informer      cache.SharedIndexInformer

	// Cache for fast lookups
	cacheMutex sync.RWMutex
	cache      map[string]*claimingRules // resourceType -> claiming rules

	// Sync state tracking for readiness checks
	syncMutex sync.RWMutex
	synced    bool
}

// NewResourceTypeValidator creates a new ResourceTypeValidator with async initialization.
// This is the recommended approach as it prevents blocking during startup.
// The validator will return immediately and sync in the background.
//
// The validator will retry indefinitely with exponential backoff (max 30s) until successful.
// This design prevents API server crash loops when the validator is created before the API
// server is fully started, which is necessary for admission plugin initialization.
func NewResourceTypeValidator(dynamicClient dynamic.Interface) ResourceTypeValidator {
	logger := log.Log.WithName("resource-type-validator")
	logger.Info("Creating ResourceTypeValidator with delayed start")

	validator := &resourceTypeValidator{
		logger:        logger,
		dynamicClient: dynamicClient,
		informer:      nil, // Will be initialized later
		cache:         make(map[string]*claimingRules),
		synced:        false,
	}

	// Start initialization in background with infinite retry
	go func() {
		attempt := 0

		logger.Info("Starting ResourceTypeValidator initialization with infinite retry")

		for {
			attempt++
			logger.V(1).Info("Attempting to initialize ResourceTypeValidator informer", "attempt", attempt)

			if err := validator.tryInitializeInformer(); err != nil {
				logger.Error(err, "Failed to initialize ResourceTypeValidator informer, will retry", "attempt", attempt)

				// Calculate exponential backoff delay with cap
				delay := time.Duration(attempt) * 5 * time.Second
				if delay > 30*time.Second {
					delay = 30 * time.Second
				}

				logger.V(1).Info("Waiting before retry", "delay", delay, "attempt", attempt)
				time.Sleep(delay)
				continue
			}

			// Success! Stop retrying
			logger.Info("ResourceTypeValidator cache synced successfully", "attempt", attempt)

			// Mark as synced for readiness checks
			validator.syncMutex.Lock()
			validator.synced = true
			validator.syncMutex.Unlock()

			break
		}
	}()

	logger.Info("ResourceTypeValidator created, will initialize when API server is ready")
	return validator
}

// HasSynced returns true if the validator's cache has been synced with the API server.
// This method is safe for concurrent use and can be used in readiness checks.
func (v *resourceTypeValidator) HasSynced() bool {
	v.syncMutex.RLock()
	defer v.syncMutex.RUnlock()
	return v.synced
}

// tryInitializeInformer attempts to create and sync the informer, returning an error if it fails
func (v *resourceTypeValidator) tryInitializeInformer() error {
	gvr := schema.GroupVersionResource{
		Group:    quotav1alpha1.GroupVersion.Group,
		Version:  quotav1alpha1.GroupVersion.Version,
		Resource: "resourceregistrations",
	}

	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return v.dynamicClient.Resource(gvr).List(context.Background(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return v.dynamicClient.Resource(gvr).Watch(context.Background(), options)
		},
	}

	informer := cache.NewSharedIndexInformer(
		lw,
		&unstructured.Unstructured{},
		10*time.Minute,
		cache.Indexers{
			"resourceType": func(obj interface{}) ([]string, error) {
				if unstrObj, ok := obj.(*unstructured.Unstructured); ok {
					resourceType, found, err := unstructured.NestedString(unstrObj.Object, "spec", "resourceType")
					if err != nil {
						return nil, fmt.Errorf("failed to extract resource type: %w", err)
					} else if !found {
						return nil, fmt.Errorf("failed to extract resourceType from ResourceRegistration")
					}
					return []string{resourceType}, nil
				}
				return nil, fmt.Errorf("object is not an unstructured object")
			},
		},
	)

	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    v.onResourceRegistrationAdd,
		UpdateFunc: v.onResourceRegistrationUpdate,
		DeleteFunc: v.onResourceRegistrationDelete,
	}); err != nil {
		return fmt.Errorf("failed to add event handler: %w", err)
	}

	v.cacheMutex.Lock()
	v.informer = informer
	v.cacheMutex.Unlock()

	// Start the informer
	informerStopCh := make(chan struct{})
	go informer.Run(informerStopCh)

	// Wait for sync with timeout
	syncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	syncDone := make(chan bool, 1)
	go func() {
		syncDone <- cache.WaitForCacheSync(informerStopCh, informer.HasSynced)
	}()

	select {
	case <-syncCtx.Done():
		close(informerStopCh)
		return fmt.Errorf("timeout waiting for cache sync")
	case synced := <-syncDone:
		if !synced {
			close(informerStopCh)
			return fmt.Errorf("failed to sync cache")
		}
	}

	// Success - informer is running and synced
	return nil
}

// ValidateResourceType validates using the cached data.
// The cache only contains active ResourceRegistrations, so this is a simple lookup.
func (v *resourceTypeValidator) ValidateResourceType(ctx context.Context, resourceType string) error {
	v.cacheMutex.RLock()
	defer v.cacheMutex.RUnlock()

	_, exists := v.cache[resourceType]
	if !exists {
		//lint:ignore ST1005 "Error message intentionally capitalized for user-facing display"
		return fmt.Errorf("Resource type '%s' is not available for quota management. Enable quota tracking for this resource type by registering it with the quota system", resourceType)
	}

	return nil
}

func (v *resourceTypeValidator) IsResourceTypeRegistered(resourceType string) bool {
	v.cacheMutex.RLock()
	defer v.cacheMutex.RUnlock()

	_, exists := v.cache[resourceType]
	return exists
}

// IsClaimingResourceAllowed checks if the given resource type is allowed to claim quota for the specified resource type.
func (v *resourceTypeValidator) IsClaimingResourceAllowed(ctx context.Context, resourceType string, consumerRef quotav1alpha1.ConsumerRef, claimingAPIGroup, claimingKind string) (bool, []string, error) {
	v.cacheMutex.RLock()
	defer v.cacheMutex.RUnlock()

	rules, exists := v.cache[resourceType]
	if !exists {
		return false, nil, fmt.Errorf("no ResourceRegistration found for resource type %s", resourceType)
	}

	// Verify this registration is for the correct consumer type
	if rules.consumerType.APIGroup != consumerRef.APIGroup ||
		rules.consumerType.Kind != consumerRef.Kind {
		return false, nil, fmt.Errorf("consumer type mismatch for resource type %s: expected %s/%s, got %s/%s",
			resourceType,
			rules.consumerType.APIGroup, rules.consumerType.Kind,
			consumerRef.APIGroup, consumerRef.Kind,
		)
	}

	if len(rules.claimingResources) == 0 {
		// When not specified, deny by default for security
		return false, nil, nil // No allowed resources configured
	}

	// Build allowed list for error messages
	var allowedList []string
	for _, allowedResource := range rules.claimingResources {
		if allowedResource.APIGroup == "" {
			allowedList = append(allowedList, fmt.Sprintf("core/%s", allowedResource.Kind))
		} else {
			allowedList = append(allowedList, fmt.Sprintf("%s/%s", allowedResource.APIGroup, allowedResource.Kind))
		}

		if allowedResource.APIGroup == claimingAPIGroup &&
			strings.EqualFold(allowedResource.Kind, claimingKind) {
			return true, allowedList, nil // Match found
		}
	}

	return false, allowedList, nil // Not allowed
}

// Event handlers for cache maintenance
func (v *resourceTypeValidator) onResourceRegistrationAdd(obj interface{}) {
	reg := v.convertToResourceRegistration(obj)
	if reg == nil {
		v.logger.Error(nil, "Received invalid object in Add handler")
		return
	}

	v.updateCacheForRegistration(reg)
}

func (v *resourceTypeValidator) onResourceRegistrationUpdate(oldObj, newObj interface{}) {
	reg := v.convertToResourceRegistration(newObj)
	if reg == nil {
		v.logger.Error(nil, "Received invalid object in Update handler")
		return
	}

	v.updateCacheForRegistration(reg)
}

func (v *resourceTypeValidator) onResourceRegistrationDelete(obj interface{}) {
	reg := v.convertToResourceRegistration(obj)
	if reg == nil {
		// Handle DeletedFinalStateUnknown
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			reg = v.convertToResourceRegistration(tombstone.Obj)
			if reg == nil {
				v.logger.Error(nil, "Received invalid object in Delete handler tombstone")
				return
			}
		} else {
			v.logger.Error(nil, "Received invalid object in Delete handler")
			return
		}
	}

	v.cacheMutex.Lock()
	delete(v.cache, reg.Spec.ResourceType)
	v.cacheMutex.Unlock()

	v.logger.V(1).Info("Removed ResourceRegistration from cache", "resourceType", reg.Spec.ResourceType)
}

// convertToResourceRegistration converts an unstructured object to a ResourceRegistration
func (v *resourceTypeValidator) convertToResourceRegistration(obj interface{}) *quotav1alpha1.ResourceRegistration {
	unstrObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}

	var reg quotav1alpha1.ResourceRegistration
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstrObj.Object, &reg); err != nil {
		v.logger.Error(err, "Failed to convert unstructured object to ResourceRegistration")
		return nil
	}

	return &reg
}

// updateCacheForRegistration updates the cache based on ResourceRegistration status.
// Only active ResourceRegistrations are kept in the cache for fast validation.
func (v *resourceTypeValidator) updateCacheForRegistration(reg *quotav1alpha1.ResourceRegistration) {
	resourceType := reg.Spec.ResourceType

	activeCondition := apimeta.FindStatusCondition(reg.Status.Conditions, quotav1alpha1.ResourceRegistrationActive)
	isActive := activeCondition != nil && activeCondition.Status == metav1.ConditionTrue

	v.cacheMutex.Lock()
	defer v.cacheMutex.Unlock()

	if isActive {
		rules := &claimingRules{
			resourceType:      resourceType,
			consumerType:      reg.Spec.ConsumerType,
			claimingResources: make([]quotav1alpha1.ClaimingResource, len(reg.Spec.ClaimingResources)),
			registrationName:  reg.Name,
		}
		copy(rules.claimingResources, reg.Spec.ClaimingResources)

		v.cache[resourceType] = rules
		v.logger.V(1).Info("Updated active ResourceRegistration in cache",
			"resourceType", resourceType,
			"consumerType", fmt.Sprintf("%s/%s", reg.Spec.ConsumerType.APIGroup, reg.Spec.ConsumerType.Kind))
	} else {
		if _, exists := v.cache[resourceType]; exists {
			delete(v.cache, resourceType)
			v.logger.V(1).Info("Removed inactive ResourceRegistration from cache",
				"resourceType", resourceType,
				"consumerType", fmt.Sprintf("%s/%s", reg.Spec.ConsumerType.APIGroup, reg.Spec.ConsumerType.Kind),
				"reason", "not active")
		}
	}
}
