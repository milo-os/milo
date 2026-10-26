package projectpurge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"go.miloapis.com/milo/internal/projectscope"
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
	gvr schema.GroupVersionResource
}

func ignorable(err error) bool {
	return err == nil ||
		apierrors.IsNotFound(err) ||
		apierrors.IsMethodNotSupported(err) ||
		meta.IsNoMatchError(err)
}

// protected namespaces are never deleted. The API server rejects deletion of
// "default", so it is drained in place and anything left in it is reported as
// a blocker instead.
var protected = map[string]struct{}{
	"default":         {},
	"kube-system":     {},
	"kube-public":     {},
	"kube-node-lease": {},
}

// maxReportedBlockers bounds how much detail is gathered and carried on the
// project's status condition, which has a size limit.
const maxReportedBlockers = 10

// Blocker names something that still exists in a project's control plane, and
// the finalizers holding it, so the component responsible is identifiable.
type Blocker struct {
	// Namespace is the namespace an object lives in, or the namespace itself
	// when Resource is empty. Empty for a cluster-scoped object.
	Namespace string
	// Resource is the plural resource name of a remaining object. Empty when
	// the namespace itself has not finished deleting.
	Resource string
	// Name of the remaining object.
	Name string
	// Finalizers holding the object.
	Finalizers []string
	// Detail is the namespace controller's own account of what is left, when
	// it has one.
	Detail string
	// Marked reports, for a namespace (Resource empty), whether it carries a
	// deletion mark. An unmarked namespace appeared after the purge's sweep.
	Marked bool
}

func (b Blocker) String() string {
	if b.Resource == "" {
		state := "is still terminating"
		if !b.Marked {
			state = "is not yet deleted"
		}
		if b.Detail != "" {
			return fmt.Sprintf("namespace %q %s (%s)", b.Namespace, state, b.Detail)
		}
		return fmt.Sprintf("namespace %q %s", b.Namespace, state)
	}
	name := b.Name
	if b.Namespace != "" {
		name = b.Namespace + "/" + b.Name
	}
	if len(b.Finalizers) > 0 {
		return fmt.Sprintf("%s %q (finalizers: %s)", b.Resource, name, strings.Join(b.Finalizers, ", "))
	}
	return fmt.Sprintf("%s %q", b.Resource, name)
}

// Status reports whether a project's resources have drained, and what is
// holding them when they have not.
type Status struct {
	Complete bool
	Blockers []Blocker
}

// Unmarked names the namespaces that are present without a deletion mark.
func (s Status) Unmarked() []string {
	var names []string
	for _, b := range s.Blockers {
		if b.Resource == "" && !b.Marked {
			names = append(names, b.Namespace)
		}
	}
	return names
}

// NoteDeletes records the outcome of deleting the named namespaces on their
// blockers. A delete that went through set the namespace's deletion mark; a
// refused delete keeps its namespace unmarked and names why.
func (s *Status) NoteDeletes(deleted []string, refused map[string]error) {
	asked := sets.NewString(deleted...)
	for i := range s.Blockers {
		b := &s.Blockers[i]
		if b.Resource != "" || !asked.Has(b.Namespace) {
			continue
		}
		if err := refused[b.Namespace]; err != nil {
			b.Detail = "delete refused: " + err.Error()
			continue
		}
		b.Marked = true
	}
}

// Message renders the blockers for a status condition.
func (s Status) Message() string {
	if len(s.Blockers) == 0 {
		return "Waiting for project resources to be removed"
	}
	parts := make([]string, 0, maxReportedBlockers+1)
	for i, b := range s.Blockers {
		if i == maxReportedBlockers {
			parts = append(parts, fmt.Sprintf("and %d more", len(s.Blockers)-maxReportedBlockers))
			break
		}
		parts = append(parts, b.String())
	}
	return "Waiting for project resources to be removed: " + strings.Join(parts, "; ")
}

