package etcdshared

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
)

const projectReservationAnnotation = "apps.miloapis.com/project-reservation"

var ErrReservationUnavailable = errors.New("root Project reservation storage is unavailable")
var ErrReservationBinding = errors.New("root Project reservation binding is invalid or conflicts")

// ProjectReservations is a server-local capability, never an HTTP identity or
// permission. Only actual root storage registration supplies its backend.
// There is deliberately no release, generic update, or caller-supplied key.
type ProjectReservations struct{ manager *CommitFence }
type rootReservationStore struct {
	store storage.Interface
	key   func(runtime.Object) (string, error)
}
type ProjectReservation struct {
	Name            string
	ProjectUID      string
	ReservationUID  string
	ResourceVersion string
}

// ProjectReservations returns a handle that fails closed until root storage is
// registered. It has no production caller; constructing it does not enroll Apps.
func (m *CommitFence) ProjectReservations() *ProjectReservations {
	return &ProjectReservations{manager: m}
}

func (m *CommitFence) registerReservationStorage(c storagebackend.ConfigForResource, key func(runtime.Object) (string, error), store storage.Interface) (func(), error) {
	if m == nil || c.GroupResource != rootProjectResource || store == nil {
		return nil, ErrReservationUnavailable
	}
	probe, err := key(&unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "storage-fence-key-probe"}}})
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.invalid || m.root == nil || m.root.transport != transportKey(c.Transport) || m.root.prefix != c.Prefix || m.root.probe != probe {
		return nil, ErrReservationUnavailable
	}
	// Retirement disables only this optional capability. Equivalent replacement
	// CRD storage must still initialize for ordinary API requests.
	if m.reservationInvalid {
		return func() {}, nil
	}
	m.reservation = &rootReservationStore{store: store, key: key}
	// Any destruction of an attached root registration invalidates acquisition
	// until restart, even if another equivalent CRD store remains. Never silently
	// switch a capability to a potentially retired registration.
	return func() { m.mu.Lock(); defer m.mu.Unlock(); m.reservationInvalid = true; m.reservation = nil }, nil
}

