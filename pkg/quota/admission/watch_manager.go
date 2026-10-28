package admission

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// maxHeldOutcomes bounds the outcomes an unbound waiter holds. A waiter is
// unbound only while its request reads, deletes and creates one claim, so it
// meets few claims of its name; the bound keeps a name that churns from
// growing a request's memory. The oldest goes first, because the request's own
// claim is the latest of its name. Were it dropped anyway, the request would
// time out, and a client retries a timeout.
const maxHeldOutcomes = 4

// claimWaiter represents a waiter for a specific ResourceClaim. It implements
// ClaimWaiter for the request that registered it.
type claimWaiter struct {
	manager    *watchManager
	claimName  string
	namespace  string
	resultChan chan ClaimResult
	timeout    time.Duration
	cancelFunc context.CancelFunc
	timer      *time.Timer
	startTime  time.Time

	// uid is the claim the waiter judges by, set by Bind once the request has
	// created it. held keeps, oldest first, the final outcomes seen while uid
	// is empty. The manager's waitersLock guards both.
	uid  types.UID
	held []heldOutcome
}

// heldOutcome is a claim's final outcome, kept until its waiter is bound.
type heldOutcome struct {
	uid     types.UID
	result  ClaimResult
	outcome string // the waiter duration's result label
}

// Result implements ClaimWaiter.
func (c *claimWaiter) Result() <-chan ClaimResult { return c.resultChan }

// Bind implements ClaimWaiter.
func (c *claimWaiter) Bind(uid types.UID) { c.manager.bind(c.key(), c, uid) }

// Cancel implements ClaimWaiter.
func (c *claimWaiter) Cancel() {
	c.cancelFunc()
	c.manager.waitersLock.Lock()
	defer c.manager.waitersLock.Unlock()
	c.manager.unregisterLocked(c.key(), c)
}

func (c *claimWaiter) key() types.NamespacedName {
	return types.NamespacedName{Namespace: c.namespace, Name: c.claimName}
}

// hold keeps uid's latest final outcome until Bind, dropping the oldest held
// outcome beyond maxHeldOutcomes. The caller holds waitersLock.
func (c *claimWaiter) hold(uid types.UID, result ClaimResult, outcome string) {
	c.held = slices.DeleteFunc(c.held, func(h heldOutcome) bool { return h.uid == uid })
	c.held = append(c.held, heldOutcome{uid: uid, result: result, outcome: outcome})
	if len(c.held) > maxHeldOutcomes {
		c.held = slices.Delete(c.held, 0, 1)
	}
}

// watchManager implements ClaimWatchManager using direct watch streams with efficient,
// stateless operation optimized for admission plugin requirements.
//
// Key characteristics:
// - Direct watch API: Connects to Kubernetes watch stream without LIST operation
// - Fast startup: Begins watching from "now" using empty resourceVersion
// - Minimal memory: No cache; tracks only active waiters
// - TTL-based lifecycle: Automatically stops when inactive
// - Bookmark resumption: Resumes from last bookmark to minimize missed events
// - Infinite retry: Exponential backoff with jitter for transient failures
// - 410 Gone handling: Restarts from current time when resourceVersion expires
type watchManager struct {
	dynamicClient dynamic.Interface
	logger        logr.Logger
	config        *WatchManagerConfig
	projectID     string // Empty string for root scope

	// Watch stream state
	watchLock      sync.RWMutex
	watchInterface watch.Interface
	watchCtx       context.Context
	watchCancel    context.CancelFunc

	// Resumption state for bookmark-based recovery
	lastBookmark          string
	lastResourceVersion   string
	lastBookmarkTimestamp time.Time
	bookmarkLock          sync.RWMutex

	// Stream metrics tracking
	streamStartTime time.Time
	streamLock      sync.RWMutex

	// Waiters management
	waitersLock sync.RWMutex
	waiters     map[types.NamespacedName]*claimWaiter

	// TTL management
	ttlMu         sync.Mutex
	ttlTimer      *time.Timer
	activeWaiters int32 // Atomic counter

	// Lifecycle
	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	started   atomic.Bool

	// Callback for TTL expiration (to remove from parent cache)
	onTTLExpired func()
}