// StartPurge runs Phases A, B and C (discovery, DeleteCollection on namespaced
// resources, delete cluster-scoped objects, delete namespaces). These are fast fire-and-forget operations that issue delete
// commands without waiting for completion. Every phase is idempotent and safe
// to re-run.
//
// Namespaces are deleted, never force-finalized. Kubernetes guarantees that a
// namespace outlives its contents and consumers resolve their own state through
// it, so a namespace that will not drain is reported as a blocker rather than
// removed out from under whatever still lives in it. Each project control plane
// has a namespace controller that drains namespaces correctly.
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

	namespaced, err := namespacedResources(disco.ServerPreferredResources)
	if err != nil {
		return err
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

	// Phase B: cluster-scoped objects. Status waits for the ones that carry
	// finalizers, so their owners finish against a control plane that still
	// answers. A failed read or a refused delete here must not keep
	// namespaces from being deleted, so its error is returned after Phase C.
	local, clusterErr := servedLocally(deadline, dyn)
	var cluster []res
	if clusterErr == nil {
		cluster, clusterErr = clusterScopedResources(disco.ServerPreferredResources, local)
	}
	if clusterErr == nil {
		clusterErr = deleteClusterObjects(deadline, dyn, cluster, o.Parallel, listOpts)
	}

	// Phase C: delete namespaces themselves (sets DeletionTimestamp)
	return errors.Join(clusterErr, firstRefusal(deleteNamespaces(deadline, core, namespaces, o.Parallel)))
}

// DeleteNamespaces deletes the named namespaces of a project's control plane,
// never a protected one, and returns each refused delete by namespace.
func (p *Purger) DeleteNamespaces(ctx context.Context, cfg *rest.Config, names []string) (map[string]error, error) {
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("typed core: %w", err)
	}
	return deleteNamespaces(ctx, core, names, 8), nil
}

// deleteNamespaces deletes each named namespace that is not protected. A
// namespace already gone, or already marked, is no error.
func deleteNamespaces(ctx context.Context, core kubernetes.Interface, names []string, parallel int) map[string]error {
	bg := metav1.DeletePropagationBackground
	var mu sync.Mutex
	refused := map[string]error{}
	_ = runParallel(ctx, parallel, names, func(ctx context.Context, ns string) error {
		if _, ok := protected[ns]; ok {
			return nil
		}
		err := core.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{PropagationPolicy: &bg})
		if ignorable(err) {
			return nil
		}
		if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
			err = fmt.Errorf("rbac forbids deleting namespace %q: %w", ns, err)
		} else {
			err = fmt.Errorf("delete namespace %q: %w", ns, err)
		}
		mu.Lock()
		refused[ns] = err
		mu.Unlock()
		return nil
	})
	return refused
}

// firstRefusal returns the refusal of the first namespace by name, or nil.
func firstRefusal(refused map[string]error) error {
	names := make([]string, 0, len(refused))
	for ns := range refused {
		names = append(names, ns)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil
	}
	return refused[names[0]]
}

// Status reports whether every resource in a project's control plane is gone.
// A project has drained once its namespaces have been removed, the
// namespaces that cannot be deleted hold nothing, and no cluster-scoped object
// carries a finalizer. Only errors that definitively
// indicate the per-project API server is gone (e.g. connection refused) are
// treated as complete. All other errors (timeouts, 500s, 429s, RBAC issues,
// context cancellation) are returned so the controller can retry.
func (p *Purger) Status(ctx context.Context, cfg *rest.Config, project string) (Status, error) {
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return Status{}, fmt.Errorf("building client for project %s: %w", project, err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return Status{}, fmt.Errorf("building dynamic client for project %s: %w", project, err)
	}
	disco, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return Status{}, fmt.Errorf("building discovery client for project %s: %w", project, err)
	}

	status, err := purgeStatus(ctx, core, dyn, disco.ServerPreferredResources)
	if err != nil {
		return Status{}, fmt.Errorf("checking cleanup for project %s: %w", project, err)
	}
	return status, nil
}