func validReservationBinding(name, projectUID, reservationUID string) bool {
	parsed, err := uuid.Parse(reservationUID)
	return len(validation.IsDNS1123Subdomain(name)) == 0 && projectUID != "" && len(projectUID) <= 128 && len(validation.IsValidLabelValue(projectUID)) == 0 && err == nil && parsed != uuid.Nil && parsed.String() == reservationUID
}
func positiveReservationVersion(rv string) bool {
	n, err := strconv.ParseInt(rv, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == rv
}

// Cancellation/deadlines propagate, but request identity and private native
// commit proofs never flow into this distinct server-local storage operation.
type reservationContext struct{ context.Context }

func (reservationContext) Value(any) any { return nil }

func (c *ProjectReservations) withStore(ctx context.Context, fn func(context.Context, *rootReservationStore) (ProjectReservation, error)) (ProjectReservation, error) {
	if c == nil || c.manager == nil {
		return ProjectReservation{}, ErrReservationUnavailable
	}
	ctx, cancel := context.WithTimeout(reservationContext{ctx}, 2*time.Second)
	defer cancel()
	m := c.manager
	// Hold registration stable through the bounded operation. Destruction waits
	// for outstanding calls before closing their backing storage.
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.invalid || m.reservationInvalid || m.root == nil || m.reservation == nil {
		return ProjectReservation{}, ErrReservationUnavailable
	}
	return fn(ctx, m.reservation)
}

// Acquire adds exactly the canonical annotation to an existing, nonterminating
// Project under exact UID and resourceVersion preconditions. Already-held
// objects conflict, including the same reservation. Recover uncertain replies
// with ReadHeld and the original binding; never adopt an unrelated hold.
func (c *ProjectReservations) Acquire(ctx context.Context, name, projectUID, expectedRV, reservationUID string) (ProjectReservation, error) {
	if !validReservationBinding(name, projectUID, reservationUID) || !positiveReservationVersion(expectedRV) {
		return ProjectReservation{}, ErrReservationBinding
	}
	return c.withStore(ctx, func(ctx context.Context, s *rootReservationStore) (ProjectReservation, error) {
		key, err := s.key(&unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": name}}})
		if err != nil {
			return ProjectReservation{}, err
		}
		out := &unstructured.Unstructured{}
		uid := types.UID(projectUID)
		err = s.store.GuaranteedUpdate(ctx, key, out, false, &storage.Preconditions{UID: &uid, ResourceVersion: &expectedRV}, func(old runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
			obj, ok := old.(*unstructured.Unstructured)
			if !ok {
				return nil, nil, ErrReservationBinding
			}
			if _, err := inspectReservation(obj, name, projectUID, "", false); err != nil {
				return nil, nil, err
			}
			next := obj.DeepCopy()
			annotations := next.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[projectReservationAnnotation] = reservationUID
			next.SetAnnotations(annotations)
			return next, nil, nil
		}, nil)
		if err != nil {
			return ProjectReservation{}, err
		}
		return inspectReservation(out, name, projectUID, reservationUID, true)
	})
}

// ReadHeld is linearizable recovery of one exact binding. It never writes or
// accepts a replacement UID, unknown hold, terminating Project, or label alias.
func (c *ProjectReservations) ReadHeld(ctx context.Context, name, projectUID, reservationUID string) (ProjectReservation, error) {
	if !validReservationBinding(name, projectUID, reservationUID) {
		return ProjectReservation{}, ErrReservationBinding
	}
	return c.withStore(ctx, func(ctx context.Context, s *rootReservationStore) (ProjectReservation, error) {
		key, err := s.key(&unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": name}}})
		if err != nil {
			return ProjectReservation{}, err
		}
		out := &unstructured.Unstructured{}
		if err = s.store.Get(ctx, key, storage.GetOptions{}, out); err != nil {
			return ProjectReservation{}, err
		}
		return inspectReservation(out, name, projectUID, reservationUID, true)
	})
}

func inspectReservation(obj *unstructured.Unstructured, name, uid, reservation string, held bool) (ProjectReservation, error) {
	if obj.GetAPIVersion() != "resourcemanager.miloapis.com/v1alpha1" || obj.GetKind() != "Project" || obj.GetName() != name || string(obj.GetUID()) != uid || obj.GetNamespace() != "" || !positiveReservationVersion(obj.GetResourceVersion()) {
		return ProjectReservation{}, ErrReservationBinding
	}
	metadata, ok := obj.Object["metadata"].(map[string]any)
	if !ok {
		return ProjectReservation{}, ErrReservationBinding
	}
	for _, field := range []string{"deletionTimestamp", "deletionGracePeriodSeconds"} {
		if value, present := metadata[field]; present && value != nil {
			return ProjectReservation{}, ErrReservationBinding
		}
	}
	for _, field := range []string{"labels", "annotations"} {
		values, present, err := unstructured.NestedStringMap(obj.Object, "metadata", field)
		if err != nil {
			return ProjectReservation{}, ErrReservationBinding
		}
		if !present {
			values = nil
		}
		for _, key := range []string{"apps.miloapis.com/project-uid", "apps.miloapis.com/app-uid"} {
			if _, ok := values[key]; ok {
				return ProjectReservation{}, ErrReservationBinding
			}
		}
		value, exists := values[projectReservationAnnotation]
		if field == "annotations" && held {
			if !exists || value != reservation {
				return ProjectReservation{}, ErrReservationBinding
			}
		} else if exists {
			return ProjectReservation{}, ErrReservationBinding
		}
	}
	if held && reservation == "" {
		return ProjectReservation{}, fmt.Errorf("%w: missing reservation", ErrReservationBinding)
	}
	return ProjectReservation{Name: name, ProjectUID: uid, ReservationUID: reservation, ResourceVersion: obj.GetResourceVersion()}, nil
}