// NewWatchManager creates a new watch manager with TTL-based lifecycle management
func NewWatchManager(dynamicClient dynamic.Interface, logger logr.Logger, projectID string) ClaimWatchManager {
	config := DefaultWatchManagerConfig()

	return &watchManager{
		dynamicClient: dynamicClient,
		logger:        logger,
		config:        config,
		projectID:     projectID,
		waiters:       make(map[types.NamespacedName]*claimWaiter),
		stopCh:        make(chan struct{}),
	}
}

// SetTTLExpiredCallback sets the callback function to be called when TTL expires
func (w *watchManager) SetTTLExpiredCallback(callback func()) {
	w.onTTLExpired = callback
}

// Start initializes the watch manager using Limit=0 to avoid expensive initial LIST.
func (w *watchManager) Start(ctx context.Context) error {
	var startErr error
	w.startOnce.Do(func() {
		w.logger.Info("Starting watch manager",
			"project", w.projectID,
			"ttl", w.config.TTL.DefaultTTL)

		watchManagersCreated.Inc()
		watchManagersActive.Inc()
		watchStreamsDesired.Inc()

		gvr := schema.GroupVersionResource{
			Group:    quotav1alpha1.GroupVersion.Group,
			Version:  quotav1alpha1.GroupVersion.Version,
			Resource: "resourceclaims",
		}

		// Start watching from current time (empty resourceVersion).
		// We catch all events for claims created after this point - no historical state needed.
		w.bookmarkLock.Lock()
		w.lastResourceVersion = ""
		w.bookmarkLock.Unlock()

		w.watchCtx, w.watchCancel = context.WithCancel(context.Background())

		// Use channel to wait for watch to actually start
		watchStarted := make(chan error, 1)
		go w.watchLoop(w.watchCtx, gvr, watchStarted)

		// Wait for watch to start or fail, respecting the caller's context timeout
		select {
		case err := <-watchStarted:
			if err != nil {
				startErr = fmt.Errorf("failed to start watch: %w", err)
				w.logger.Error(err, "Failed to start watch manager")
				watchStreamsDesired.Dec() // Failed to start, decrement desired
				return
			}
		case <-ctx.Done():
			startErr = fmt.Errorf("watch startup cancelled: %w", ctx.Err())
			w.logger.Error(startErr, "Watch manager startup cancelled")
			watchStreamsDesired.Dec() // Failed to start, decrement desired
			// Cancel the watch context to stop the watchLoop goroutine
			w.watchCancel()
			return
		}

		w.started.Store(true)
		w.logger.Info("Watch manager started",
			"project", w.projectID)
	})

	return startErr
}

func (w *watchManager) Stop() {
	w.stopOnce.Do(func() {
		w.logger.Info("Stopping watch manager", "project", w.projectID)

		w.ttlMu.Lock()
		if w.ttlTimer != nil {
			w.ttlTimer.Stop()
			w.ttlTimer = nil
		}
		w.ttlMu.Unlock()

		if w.watchCancel != nil {
			w.watchCancel()
		}

		close(w.stopCh)

		w.watchLock.Lock()
		if w.watchInterface != nil {
			w.watchInterface.Stop()
			w.watchInterface = nil
		}
		w.watchLock.Unlock()

		w.waitersLock.Lock()
		for key, waiter := range w.waiters {
			if waiter.timer != nil {
				waiter.timer.Stop()
			}
			waiter.cancelFunc()
			close(waiter.resultChan)
			waiter.held = nil
			delete(w.waiters, key)
		}
		w.waitersLock.Unlock()

		watchManagersStopped.Inc()
		watchManagersActive.Dec()
		watchStreamsDesired.Dec()

		w.started.Store(false)
		w.logger.Info("Watch manager stopped", "project", w.projectID)
	})
}

