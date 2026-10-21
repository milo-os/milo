package etcdshared

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
)

const reservationTestUID = "d8eecf6d-63d2-4a13-a2d0-2601cff90eca"

func reservationProject() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "resourcemanager.miloapis.com/v1alpha1", "kind": "Project", "metadata": map[string]any{"name": "probe", "uid": "project-uid", "resourceVersion": "9"}, "spec": map[string]any{"keep": "unchanged"}}}
}

type reservationStorageProbe struct {
	storage.Interface
	calls  int
	object *unstructured.Unstructured
}

func (p *reservationStorageProbe) Get(_ context.Context, _ string, _ storage.GetOptions, out runtime.Object) error {
	p.calls++
	out.(*unstructured.Unstructured).Object = p.object.DeepCopy().Object
	return nil
}
func (p *reservationStorageProbe) GuaranteedUpdate(ctx context.Context, key string, out runtime.Object, ignore bool, pre *storage.Preconditions, update storage.UpdateFunc, suggestion runtime.Object) error {
	p.calls++
	if err := pre.Check(key, p.object); err != nil {
		return err
	}
	next, _, err := update(p.object.DeepCopy(), storage.ResponseMeta{})
	if err != nil {
		return err
	}
	p.object = next.(*unstructured.Unstructured)
	p.object.SetResourceVersion("10")
	out.(*unstructured.Unstructured).Object = p.object.DeepCopy().Object
	return nil
}
func reservationUnitFixture(t *testing.T) (*CommitFence, *reservationStorageProbe, func()) {
	t.Helper()
	m := NewCommitFence()
	c := storagebackend.ConfigForResource{Config: storagebackend.Config{Prefix: "/registry"}, GroupResource: rootProjectResource}
	key := func(obj runtime.Object) (string, error) {
		return "/projects/" + obj.(*unstructured.Unstructured).GetName(), nil
	}
	m.root = &rootProjectStore{transport: transportKey(c.Transport), prefix: c.Prefix, probe: "/projects/storage-fence-key-probe", key: key}
	probe := &reservationStorageProbe{object: reservationProject()}
	destroy, err := m.registerReservationStorage(c, key, probe)
	require.NoError(t, err)
	return m, probe, destroy
}
func TestProjectReservationCapabilityBinding(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-uid", "wrong-rv", "wrong-name", "uppercase-reservation", "nil-uuid", "noncanonical-rv", "zero-rv"} {
		t.Run(mode, func(t *testing.T) {
			m, store, _ := reservationUnitFixture(t)
			name, uid, rv, res := "probe", "project-uid", "9", reservationTestUID
			switch mode {
			case "wrong-uid":
				uid = "other"
			case "wrong-rv":
				rv = "8"
			case "wrong-name":
				name = "replacement"
			case "uppercase-reservation":
				res = "D8EECF6D-63D2-4A13-A2D0-2601CFF90ECA"
			case "nil-uuid":
				res = "00000000-0000-0000-0000-000000000000"
			case "noncanonical-rv":
				rv = "09"
			case "zero-rv":
				rv = "0"
			}
			before := store.object.DeepCopy()
			got, err := m.ProjectReservations().Acquire(context.Background(), name, uid, rv, res)
			if mode != "valid" {
				require.Error(t, err)
				require.Equal(t, before, store.object)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "10", got.ResourceVersion)
			next := store.object.DeepCopy()
			next.SetAnnotations(nil)
			next.SetResourceVersion("9")
			require.Equal(t, before, next)
			recovered, err := m.ProjectReservations().ReadHeld(context.Background(), name, uid, res)
			require.NoError(t, err)
			require.Equal(t, got, recovered)
			_, err = m.ProjectReservations().Acquire(context.Background(), name, uid, "10", res)
			require.Error(t, err)
		})
	}
}
func TestProjectReservationRejectsMalformedAndTerminatingObjects(t *testing.T) {
	cases := map[string]func(*unstructured.Unstructured){
		"terminating": func(u *unstructured.Unstructured) {
			u.Object["metadata"].(map[string]any)["deletionTimestamp"] = "2026-01-01T00:00:00Z"
		},
		"grace": func(u *unstructured.Unstructured) {
			u.Object["metadata"].(map[string]any)["deletionGracePeriodSeconds"] = int64(0)
		},
		"wrong-kind":    func(u *unstructured.Unstructured) { u.SetKind("Namespace") },
		"wrong-version": func(u *unstructured.Unstructured) { u.SetAPIVersion("resourcemanager.miloapis.com/v1beta1") },
		"namespace":     func(u *unstructured.Unstructured) { u.SetNamespace("tenant") },
	}
	for _, field := range []string{"labels", "annotations"} {
		for _, key := range []string{projectReservationAnnotation, "apps.miloapis.com/project-uid", "apps.miloapis.com/app-uid"} {
			for _, value := range []any{"", nil, int64(1), reservationTestUID} {
				field, key, value := field, key, value
				cases[field+key+stringifyReservationValue(value)] = func(u *unstructured.Unstructured) {
					u.Object["metadata"].(map[string]any)[field] = map[string]any{key: value}
				}
			}
		}
		field := field
		cases[field+"-container"] = func(u *unstructured.Unstructured) { u.Object["metadata"].(map[string]any)[field] = "bad" }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m, store, _ := reservationUnitFixture(t)
			mutate(store.object)
			before := store.object.DeepCopy()
			_, err := m.ProjectReservations().Acquire(context.Background(), "probe", "project-uid", "9", reservationTestUID)
			require.Error(t, err)
			require.Equal(t, before, store.object)
		})
	}
}
func stringifyReservationValue(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case nil:
		return "null"
	default:
		return "number"
	}
}
func TestProjectReservationRegistryLifetime(t *testing.T) {
	_, err := NewCommitFence().ProjectReservations().Acquire(context.Background(), "probe", "project-uid", "9", reservationTestUID)
	require.ErrorIs(t, err, ErrReservationUnavailable)
	for _, mode := range []string{"destroyed", "conflicting", "equivalent-registration-destroyed"} {
		t.Run(mode, func(t *testing.T) {
			m, store, destroy := reservationUnitFixture(t)
			cap := m.ProjectReservations()
			if mode == "conflicting" {
				m.invalid = true
			} else if mode == "destroyed" {
				destroy()
			} else {
				c := storagebackend.ConfigForResource{Config: storagebackend.Config{Prefix: "/registry"}, GroupResource: rootProjectResource}
				_, err := m.registerReservationStorage(c, m.root.key, store)
				require.NoError(t, err)
				destroy()
			}
			_, err := cap.Acquire(context.Background(), "probe", "project-uid", "9", reservationTestUID)
			require.ErrorIs(t, err, ErrReservationUnavailable)
			require.Zero(t, store.calls)
			_, err = cap.ReadHeld(context.Background(), "probe", "project-uid", reservationTestUID)
			require.ErrorIs(t, err, ErrReservationUnavailable)
			if mode != "conflicting" {
				c := storagebackend.ConfigForResource{Config: storagebackend.Config{Prefix: "/registry"}, GroupResource: rootProjectResource}
				cleanup, err := m.registerReservationStorage(c, m.root.key, store)
				require.NoError(t, err, "ordinary replacement root store still initializes")
				cleanup()
				_, err = cap.ReadHeld(context.Background(), "probe", "project-uid", reservationTestUID)
				require.ErrorIs(t, err, ErrReservationUnavailable, "replacement cannot re-enable retired capability")
			}

		})
	}
	m, store, _ := reservationUnitFixture(t)
	c := storagebackend.ConfigForResource{Config: storagebackend.Config{Prefix: "/registry"}, GroupResource: rootProjectResource}
	c.GroupResource.Resource = "other"
	_, err = m.registerReservationStorage(c, m.root.key, store)
	require.ErrorIs(t, err, ErrReservationUnavailable)
}

