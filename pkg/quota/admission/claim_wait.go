package admission

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
)

// claimRef names the ResourceClaim an admission request created: the wait
// follows this claim, and starts from the resource version its create returned.
type claimRef struct {
	Name            string
	Namespace       string
	UID             types.UID
	ResourceVersion string
}

// maxGetsAfterGone bounds the GET-and-watch-again rounds that 410 Gone answers
// can cause in one wait, so a server that answers 410 to every watch costs a
// few GETs, not a loop until the timeout.
const maxGetsAfterGone = 3

// Wait outcomes that carry no grant or denial. awaitClaimResult maps
// waitTimedOut to a timeout failure and the others to an internal failure.
const (
	waitTimedOut   = "timeout"
	waitDeleted    = "deleted"
	waitWatchError = "watch_failed"
)

// claimWait is one admission request's wait for its own claim's outcome.
type claimWait struct {
	claims dynamic.ResourceInterface
	ref    claimRef
}

// waitForClaim returns the outcome of the claim ref names. It watches that one
// claim by name from the version its create returned, with client-go's
// RetryWatcher. The watch cache accepts a version it has not reached yet and
// sends only later events, so no outcome after the create is missed. A 410
// ends the watch, and one GET then decides. The wait ends at the claim's grant
// or denial, at its delete or replacement ("deleted"), or when ctx ends
// ("timeout").
func waitForClaim(ctx context.Context, claims dynamic.ResourceInterface, ref claimRef) ClaimResult {
	start := time.Now()
	waiterRegistrations.Inc()
	waitersCurrent.Inc()
	defer waitersCurrent.Dec()

	result := claimWait{claims: claims, ref: ref}.run(ctx)

	label := resultLabel(result)
	waiterCompletions.WithLabelValues(label).Inc()
	waiterDuration.WithLabelValues(label).Observe(time.Since(start).Seconds())
	return result
}

func (w claimWait) run(ctx context.Context) ClaimResult {
	out, gone, err := w.watchFrom(ctx, w.ref.ResourceVersion)
	for gets := 0; out == nil && gone && gets < maxGetsAfterGone && ctx.Err() == nil; gets++ {
		claimWaitGets.Inc()
		var rv string
		if out, rv = w.decideByGet(ctx); out == nil {
			out, gone, err = w.watchFrom(ctx, rv)
		}
	}
	if out != nil {
		return *out
	}
	return w.unfinished(ctx, err)
}

// watchFrom watches the claim from rv until its outcome, the end of ctx, or a
// 410, which it reports as gone. Any other end returns the error that ended it.
func (w claimWait) watchFrom(ctx context.Context, rv string) (*ClaimResult, bool, error) {
	var out *ClaimResult
	var gone bool
	_, err := watchtools.Until(ctx, rv, w.watcher(), func(ev watch.Event) (bool, error) {
		if ev.Type == watch.Error {
			err := apierrors.FromObject(ev.Object)
			gone = apierrors.IsGone(err) || apierrors.IsResourceExpired(err)
			return true, err
		}
		claim, ok := ev.Object.(*unstructured.Unstructured)
		if !ok || claim.GetUID() != w.ref.UID {
			// An earlier claim of the same name: its events may still arrive.
			return false, nil
		}
		out = w.outcome(ev.Type, claim)
		return out != nil, nil
	})
	return out, gone, err
}

// watcher watches the one claim the wait follows. The watch cache serves a
// watch narrowed to one name from that name's key alone.
func (w claimWait) watcher() cache.Watcher {
	selector := fields.OneTermEqualSelector("metadata.name", w.ref.Name).String()
	return &cache.ListWatch{
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = selector
			return w.claims.Watch(ctx, options)
		},
	}
}

// decideByGet reads the claim once after a 410: its outcome when it has one,
// otherwise the version to watch again from. A claim that is gone, or whose
// name now holds another claim, ends the wait as "deleted". Cleanup keeps a
// denied claim for quota.DefaultDeniedClaimRetention, so a denial is still
// found here unless a retry replaced the claim in the meantime.
func (w claimWait) decideByGet(ctx context.Context) (*ClaimResult, string) {
	claim, err := w.claims.Get(ctx, w.ref.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err) || (err == nil && claim.GetUID() != w.ref.UID):
		return w.outcome(watch.Deleted, nil), ""
	case err != nil:
		return &ClaimResult{Reason: waitWatchError, Error: fmt.Errorf("reading ResourceClaim %s/%s after 410 Gone: %w", w.ref.Namespace, w.ref.Name, err)}, ""
	}
	return w.outcome(watch.Modified, claim), claim.GetResourceVersion()
}

// outcome returns the claim's final outcome, or nil while it is pending.
func (w claimWait) outcome(event watch.EventType, claim *unstructured.Unstructured) *ClaimResult {
	if event == watch.Deleted {
		return &ClaimResult{Reason: waitDeleted, Error: fmt.Errorf("ResourceClaim %s/%s was deleted", w.ref.Namespace, w.ref.Name)}
	}
	switch cond := grantedCondition(claim); classify(cond) {
	case claimConditionGranted:
		return &ClaimResult{Granted: true, Reason: cond.reason}
	case claimConditionDenied:
		return &ClaimResult{Granted: false, Reason: cond.message}
	}
	return nil
}

// unfinished names a wait that ended without an outcome: a timeout when ctx
// ended, otherwise the watch's own error, such as a 403.
func (w claimWait) unfinished(ctx context.Context, err error) ClaimResult {
	if ctx.Err() != nil {
		return ClaimResult{Reason: waitTimedOut, Error: fmt.Errorf("timeout waiting for ResourceClaim %s/%s: %w", w.ref.Namespace, w.ref.Name, ctx.Err())}
	}
	return ClaimResult{Reason: waitWatchError, Error: fmt.Errorf("watching ResourceClaim %s/%s: %w", w.ref.Namespace, w.ref.Name, err)}
}

// resultLabel is the result label of the waiter metrics.
func resultLabel(r ClaimResult) string {
	switch {
	case r.Granted:
		return "granted"
	case r.Error == nil:
		return "denied"
	}
	return r.Reason
}