// RegisterClaimWaiter registers a waiter for a specific ResourceClaim.
// Can be called before the claim exists.
func (w *watchManager) RegisterClaimWaiter(ctx context.Context, claimName, namespace string, timeout time.Duration) (ClaimWaiter, error) {
	if !w.started.Load() {
		return nil, fmt.Errorf("watch manager not started")
	}

	key := types.NamespacedName{Namespace: namespace, Name: claimName}

	w.logger.V(4).Info("Registering claim waiter",
		"claimName", claimName,
		"namespace", namespace,
		"timeout", timeout,
		"project", w.projectID)

	// Check if we've reached the maximum number of waiters
	w.waitersLock.RLock()
	if w.config.MaxWaiters > 0 && len(w.waiters) >= w.config.MaxWaiters {
		w.waitersLock.RUnlock()
		return nil, fmt.Errorf("maximum number of waiters (%d) reached", w.config.MaxWaiters)
	}
	w.waitersLock.RUnlock()

	// Create waiter context for cancellation
	_, cancelFunc := context.WithCancel(ctx)

	resultChan := make(chan ClaimResult, 1)

	// Create the waiter struct (without timer initially)
	waiter := &claimWaiter{
		manager:    w,
		claimName:  claimName,
		namespace:  namespace,
		resultChan: resultChan,
		timeout:    timeout,
		cancelFunc: cancelFunc,
		timer:      nil, // Will be set later if needed
		startTime:  time.Now(),
	}

	// Register the waiter BEFORE checking for existing claims
	w.waitersLock.Lock()
	w.waiters[key] = waiter
	w.waitersLock.Unlock()

	// Increment active waiter count and update TTL
	activeCount := atomic.AddInt32(&w.activeWaiters, 1)
	w.updateTTL()

	// Metrics: track registrations and current waiter count
	waiterRegistrations.Inc()
	waitersCurrent.Set(float64(activeCount))

	// Start the timeout timer
	timer := time.AfterFunc(timeout, func() {
		w.logger.V(3).Info("Claim waiter timed out",
			"claimName", claimName,
			"namespace", namespace,
			"timeout", timeout,
			"project", w.projectID)

		// Metrics for timeout
		waiterDuration.WithLabelValues("timeout").Observe(time.Since(waiter.startTime).Seconds())

		w.deliver(key, waiter, ClaimResult{
			Granted: false,
			Reason:  "timeout",
			Error:   fmt.Errorf("timeout waiting for ResourceClaim %s/%s after %v", namespace, claimName, timeout),
		})
	})
	// The waiter is already registered, so the timer is set under the lock its readers hold.
	w.waitersLock.Lock()
	waiter.timer = timer
	w.waitersLock.Unlock()

	w.logger.V(4).Info("Claim waiter registered successfully",
		"claimName", claimName,
		"namespace", namespace,
		"project", w.projectID)

	return waiter, nil
}

// UnregisterClaimWaiter unregisters a waiter for a specific ResourceClaim
func (w *watchManager) UnregisterClaimWaiter(claimName, namespace string) {
	key := types.NamespacedName{Namespace: namespace, Name: claimName}

	w.waitersLock.Lock()
	defer w.waitersLock.Unlock()

	if waiter, exists := w.waiters[key]; exists {
		w.unregisterLocked(key, waiter)
	}
}

// deliver sends result to waiter and unregisters it, if waiter is still the
// one registered for key. It holds waitersLock from the check to the close, so
// a timeout, a claim event, a cancel and Stop cannot interleave: a timer's
// callback runs in its own goroutine and Timer.Stop does not wait for it.
// The channel holds one result and only deliver sends, so the send never
// blocks.
func (w *watchManager) deliver(key types.NamespacedName, waiter *claimWaiter, result ClaimResult) {
	w.waitersLock.Lock()
	defer w.waitersLock.Unlock()
	w.deliverLocked(key, waiter, result)
}

// deliverLocked is deliver for a caller that holds waitersLock.
func (w *watchManager) deliverLocked(key types.NamespacedName, waiter *claimWaiter, result ClaimResult) {
	if w.waiters[key] != waiter {
		return
	}
	waiter.resultChan <- result
	w.unregisterLocked(key, waiter)
}

// offer gives a claim's final outcome to waiter, if waiter is still the one
// registered for key. A bound waiter takes only its own claim's outcome; an
// unbound one holds the outcome for Bind.
func (w *watchManager) offer(key types.NamespacedName, waiter *claimWaiter, uid types.UID, result ClaimResult, outcome string) {
	w.waitersLock.Lock()
	defer w.waitersLock.Unlock()

	if w.waiters[key] != waiter {
		return
	}
	switch waiter.uid {
	case "":
		waiter.hold(uid, result, outcome)
	case uid:
		waiterDuration.WithLabelValues(outcome).Observe(time.Since(waiter.startTime).Seconds())
		w.deliverLocked(key, waiter, result)
	}
}