func purgeStatus(
	ctx context.Context,
	core kubernetes.Interface,
	dyn dynamic.Interface,
	discoverResources func() ([]*metav1.APIResourceList, error),
) (Status, error) {
	nsList, err := core.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		if isServerGone(err) {
			return Status{Complete: true}, nil
		}
		return Status{}, fmt.Errorf("listing namespaces: %w", err)
	}

	var blockers []Blocker
	var drainedInPlace []string
	for i := range nsList.Items {
		ns := &nsList.Items[i]
		if _, ok := protected[ns.Name]; ok {
			drainedInPlace = append(drainedInPlace, ns.Name)
			continue
		}
		blockers = append(blockers, Blocker{Namespace: ns.Name, Detail: namespaceDetail(ns), Marked: ns.DeletionTimestamp != nil})
	}

	// Namespaces that cannot be deleted still have to be empty, otherwise the
	// project would be removed while objects — and the consumer finalizers on
	// them — are still live.
	if len(drainedInPlace) > 0 {
		remaining, err := remainingContent(ctx, dyn, discoverResources, drainedInPlace)
		if err != nil {
			return Status{}, err
		}
		blockers = append(blockers, remaining...)
	}

	// Cluster-scoped objects that carry finalizers hold the project too, so
	// their owners can finish against a control plane that still answers.
	local, err := servedLocally(ctx, dyn)
	if err != nil {
		return Status{}, err
	}
	cluster, err := clusterScopedResources(discoverResources, local)
	if err != nil {
		return Status{}, err
	}
	held, err := heldClusterObjects(ctx, dyn, cluster)
	if err != nil {
		return Status{}, err
	}
	blockers = append(blockers, held...)

	sort.Slice(blockers, func(i, j int) bool {
		a, b := blockers[i], blockers[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Resource != b.Resource {
			return a.Resource < b.Resource
		}
		return a.Name < b.Name
	})

	return Status{Complete: len(blockers) == 0, Blockers: blockers}, nil
}

// namespaceDetail surfaces the namespace controller's own account of what is
// left in a terminating namespace, which names the finalizers holding it.
func namespaceDetail(ns *corev1.Namespace) string {
	var details []string
	for _, c := range ns.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case corev1.NamespaceContentRemaining,
			corev1.NamespaceFinalizersRemaining,
			corev1.NamespaceDeletionContentFailure:
			details = append(details, c.Message)
		}
	}
	return strings.Join(details, "; ")
}

// remainingContent lists what is left in namespaces that are drained in place.
func remainingContent(
	ctx context.Context,
	dyn dynamic.Interface,
	discoverResources func() ([]*metav1.APIResourceList, error),
	namespaces []string,
) ([]Blocker, error) {
	resources, err := namespacedResources(discoverResources)
	if err != nil {
		return nil, err
	}

	var mu sync.Mutex
	var blockers []Blocker

	err = runParallel(ctx, 8, resources, func(ctx context.Context, r res) error {
		for _, ns := range namespaces {
			list, err := dyn.Resource(r.gvr).Namespace(ns).List(ctx, metav1.ListOptions{Limit: maxReportedBlockers})
			if err != nil {
				if ignorable(err) {
					continue
				}
				if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
					return fmt.Errorf("rbac forbids listing %s in ns=%s: %w", r.gvr, ns, err)
				}
				return fmt.Errorf("list %s ns=%s: %w", r.gvr, ns, err)
			}
			for i := range list.Items {
				item := &list.Items[i]
				mu.Lock()
				blockers = append(blockers, Blocker{
					Namespace:  ns,
					Resource:   r.gvr.Resource,
					Name:       item.GetName(),
					Finalizers: item.GetFinalizers(),
				})
				mu.Unlock()
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return blockers, nil
}

// namespacedResources returns the namespaced resources the purge deletes.
// Namespaces are deleted in Phase C, the kinds every project shares are never
// deleted, and cluster-scoped resources are covered by clusterScopedResources.
func namespacedResources(discoverResources func() ([]*metav1.APIResourceList, error)) ([]res, error) {
	lists, err := discoverResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return nil, fmt.Errorf("discover: %w", err)
	}

	var namespaced []res
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
			if !ar.Namespaced {
				continue
			}
			gvr := schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: ar.Name}
			if gvr.Group == "" && gvr.Resource == "namespaces" {
				continue
			}
			if projectscope.StaysGlobal(gvr.GroupResource()) {
				continue
			}
			namespaced = append(namespaced, res{gvr: gvr})
		}
	}
	return namespaced, nil
}

