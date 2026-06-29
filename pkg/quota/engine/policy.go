package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/component-base/metrics"
	legacyregistry "k8s.io/component-base/metrics/legacyregistry"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// Metrics for the policy engine shared informer. Registered once at init.
var (
	policyInformerEventsTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota",
			Name:           "policy_informer_events_total",
			Help:           "ClaimCreationPolicy events observed by the shared informer.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"type"}, // add|update|delete|unknown
	)

	policyLoadTotal = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota",
			Name:           "policy_load_total",
			Help:           "Total number of ClaimCreationPolicy load operations.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	policyActiveGauge = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      "milo_quota",
			Name:           "policies_active",
			Help:           "Current number of active ClaimCreationPolicy objects in the cache.",
			StabilityLevel: metrics.ALPHA,
		},
	)
)

func init() {
	// Register metrics with Kubernetes legacy registry so they are exposed on the apiserver /metrics.
	legacyregistry.MustRegister(policyInformerEventsTotal)
	legacyregistry.MustRegister(policyLoadTotal)
	legacyregistry.MustRegister(policyActiveGauge)
}

// PolicyEngine manages ClaimCreationPolicy resources and provides fast GVK-based lookups.
type PolicyEngine interface {
	// GetPolicyForGVK returns the active policy for a given GroupVersionKind.
	// Returns nil if no policy is found.
	GetPolicyForGVK(gvk schema.GroupVersionKind) (*quotav1alpha1.ClaimCreationPolicy, error)

	// Start begins the policy loading and watching process.
	Start(ctx context.Context) error

	// Close stops the policy engine and cleans up resources like watchers.
	Close()
}

// policyEngine implements PolicyEngine with shared informer support.
type policyEngine struct {
	dynamicClient dynamic.Interface
	logger        logr.Logger
	mu            sync.RWMutex
	gvkIndex      sync.Map // map[string]*quotav1alpha1.ClaimCreationPolicy
	initialized   bool

	// Shared informer management
	informer     cache.SharedIndexInformer
	stopCh       chan struct{}
	workqueue    workqueue.TypedRateLimitingInterface[types.NamespacedName]
	startOnce    sync.Once
	resyncPeriod time.Duration
}

// NewPolicyEngine creates a policy engine that uses shared informer for policy access.
// Call Start() to begin loading and watching policies.
func NewPolicyEngine(dynamicClient dynamic.Interface, logger logr.Logger) PolicyEngine {
	return &policyEngine{
		dynamicClient: dynamicClient,
		logger:        logger.WithName("policy-engine"),
		gvkIndex:      sync.Map{},
		initialized:   false,
		stopCh:        make(chan struct{}),
		resyncPeriod:  10 * time.Minute, // Default resync period
		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
			workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{
				Name: "quota_policy_engine",
			},
		),
	}
}

// Start begins the policy loading and watching process using shared informer.
func (e *policyEngine) Start(ctx context.Context) error {
	var startErr error
	e.startOnce.Do(func() {
		e.logger.Info("Starting policy engine with shared informer")

		// Create GVR for ClaimCreationPolicies
		gvr := schema.GroupVersionResource{
			Group:    quotav1alpha1.GroupVersion.Group,
			Version:  quotav1alpha1.GroupVersion.Version,
			Resource: "claimcreationpolicies",
		}

		// Create shared informer
		lw := &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return e.dynamicClient.Resource(gvr).List(ctx, options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return e.dynamicClient.Resource(gvr).Watch(ctx, options)
			},
		}

		e.informer = cache.NewSharedIndexInformer(
			lw,
			&unstructured.Unstructured{},
			e.resyncPeriod,
			cache.Indexers{},
		)

		// Add event handlers
		_, err := e.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				policyInformerEventsTotal.WithLabelValues("add").Inc()
				e.handlePolicyEvent(obj)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				policyInformerEventsTotal.WithLabelValues("update").Inc()
				e.handlePolicyEvent(newObj)
			},
			DeleteFunc: func(obj interface{}) {
				policyInformerEventsTotal.WithLabelValues("delete").Inc()
				e.handlePolicyEvent(obj)
			},
		})
		if err != nil {
			startErr = fmt.Errorf("failed to add event handler: %w", err)
			return
		}

		// Start the informer
		go e.informer.Run(e.stopCh)

		// Start the workqueue processor
		go e.processWorkItems(ctx)

		// Wait for informer cache to sync
		if !cache.WaitForCacheSync(e.stopCh, e.informer.HasSynced) {
			startErr = fmt.Errorf("timed out waiting for cache to sync")
			return
		}

		e.mu.Lock()
		e.initialized = true
		e.mu.Unlock()

		e.logger.Info("Policy engine started successfully with shared informer")
	})

	return startErr
}

// GetPolicyForGVK returns the active policy for a given GroupVersionKind.
func (e *policyEngine) GetPolicyForGVK(gvk schema.GroupVersionKind) (*quotav1alpha1.ClaimCreationPolicy, error) {
	e.logger.V(1).Info("Looking up policy for GVK", "gvk", gvk.String())

	if value, ok := e.gvkIndex.Load(gvk.String()); ok {
		if policy, ok := value.(*quotav1alpha1.ClaimCreationPolicy); ok {
			// Skip disabled policies
			if policy.Spec.Disabled != nil && *policy.Spec.Disabled {
				return nil, nil // Policy exists but is disabled
			}
			e.logger.V(1).Info("Found policy for GVK", "gvk", gvk.String(), "policy", policy.Name)
			return policy, nil
		}
	}

	e.logger.V(3).Info("No policy found for GVK", "gvk", gvk.String())
	return nil, nil // No policy found for this GVK
}