// bind binds waiter to the claim uid and delivers the outcome held for that
// claim, if any; the other held outcomes are dropped.
func (w *watchManager) bind(key types.NamespacedName, waiter *claimWaiter, uid types.UID) {
	w.waitersLock.Lock()
	defer w.waitersLock.Unlock()

	if w.waiters[key] != waiter {
		return
	}
	held := waiter.held
	waiter.uid, waiter.held = uid, nil
	for _, h := range held {
		if h.uid == uid {
			waiterDuration.WithLabelValues(h.outcome).Observe(time.Since(waiter.startTime).Seconds())
			w.deliverLocked(key, waiter, h.result)
			return
		}
	}
}

// unregisterLocked removes waiter, if it is still the one registered for key,
// and closes its channel. The caller holds waitersLock.
func (w *watchManager) unregisterLocked(key types.NamespacedName, waiter *claimWaiter) {
	if w.waiters[key] != waiter {
		return
	}
	claimName, namespace := key.Name, key.Namespace
	w.logger.V(4).Info("Unregistering claim waiter",
		"claimName", claimName,
		"namespace", namespace,
		"project", w.projectID)

	if waiter.timer != nil {
		waiter.timer.Stop()
	}
	waiter.cancelFunc()
	close(waiter.resultChan)
	waiter.held = nil
	delete(w.waiters, key)

	// Decrement active waiter count and update TTL
	activeCount := atomic.AddInt32(&w.activeWaiters, -1)
	w.updateTTL()

	// Metrics: unregister and current waiter count
	waiterCompletions.WithLabelValues("unregistered").Inc()
	waitersCurrent.Set(float64(activeCount))

	w.logger.V(4).Info("Claim waiter unregistered",
		"claimName", claimName,
		"namespace", namespace,
		"project", w.projectID)
}

// updateTTL manages the TTL timer based on active waiter count
func (w *watchManager) updateTTL() {
	w.ttlMu.Lock()
	defer w.ttlMu.Unlock()

	activeCount := atomic.LoadInt32(&w.activeWaiters)

	if activeCount > 0 {
		// Active waiters - cancel TTL
		if w.ttlTimer != nil {
			w.ttlTimer.Stop()
			w.ttlTimer = nil
		}

		w.logger.V(5).Info("TTL paused due to active waiters",
			"project", w.projectID)
	} else {
		// No active waiters - start/reset TTL
		if w.ttlTimer != nil {
			w.ttlTimer.Stop()
		}

		ttlDuration := w.config.TTL.DefaultTTL
		w.ttlTimer = time.AfterFunc(ttlDuration, func() {
			w.logger.Info("TTL expired, stopping watch manager",
				"ttl", ttlDuration,
				"project", w.projectID)

			ttlExpirations.Inc()

			// Stop the watch manager
			w.Stop()

			// Notify parent to remove from cache
			if w.onTTLExpired != nil {
				w.onTTLExpired()
			}
		})

		ttlResets.Inc()

		w.logger.V(5).Info("TTL started/reset",
			"project", w.projectID)
	}
}

