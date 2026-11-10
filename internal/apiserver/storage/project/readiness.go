package projectstorage

import (
	"fmt"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
)

// ProjectNames is the view of Projects a readiness check walks.
type ProjectNames interface {
	HasSynced() bool
	Names() []string
}

// ReadinessRegistrar takes a storage's readiness check, as the server's
// storage readiness hook does (k8s.io/apiserver server/storage_readiness_hook.go).
type ReadinessRegistrar interface {
	RegisterStorage(gvr metav1.GroupVersionResource, storage rest.StorageWithReadiness)
}

// muxes hands a readiness check the mux the decorator built for its resource.
// apiextensions builds a definition's storage on the first request for it,
// through a decorator this package never calls itself.
var muxes = struct {
	sync.Mutex
	byResource map[schema.GroupResource]*projectMux
}{byResource: map[schema.GroupResource]*projectMux{}}

func registerMux(gr schema.GroupResource, m *projectMux) {
	muxes.Lock()
	defer muxes.Unlock()
	muxes.byResource[gr] = m
}

func forgetMux(gr schema.GroupResource, m *projectMux) {
	muxes.Lock()
	defer muxes.Unlock()
	if muxes.byResource[gr] == m {
		delete(muxes.byResource, gr)
	}
}

func registeredMux(gr schema.GroupResource) *projectMux {
	muxes.Lock()
	defer muxes.Unlock()
	return muxes.byResource[gr]
}

// ChildrenReadiness keeps a starting API server out of traffic until the
// child storage of one resource serves in every Project of the view. The
// server's storage readiness hook calls ReadinessCheck every 100 ms until it
// passes or the server's storage initialization timeout ends.
type ChildrenReadiness struct {
	gr     schema.GroupResource
	view   ProjectNames
	served func() error  // one list of the resource on the root: NotFound until it is served
	batch  int           // children built per call, at most
	every  time.Duration // at most one served() per this
	now    func() time.Time

	mu     sync.Mutex
	asked  time.Time
	reason string
}

// NewChildrenReadiness returns the check for gr's children over view. served
// lists gr on the root; batch bounds the children built per call.
func NewChildrenReadiness(gr schema.GroupResource, view ProjectNames, served func() error, batch int) *ChildrenReadiness {
	return &ChildrenReadiness{gr: gr, view: view, served: served, batch: batch, every: time.Second, now: time.Now}
}

// New and Destroy make the check a rest.Storage for the hook's registration;
// the hook keeps only ReadinessCheck.
func (r *ChildrenReadiness) New() runtime.Object { return &metav1.Status{} }

// Destroy holds nothing to release.
func (r *ChildrenReadiness) Destroy() {}

// ReadinessCheck returns nil once every child of every Project in the view
// serves, and the reason it does not otherwise.
func (r *ChildrenReadiness) ReadinessCheck() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.check()
}

func (r *ChildrenReadiness) check() error {
	if !r.view.HasSynced() {
		return r.record("view", fmt.Errorf("the project view has not synced"))
	}
	m := registeredMux(r.gr)
	if m == nil {
		return r.record("resource", r.askServed())
	}
	names := r.view.Names()
	m.buildChildren(names, r.batch)
	cold := m.unready(names)
	readinessChildren.WithLabelValues(r.gr.Group, r.gr.Resource, "ready").Set(float64(len(names) - len(cold)))
	readinessChildren.WithLabelValues(r.gr.Group, r.gr.Resource, "cold").Set(float64(len(cold)))
	if len(cold) > 0 {
		return r.record("children", fmt.Errorf("%d of %d %s storages do not serve yet, the first in project %s",
			len(cold), len(names), r.gr, cold[0]))
	}
	return r.record("ready", nil)
}

// askServed lists the resource on the root, at most once every r.every: the
// request makes apiextensions build its storage, and so the mux. Until the
// definition is served the list answers 404 for the resource.
func (r *ChildrenReadiness) askServed() error {
	if r.now().Sub(r.asked) < r.every {
		return fmt.Errorf("%s is not served yet", r.gr)
	}
	r.asked = r.now()
	switch err := r.served(); {
	case apierrors.IsNotFound(err):
		return fmt.Errorf("%s is not served yet", r.gr)
	case err != nil:
		return fmt.Errorf("listing %s on the root: %w", r.gr, err)
	}
	return fmt.Errorf("%s is served; its storage is being built", r.gr)
}

// record logs a change of reason at Info, so a pod that goes ready at the
// hook's deadline with cold storages says why, and returns err.
func (r *ChildrenReadiness) record(reason string, err error) error {
	if reason != r.reason {
		r.reason = reason
		klog.InfoS("Project storage readiness", "resource", r.gr.String(), "reason", reason, "detail", err)
	}
	return err
}

// RegisterChildrenReadiness registers r with the hook under gvr, unless the
// operator turned the check off.
func RegisterChildrenReadiness(reg ReadinessRegistrar, enabled bool, gvr metav1.GroupVersionResource, r *ChildrenReadiness) {
	if enabled {
		reg.RegisterStorage(gvr, r)
	}
}