// clusterScopedResources returns the cluster-scoped resources the purge
// deletes. Namespaces are deleted in Phase C, and the kinds every project
// shares are never deleted.
//
// Only group versions in local, the ones Milo stores itself, are returned, so a
// kind served from elsewhere is never deleted with a project. Like
// namespacedResources, only resources that support deletecollection are
// returned.
func clusterScopedResources(discoverResources func() ([]*metav1.APIResourceList, error), local sets.Set[schema.GroupVersion]) ([]res, error) {
	lists, err := discoverResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return nil, fmt.Errorf("discover: %w", err)
	}

	var cluster []res
	for _, l := range lists {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil {
			continue
		}
		if !local.Has(gv) {
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
			if ar.Namespaced {
				continue
			}
			gvr := schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: ar.Name}
			if gvr.Group == "" && gvr.Resource == "namespaces" {
				continue
			}
			if projectscope.StaysGlobal(gvr.GroupResource()) {
				continue
			}
			cluster = append(cluster, res{gvr: gvr})
		}
	}
	return cluster, nil
}

var apiServicesGVR = schema.GroupVersionResource{Group: "apiregistration.k8s.io", Version: "v1", Resource: "apiservices"}

// servedLocally returns the group versions Milo stores itself: those an
// APIService without spec.service names. A group version no APIService names
// cannot be told apart, so it is left out.
func servedLocally(ctx context.Context, dyn dynamic.Interface) (sets.Set[schema.GroupVersion], error) {
	list, err := dyn.Resource(apiServicesGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list apiservices: %w", err)
	}
	local := sets.New[schema.GroupVersion]()
	for i := range list.Items {
		obj := list.Items[i].Object
		if _, found, _ := unstructured.NestedMap(obj, "spec", "service"); found {
			continue
		}
		group, _, _ := unstructured.NestedString(obj, "spec", "group")
		version, _, _ := unstructured.NestedString(obj, "spec", "version")
		local.Insert(schema.GroupVersion{Group: group, Version: version})
	}
	return local, nil
}

// heldClusterObjects lists the objects of the given cluster-scoped resources
// that carry finalizers. Marked reports whether each one already carries a
// deletion mark.
func heldClusterObjects(ctx context.Context, dyn dynamic.Interface, resources []res) ([]Blocker, error) {
	var mu sync.Mutex
	var blockers []Blocker

	err := runParallel(ctx, 8, resources, func(ctx context.Context, r res) error {
		list, err := dyn.Resource(r.gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			if ignorable(err) {
				return nil
			}
			if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
				return fmt.Errorf("rbac forbids listing %s: %w", r.gvr, err)
			}
			return fmt.Errorf("list %s: %w", r.gvr, err)
		}
		for i := range list.Items {
			item := &list.Items[i]
			if len(item.GetFinalizers()) == 0 {
				continue
			}
			mu.Lock()
			blockers = append(blockers, Blocker{
				Resource:   r.gvr.Resource,
				Name:       item.GetName(),
				Finalizers: item.GetFinalizers(),
				Marked:     item.GetDeletionTimestamp() != nil,
			})
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return blockers, nil
}

// deleteClusterObjects deletes every object of the given cluster-scoped
// resources. A refused delete does not stop the others; every refusal is
// returned.
func deleteClusterObjects(ctx context.Context, dyn dynamic.Interface, resources []res, parallel int, listOpts metav1.ListOptions) error {
	bg := metav1.DeletePropagationBackground
	delOpts := metav1.DeleteOptions{PropagationPolicy: &bg}

	var mu sync.Mutex
	var errs []error
	_ = runParallel(ctx, parallel, resources, func(ctx context.Context, r res) error {
		err := dyn.Resource(r.gvr).DeleteCollection(ctx, delOpts, listOpts)
		if ignorable(err) {
			return nil
		}
		if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
			err = fmt.Errorf("rbac forbids DeleteCollection for %s: %w", r.gvr, err)
		} else {
			err = fmt.Errorf("DeleteCollection %s: %w", r.gvr, err)
		}
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
		return nil
	})
	return errors.Join(errs...)
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