// watchLoop implements infinite retry with exponential backoff
func (w *watchManager) watchLoop(ctx context.Context, gvr schema.GroupVersionResource, watchStarted chan<- error) {
	backoff := w.config.Retry.InitialDelay
	firstAttempt := true

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Watch loop context cancelled", "project", w.projectID)
			// Signal failure if we never started
			if firstAttempt {
				watchStarted <- fmt.Errorf("watch loop cancelled before starting")
			}
			return
		case <-w.stopCh:
			w.logger.Info("Watch loop stopped", "project", w.projectID)
			// Signal failure if we never started
			if firstAttempt {
				watchStarted <- fmt.Errorf("watch loop stopped before starting")
			}
			return
		default:
		}

		// Attempt to establish/maintain watch stream
		restartStart := time.Now()
		var startedChan chan<- error
		if firstAttempt {
			startedChan = watchStarted
		}
		err := w.processWatchStream(ctx, gvr, startedChan)
		restartDuration := time.Since(restartStart)

		// Mark first attempt as complete
		if firstAttempt {
			firstAttempt = false
		}

		if err == nil {
			// Clean shutdown or reconnection successful, reset backoff
			backoff = w.config.Retry.InitialDelay
			continue
		}

		// Error occurred - classify and retry

		// Check if context was cancelled (clean shutdown)
		if ctx.Err() != nil {
			w.logger.Info("Watch loop stopping due to context cancellation",
				"project", w.projectID,
				"error", err)
			return
		}

		// 410 Gone - resourceVersion too old, restart from current time
		if statusErr, ok := err.(*errors.StatusError); ok && statusErr.Status().Code == http.StatusGone {
			w.logger.Info("ResourceVersion too old (410 Gone), restarting from current time",
				"project", w.projectID)

			w.bookmarkLock.Lock()
			w.lastBookmark = ""
			w.lastResourceVersion = ""
			w.bookmarkLock.Unlock()

			watchRestarts.WithLabelValues("410").Inc()
			continue
		}

		jitteredBackoff := w.applyJitter(backoff)

		// Extract status code for metrics
		statusCode := "unknown"
		if statusErr, ok := err.(*errors.StatusError); ok {
			statusCode = fmt.Sprintf("%d", statusErr.Status().Code)
		}

		w.logger.Info("Watch stream failed, retrying",
			"error", err,
			"project", w.projectID)

		watchRestarts.WithLabelValues(statusCode).Inc()
		watchRestartDuration.Observe(restartDuration.Seconds())

		// Sleep with backoff
		select {
		case <-time.After(jitteredBackoff):
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		}

		// Increase backoff (capped at MaxDelay)
		backoff = time.Duration(float64(backoff) * w.config.Retry.Multiplier)
		if backoff > w.config.Retry.MaxDelay {
			backoff = w.config.Retry.MaxDelay
		}
	}
}

// processWatchStream establishes and processes a single watch stream.
// The optional watchStarted channel is used to signal successful watch establishment
// on the first attempt. It's signaled with nil on success or an error on failure.
func (w *watchManager) processWatchStream(ctx context.Context, gvr schema.GroupVersionResource, watchStarted chan<- error) error {
	// Determine resumption point
	w.bookmarkLock.RLock()
	lastBookmark := w.lastBookmark
	lastRV := w.lastResourceVersion
	w.bookmarkLock.RUnlock()

	var watchOpts metav1.ListOptions
	watchOpts.Watch = true
	watchOpts.AllowWatchBookmarks = true

	if lastBookmark != "" {
		// Preferred: resume from bookmark
		watchOpts.ResourceVersion = lastBookmark
		w.logger.V(4).Info("Resuming watch from bookmark",
			"resourceVersion", lastBookmark,
			"project", w.projectID)
	} else if lastRV != "" {
		// Fallback: resume from last event
		watchOpts.ResourceVersion = lastRV
		w.logger.V(4).Info("Resuming watch from last resourceVersion",
			"resourceVersion", lastRV,
			"project", w.projectID)
	} else {
		// Initial start or 410 Gone reset: start from current time.
		// Empty resourceVersion means watch starts from "now".
		watchOpts.ResourceVersion = ""
		w.logger.V(4).Info("Starting watch from current time",
			"project", w.projectID)
	}

	// Start watch using the long-lived context.
	// The startup timeout in Start() will catch slow establishments.
	watchInterface, err := w.dynamicClient.Resource(gvr).Watch(ctx, watchOpts)
	if err != nil {
		// Signal failure to establish watch (only on first attempt)
		if watchStarted != nil {
			watchStarted <- fmt.Errorf("failed to start watch: %w", err)
		}
		return fmt.Errorf("failed to start watch: %w", err)
	}

	// Store watch interface and record stream start time
	streamStartTime := time.Now()
	w.watchLock.Lock()
	w.watchInterface = watchInterface
	w.watchLock.Unlock()

	w.streamLock.Lock()
	w.streamStartTime = streamStartTime
	w.streamLock.Unlock()

	// Signal successful watch establishment (only on first attempt)
	if watchStarted != nil {
		watchStarted <- nil
	}

	// Ensure cleanup and metric update on disconnect
	defer func() {
		// Calculate stream lifetime
		lifetime := time.Since(streamStartTime)

		// Determine disconnect reason (will be set by caller context)
		reason := "error"
		if ctx.Err() != nil {
			reason = "shutdown"
		}

		watchStreamLifetimeSeconds.WithLabelValues(reason).Observe(lifetime.Seconds())
		watchStreamsConnected.Dec()

		w.watchLock.Lock()
		if w.watchInterface != nil {
			w.watchInterface.Stop()
			w.watchInterface = nil
		}
		w.watchLock.Unlock()
	}()

	// Mark stream as connected
	watchStreamsConnected.Inc()

	// Start metric updater goroutine (updates max uptime and max bookmark age)
	metricsCtx, metricsCancel := context.WithCancel(ctx)
	defer metricsCancel()

	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Update max uptime metric
				uptime := time.Since(streamStartTime)
				watchStreamMaxUptimeSeconds.Set(uptime.Seconds())

				// Update max bookmark age metric
				w.bookmarkLock.RLock()
				lastUpdate := w.lastBookmarkTimestamp
				w.bookmarkLock.RUnlock()

				if !lastUpdate.IsZero() {
					age := time.Since(lastUpdate)
					watchBookmarkMaxAgeSeconds.Set(age.Seconds())
				}
			case <-metricsCtx.Done():
				return
			}
		}
	}()

	w.logger.V(3).Info("Watch stream established",
		"resourceVersion", watchOpts.ResourceVersion,
		"project", w.projectID)

	// Process events
	for {
		select {
		case event, ok := <-watchInterface.ResultChan():
			if !ok {
				w.logger.V(3).Info("Watch channel closed", "project", w.projectID)
				return fmt.Errorf("watch channel closed")
			}

			if err := w.handleWatchEvent(event); err != nil {
				return err
			}

		case <-ctx.Done():
			w.logger.V(3).Info("Watch context cancelled", "project", w.projectID)
			return ctx.Err()

		case <-w.stopCh:
			w.logger.V(3).Info("Watch stopped via stop channel", "project", w.projectID)
			return nil
		}
	}
}