// handlePolicyEvent handles ClaimCreationPolicy events from the shared informer
func (e *policyEngine) handlePolicyEvent(obj interface{}) {
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		e.logger.Error(nil, "Received non-unstructured object in policy event handler")
		return
	}

	e.workqueue.Add(types.NamespacedName{
		Name:      unstructuredObj.GetName(),
		Namespace: unstructuredObj.GetNamespace(),
	})
}

// processWorkItems processes items from the workqueue
func (e *policyEngine) processWorkItems(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		default:
			item, shutdown := e.workqueue.Get()
			if shutdown {
				return
			}

			func() {
				defer e.workqueue.Done(item)

				if err := e.processPolicyEvent(ctx, item); err != nil {
					e.logger.Error(err, "Failed to process policy event", "namespace", item.Namespace, "name", item.Name)
					e.workqueue.AddRateLimited(item)
				} else {
					e.workqueue.Forget(item)
				}
			}()
		}
	}
}

// processPolicyEvent processes a single ClaimCreationPolicy event
func (e *policyEngine) processPolicyEvent(ctx context.Context, key types.NamespacedName) error {
	// Get the policy from the informer cache
	policy := e.getPolicyFromCache(key.Name, key.Namespace)
	if policy == nil {
		// Policy was deleted - remove from our cache
		e.removePolicy(key.Name)
		e.logger.V(1).Info("Policy deleted, removed from cache", "policy", key.Name)
		return nil
	}

	// Convert unstructured to ClaimCreationPolicy
	var claimPolicy quotav1alpha1.ClaimCreationPolicy
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(policy.Object, &claimPolicy); err != nil {
		return fmt.Errorf("failed to convert policy: %w", err)
	}

	// Update the policy in cache
	if err := e.updatePolicy(&claimPolicy); err != nil {
		return fmt.Errorf("failed to update policy: %w", err)
	}

	return nil
}

// getPolicyFromCache retrieves a ClaimCreationPolicy from the informer cache
func (e *policyEngine) getPolicyFromCache(name, namespace string) *unstructured.Unstructured {
	key := name // Cluster-scoped resource, no namespace in key
	if namespace != "" {
		key = fmt.Sprintf("%s/%s", namespace, name)
	}

	item, exists, err := e.informer.GetIndexer().GetByKey(key)
	if err != nil || !exists {
		return nil
	}

	unstructuredObj, ok := item.(*unstructured.Unstructured)
	if !ok {
		return nil
	}

	return unstructuredObj
}

// updatePolicy adds or updates a policy in the cache.
func (e *policyEngine) updatePolicy(policy *quotav1alpha1.ClaimCreationPolicy) error {
	if policy == nil {
		return fmt.Errorf("policy cannot be nil")
	}

	policyLoadTotal.Inc()

	// Only process policies with Ready=True status
	if !e.isPolicyReady(policy) {
		e.logger.V(1).Info("Policy not ready, skipping update", "policy", policy.Name)
		e.removePolicy(policy.Name)
		return nil
	}

	gvk := policy.Spec.Trigger.Resource.GetGVK()
	gvkKey := gvk.String()

	// Check if policy is disabled
	if policy.Spec.Disabled != nil && *policy.Spec.Disabled {
		// Remove disabled policy from cache
		e.removePolicy(policy.Name)
		e.logger.V(1).Info("Policy disabled, removed from cache", "policy", policy.Name, "gvk", gvk)
		return nil
	}

	// Check for conflicts - only one policy per GVK is allowed
	if existing, exists := e.gvkIndex.Load(gvkKey); exists {
		existingPolicy := existing.(*quotav1alpha1.ClaimCreationPolicy)
		if existingPolicy.Name != policy.Name {
			e.logger.Error(nil, "Multiple policies found for same GVK, replacing existing",
				"gvk", gvk,
				"existing", existingPolicy.Name,
				"new", policy.Name)
		}
	} else {
		// New policy being added
		policyActiveGauge.Inc()
	}

	// Store the policy in the cache
	e.gvkIndex.Store(gvkKey, policy.DeepCopy())

	e.logger.V(1).Info("Policy updated in cache",
		"policy", policy.Name,
		"gvk", gvk,
		"ready", true,
		"disabled", policy.Spec.Disabled != nil && *policy.Spec.Disabled)

	return nil
}

// removePolicy removes a policy from the cache by name.
func (e *policyEngine) removePolicy(policyName string) {
	// Since we need to find the policy by name but our index is by GVK,
	// we need to iterate through the cache to find the policy with the matching name
	var gvkKeyToRemove *string

	e.gvkIndex.Range(func(key, value interface{}) bool {
		gvkKey := key.(string)
		policy := value.(*quotav1alpha1.ClaimCreationPolicy)

		if policy.Name == policyName {
			gvkKeyToRemove = &gvkKey
			return false // Stop iteration
		}
		return true // Continue iteration
	})

	if gvkKeyToRemove != nil {
		e.gvkIndex.Delete(*gvkKeyToRemove)
		policyActiveGauge.Dec()
		e.logger.V(1).Info("Policy removed from cache", "policy", policyName, "gvkKey", *gvkKeyToRemove)
	}
}

// isPolicyReady checks if a ClaimCreationPolicy has Ready=True status condition
func (e *policyEngine) isPolicyReady(policy *quotav1alpha1.ClaimCreationPolicy) bool {
	return apimeta.IsStatusConditionTrue(policy.Status.Conditions, quotav1alpha1.ClaimCreationPolicyReady)
}

// Close stops the policy engine and cleans up resources
func (e *policyEngine) Close() {
	e.logger.V(1).Info("Closing policy engine")

	// Stop the shared informer
	close(e.stopCh)

	// Shutdown the workqueue
	e.workqueue.ShutDown()
}
