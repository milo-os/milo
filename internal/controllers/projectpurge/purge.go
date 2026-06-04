package projectpurge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Options struct {
	LabelSelector string
	FieldSelector string
	Timeout       time.Duration
	Parallel      int
}

type Purger struct{}

func New() *Purger { return &Purger{} }

type res struct {
	gvr        schema.GroupVersionResource
	namespaced bool
}

func ignorable(err error) bool {
	return err == nil ||
		apierrors.IsNotFound(err) ||
		apierrors.IsMethodNotSupported(err) ||
		meta.IsNoMatchError(err)
}

var protected = map[string]struct{}{
	"default":         {},
	"kube-system":     {},
	"kube-public":     {},
	"kube-node-lease": {},
	// add "milo-system" if you make it per-project and protect it
}

// StartPurge runs Phases A through D (discovery, DeleteCollection on namespaced
// resources, delete namespaces, force-finalize namespaces). These are fast
// fire-and-forget operations that issue delete commands without waiting for
// completion. All phases are idempotent and safe to re-run.
func (p *Purger) StartPurge(ctx context.Context, cfg *rest.Config, project string, o Options) error {
	if o.Timeout == 0 {
		o.Timeout = 2 * time.Minute
	}
	if o.Parallel <= 0 {
		o.Parallel = 8
	}

	disco, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return fmt.Errorf("discovery: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("dynamic: %w", err)
	}
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("typed core: %w", err)
	}

	// Discover resources
	lists, err := disco.ServerPreferredResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return fmt.Errorf("discover: %w", err)
	}
	var all []res
	for _, l := range lists {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil {
			continue
		}
		for _, ar := range l.APIResources {
			verbs := sets.NewString(ar.Verbs...)
			if !verbs.HasAll("list", "deletecollection") {
				continue
			}
			if containsSlash(ar.Name) {
				continue // skip subresources
			}
			all = append(all, res{
				gvr: schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: ar.Name,
				},
				namespaced: ar.Namespaced,
			})
		}
	}

	// Partition & exclude namespaces & CRDs for explicit phases.
	// Cluster-scoped resource deletion is intentionally omitted —
	// only namespaced resources and namespaces themselves are purged.
	var namespaced []res
	for _, r := range all {
		if r.gvr.Group == "" && r.gvr.Resource == "namespaces" {
			continue
		}
		if r.gvr.Group == "apiextensions.k8s.io" && r.gvr.Resource == "customresourcedefinitions" {
			continue
		}
		if r.namespaced {
			namespaced = append(namespaced, r)
		}
	}

	nsList, err := core.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}
	var namespaces []string
	for i := range nsList.Items {
		namespaces = append(namespaces, nsList.Items[i].Name)
	}

	bg := metav1.DeletePropagationBackground
	delOpts := metav1.DeleteOptions{PropagationPolicy: &bg}
	listOpts := metav1.ListOptions{LabelSelector: o.LabelSelector, FieldSelector: o.FieldSelector}

	deadline, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

	// Phase A: namespaced kinds per namespace
	if err := runParallel(deadline, o.Parallel, namespaced, func(ctx context.Context, r res) error {
		ri := dyn.Resource(r.gvr)
		for _, ns := range namespaces {
			if err := ri.Namespace(ns).DeleteCollection(ctx, delOpts, listOpts); !ignorable(err) {
				if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
					return fmt.Errorf("rbac forbids DeleteCollection for %s in ns=%s: %w", r.gvr, ns, err)
				}
				return fmt.Errorf("DeleteCollection %s ns=%s: %w", r.gvr, ns, err)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Phase B: delete namespaces themselves (sets DeletionTimestamp)
	if err := runParallel(deadline, o.Parallel, namespaces, func(ctx context.Context, ns string) error {
		if _, ok := protected[ns]; ok {
			return nil
		}
		if err := core.CoreV1().Namespaces().Delete(ctx, ns, delOpts); !ignorable(err) {
			if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
				return fmt.Errorf("rbac forbids deleting namespace %q: %w", ns, err)
			}
			return fmt.Errorf("delete namespace %q: %w", ns, err)
		}
		return nil
	}); err != nil {
		return err
	}

	// Phase C: force-finalize namespaces so we don’t rely on a namespace controller
	if err := runParallel(deadline, o.Parallel, namespaces, func(ctx context.Context, ns string) error {
		nso, err := core.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
		if ignorable(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get namespace %q: %w", ns, err)
		}

		if nso.DeletionTimestamp.IsZero() {
			_ = core.CoreV1().Namespaces().Delete(ctx, ns, delOpts)
		}

		nso.Spec.Finalizers = nil
		if _, err := core.CoreV1().Namespaces().Finalize(ctx, nso, metav1.UpdateOptions{}); !ignorable(err) {
			if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
				return fmt.Errorf("rbac forbids namespaces/finalize on %q: %w", ns, err)
			}
			return fmt.Errorf("finalize namespace %q: %w", ns, err)
		}
		return nil
	}); err != nil {
		return err
	}

	return nil
}

// IsPurgeComplete performs a single namespace list and returns true when only
// the "default" namespace (or no namespaces) remain. Only errors that
// definitively indicate the per-project API server is gone (e.g. connection
// refused) are treated as complete. All other errors (timeouts, 500s, 429s,
// RBAC issues, context cancellation) are returned so the controller can retry.
func (p *Purger) IsPurgeComplete(ctx context.Context, cfg *rest.Config, project string) (bool, error) {
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return false, fmt.Errorf("building client for project %s: %w", project, err)
	}

	nsList, err := core.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		if isServerGone(err) {
			return true, nil
		}
		return false, fmt.Errorf("listing namespaces for project %s: %w", project, err)
	}

	switch len(nsList.Items) {
	case 0:
		return true, nil
	case 1:
		return nsList.Items[0].Name == "default", nil
	default:
		return false, nil
	}
}

// isServerGone returns true when the error indicates the remote API server is
// permanently unreachable — connection refused, or the API endpoint itself no
// longer exists. Transient failures (timeouts, 500s, throttling, RBAC) return
// false so the caller retries.
func isServerGone(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return true
		}
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	if apierrors.IsNotFound(err) {
		return true
	}
	return false
}

// helper (generic, named)
func runParallel[N any](ctx context.Context, parallel int, slice []N, fn func(context.Context, N) error) error {
	sem := make(chan struct{}, parallel)
	eg, c := errgroup.WithContext(ctx)
	for _, v := range slice {
		v := v
		sem <- struct{}{}
		eg.Go(func() error {
			defer func() { <-sem }()
			return fn(c, v)
		})
	}
	return eg.Wait()
}

func containsSlash(s string) bool {
	return strings.ContainsRune(s, '/')
}