// handleWatchEvent processes a single watch event
func (w *watchManager) handleWatchEvent(event watch.Event) error {
	switch event.Type {
	case watch.Added, watch.Modified:
		w.handleClaimEvent(event.Object)
		if rv := w.extractResourceVersion(event.Object); rv != "" {
			w.bookmarkLock.Lock()
			w.lastResourceVersion = rv
			w.bookmarkLock.Unlock()
		}
		watchEventsReceived.WithLabelValues(string(event.Type)).Inc()

	case watch.Deleted:
		w.handleClaimDeletion(event.Object)
		if rv := w.extractResourceVersion(event.Object); rv != "" {
			w.bookmarkLock.Lock()
			w.lastResourceVersion = rv
			w.bookmarkLock.Unlock()
		}
		watchEventsReceived.WithLabelValues("delete").Inc()

	case watch.Bookmark:
		if rv := w.extractResourceVersion(event.Object); rv != "" {
			now := time.Now()
			w.bookmarkLock.Lock()
			w.lastBookmark = rv
			w.lastBookmarkTimestamp = now
			w.bookmarkLock.Unlock()

			w.logger.V(5).Info("Bookmark updated",
				"resourceVersion", rv,
				"project", w.projectID)
		}
		watchEventsReceived.WithLabelValues("bookmark").Inc()

	case watch.Error:
		return w.extractErrorFromEvent(event)
	}

	return nil
}

// handleClaimEvent processes a ResourceClaim event (Added or Modified)
func (w *watchManager) handleClaimEvent(obj interface{}) {
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		w.logger.Error(nil, "Received non-unstructured object in claim event handler")
		watchEventsProcessed.WithLabelValues("error").Inc()
		return
	}

	key := types.NamespacedName{
		Name:      unstructuredObj.GetName(),
		Namespace: unstructuredObj.GetNamespace(),
	}

	// Check if we have a waiter for this claim
	w.waitersLock.RLock()
	waiter, exists := w.waiters[key]
	w.waitersLock.RUnlock()

	if !exists {
		// No waiter for this claim, ignore
		watchEventsProcessed.WithLabelValues("ignored").Inc()
		return
	}

	// Evaluate the claim status
	if result := w.evaluateClaimStatus(unstructuredObj); result != nil {
		// Claim has reached a final state
		outcome := "granted"
		if !result.Granted {
			outcome = "denied"
		}

		watchEventsProcessed.WithLabelValues(outcome).Inc()

		w.offer(key, waiter, unstructuredObj.GetUID(), *result, outcome)

		w.logger.V(3).Info("Claim result offered to waiter",
			"claimName", key.Name,
			"namespace", key.Namespace,
			"uid", unstructuredObj.GetUID(),
			"granted", result.Granted,
			"reason", result.Reason,
			"project", w.projectID)
	} else {
		// Claim still pending
		watchEventsProcessed.WithLabelValues("pending").Inc()
	}
}