func TestStrictReservationRecord(t *testing.T) {
	good := `{"apiVersion":"resourcemanager.miloapis.com/v1alpha1","kind":"Project","metadata":{"name":"probe","uid":"project-uid","annotations":{"keep":"value"}}}`
	require.NoError(t, strictReservationRecord([]byte(good)))
	cases := []string{
		strings.Replace(good, `"keep":"value"`, `"apps.miloapis.com/project-reservation":null`, 1),
		strings.Replace(good, `"keep":"value"`, `"apps.miloapis.com/project-reservation":1`, 1),
		strings.Replace(good, `"keep":"value"`, `"apps.miloapis.com/project-reservation":"hold","apps.miloapis.com/project-reservation":""`, 1),
		strings.Replace(good, `"annotations":`, `"Annotations":`, 1),
		strings.Replace(good, `"annotations":{"keep":"value"}`, `"annotations":{"apps.miloapis.com/project-reservation":"hold"},"annotations":{}`, 1),
		strings.Replace(good, `"metadata":`, `"metadata":{},"metadata":`, 1),
		good + `{}`,
		strings.Replace(good, `"metadata":`, `"Metadata":{},"metadata":`, 1),
		string([]byte{0xff}),
		strings.Repeat("[", reservationJSONDepth+1) + strings.Repeat("]", reservationJSONDepth+1),
		strings.Repeat(" ", reservationRecordLimit+1),
	}
	for _, raw := range cases {
		require.ErrorIs(t, strictReservationRecord([]byte(raw)), ErrReservationBinding)
	}
}