// handleClaimDeletion processes a ResourceClaim deletion event
func (w *watchManager) handleClaimDeletion(obj interface{}) {
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		w.logger.Error(nil, "Received non-unstructured object in deletion handler")
		return
	}

	key := types.NamespacedName{
		Name:      unstructuredObj.GetName(),
		Namespace: unstructuredObj.GetNamespace(),
	}

	// Check if we have a waiter for this claim
	w.waitersLock.RLock()
	waiter, exists := w.waiters[key]
	w.waitersLock.RUnlock()

	if !exists {
		return
	}

	// Claim was deleted - notify waiter, if it is the waiter's own claim
	w.offer(key, waiter, unstructuredObj.GetUID(), ClaimResult{
		Granted: false,
		Reason:  "deleted",
		Error:   fmt.Errorf("ResourceClaim %s/%s was deleted", key.Namespace, key.Name),
	}, "deleted")
}

// evaluateClaimStatus evaluates a ResourceClaim's status and returns a result if final
func (w *watchManager) evaluateClaimStatus(claim *unstructured.Unstructured) *ClaimResult {
	// Extract the status from the unstructured object
	status, found, err := unstructured.NestedMap(claim.Object, "status")
	if err != nil || !found {
		// No status yet, claim is still pending
		return nil
	}

	// Check for conditions
	conditions, found, err := unstructured.NestedSlice(status, "conditions")
	if err != nil || !found {
		// No conditions yet, claim is still pending
		return nil
	}

	// Look for final conditions
	for _, conditionInterface := range conditions {
		condition, ok := conditionInterface.(map[string]interface{})
		if !ok {
			continue
		}

		conditionType, found, err := unstructured.NestedString(condition, "type")
		if err != nil || !found {
			continue
		}

		conditionStatus, found, err := unstructured.NestedString(condition, "status")
		if err != nil || !found {
			continue
		}

		reason, _, _ := unstructured.NestedString(condition, "reason")
		message, _, _ := unstructured.NestedString(condition, "message")

		// Check the Granted condition
		if conditionType == string(quotav1alpha1.ResourceClaimGranted) {
			if conditionStatus == string(metav1.ConditionTrue) {
				return &ClaimResult{
					Granted: true,
					Reason:  reason,
				}
			} else if conditionStatus == string(metav1.ConditionFalse) && reason == quotav1alpha1.ResourceClaimDeniedReason {
				return &ClaimResult{
					Granted: false,
					Reason:  message,
				}
			}
			// Other false statuses (like PendingEvaluation) are not final
		}
	}

	// No final condition found, claim is still pending
	return nil
}

// applyJitter applies random jitter to backoff duration
func (w *watchManager) applyJitter(duration time.Duration) time.Duration {
	jitter := w.config.Retry.Jitter
	if jitter <= 0 {
		return duration
	}

	// Apply ±jitter% randomization
	jitterAmount := float64(duration) * jitter
	randomJitter := (rand.Float64()*2 - 1) * jitterAmount // -jitter to +jitter

	jittered := time.Duration(float64(duration) + randomJitter)
	if jittered < 0 {
		jittered = duration
	}

	return jittered
}

// extractResourceVersion extracts resourceVersion from an object
func (w *watchManager) extractResourceVersion(obj interface{}) string {
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return ""
	}

	return unstructuredObj.GetResourceVersion()
}

// extractErrorFromEvent extracts error from a watch error event
func (w *watchManager) extractErrorFromEvent(event watch.Event) error {
	if event.Type != watch.Error {
		return nil
	}

	if statusErr, ok := event.Object.(*metav1.Status); ok {
		return &errors.StatusError{ErrStatus: *statusErr}
	}

	return fmt.Errorf("unknown error event: %v", event.Object)
}
