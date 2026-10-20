//go:build integration

package appsownership

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	projectstorage "go.miloapis.com/milo/internal/apiserver/storage/project"
	milorequest "go.miloapis.com/milo/pkg/request"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/server/options"
	serverstorage "k8s.io/apiserver/pkg/server/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	api "k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/pkg/kubeapiserver"
	namespacestorage "k8s.io/kubernetes/pkg/registry/core/namespace/storage"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

type namespaceStores struct {
	main     *namespacestorage.REST
	finalize *namespacestorage.FinalizeREST
}

// Two independent namespace REST instances share a real etcd process and use
// Milo's actual project-aware storage decorator. No HTTP server or controller is
// substituted for this storage test, and no production admission bypass is added.
func namespaceReplicas(t *testing.T) [2]namespaceStores {
	t.Helper()
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	require.NotEmpty(t, assets, "integration tests require KUBEBUILDER_ASSETS with an etcd binary")
	etcd := &envtest.Etcd{Path: filepath.Join(assets, "etcd"), DataDir: t.TempDir(), StartTimeout: 20 * time.Second, StopTimeout: 10 * time.Second}
	require.NoError(t, etcd.Start())
	t.Cleanup(func() { require.NoError(t, etcd.Stop()) })
	config := &storagebackend.Config{
		Type: "etcd3", Prefix: "/registry",
		Transport: storagebackend.TransportConfig{ServerList: []string{etcd.URL.String()}},
	}
	completed := kubeapiserver.NewStorageFactoryConfig().Complete(options.NewEtcdOptions(config))
	completed.APIResourceConfig = serverstorage.NewResourceConfig()
	factory, err := completed.New()
	require.NoError(t, err)
	var replicas [2]namespaceStores
	for i := range replicas {
		resourceConfig, err := factory.NewConfig(corev1.Resource("namespaces"), nil)
		require.NoError(t, err)
		opts := generic.RESTOptions{
			StorageConfig:           resourceConfig,
			Decorator:               projectstorage.ProjectAwareDecorator(corev1.Resource("namespaces"), generic.UndecoratedStorage, nil),
			DeleteCollectionWorkers: 1, ResourcePrefix: "namespaces",
		}
		main, _, finalize, err := namespacestorage.NewREST(opts)
		require.NoError(t, err)
		replicas[i] = namespaceStores{main: main, finalize: finalize}
		t.Cleanup(main.Destroy)
	}
	return replicas
}

func namespaceAttributes(name, subresource string, op admission.Operation) admission.Attributes {
	return admission.NewAttributesRecord(nil, nil, corev1.SchemeGroupVersion.WithKind("Namespace"), "", name,
		corev1.SchemeGroupVersion.WithResource("namespaces"), subresource, op, nil, false,
		&user.DefaultInfo{Name: "test-admin", Groups: []string{user.SystemPrivilegedGroup}})
}

func guardedNamespaceDelete(name string) rest.ValidateObjectFunc {
	return rest.AdmissionToValidateObjectDeleteFunc(NewPlugin(), namespaceAttributes(name, "", admission.Delete), nil)
}

func guardedNamespaceUpdate(name, subresource string) rest.ValidateObjectUpdateFunc {
	return rest.AdmissionToValidateObjectUpdateFunc(NewPlugin(), namespaceAttributes(name, subresource, admission.Update), nil)
}

func createNamespaceFixture(t *testing.T, ctx context.Context, s *namespacestorage.REST, name string, held bool) *api.Namespace {
	t.Helper()
	ns := &api.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if held {
		ns.Annotations = map[string]string{NamespaceReservationKey: "reservation-uid"}
	}
	// Storage fixture only: a reserved object cannot currently be created through
	// admission by any identity. This is not a proposed acquisition API.
	obj, err := s.Create(ctx, ns, rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.NoError(t, err)
	return obj.(*api.Namespace)
}

func TestNamespaceReservationStorage(t *testing.T) {
	replicas := namespaceReplicas(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	ctx = apirequest.WithNamespace(ctx, metav1.NamespaceNone)
	projectA := milorequest.WithProject(ctx, "project-a")
	projectB := milorequest.WithProject(ctx, "project-b")

	t.Run("reservation blocks delete and finalize on either replica", func(t *testing.T) {
		held := createNamespaceFixture(t, projectA, replicas[0].main, "held", true)
		for _, replica := range replicas {
			_, _, err := replica.main.Delete(projectA, held.Name, guardedNamespaceDelete(held.Name), &metav1.DeleteOptions{})
			require.True(t, apierrors.IsForbidden(err), "%v", err)
			removal := held.DeepCopy()
			removal.Annotations = nil
			removal.Spec.Finalizers = nil
			_, _, err = replica.finalize.Update(projectA, held.Name, rest.DefaultUpdatedObjectInfo(removal), rest.ValidateAllObjectFunc,
				guardedNamespaceUpdate(held.Name, "finalize"), false, &metav1.UpdateOptions{})
			require.True(t, apierrors.IsForbidden(err), "%v", err)
			obj, err := replica.main.Get(projectA, held.Name, &metav1.GetOptions{})
			require.NoError(t, err)
			require.True(t, obj.(*api.Namespace).DeletionTimestamp.IsZero())
		}
	})

	t.Run("same name in another project deletes and finalizes normally", func(t *testing.T) {
		ns := createNamespaceFixture(t, projectB, replicas[0].main, "held", false)
		obj, _, err := replicas[1].main.Delete(projectB, ns.Name, guardedNamespaceDelete(ns.Name), &metav1.DeleteOptions{})
		require.NoError(t, err)
		terminating := obj.(*api.Namespace)
		require.False(t, terminating.DeletionTimestamp.IsZero())
		terminating.Spec.Finalizers = nil
		_, _, err = replicas[1].finalize.Update(projectB, ns.Name, rest.DefaultUpdatedObjectInfo(terminating), rest.ValidateAllObjectFunc,
			guardedNamespaceUpdate(ns.Name, "finalize"), false, &metav1.UpdateOptions{})
		require.NoError(t, err)
		_, err = replicas[0].main.Get(projectB, ns.Name, &metav1.GetOptions{})
		require.True(t, apierrors.IsNotFound(err), "%v", err)
	})

	t.Run("terminating reserved namespace cannot be force finalized", func(t *testing.T) {
		ns := createNamespaceFixture(t, projectA, replicas[0].main, "terminating-held", false)
		obj, _, err := replicas[0].main.Delete(projectA, ns.Name, guardedNamespaceDelete(ns.Name), &metav1.DeleteOptions{})
		require.NoError(t, err)
		terminating := obj.(*api.Namespace)
		// Seed an already inconsistent/legacy state through the test store. The
		// bootstrap guard must preserve it for recovery instead of allowing loss
		// of the namespace. This is not a permitted production acquisition.
		terminating.Annotations = map[string]string{NamespaceReservationKey: "incomplete-release"}
		obj, _, err = replicas[1].main.Update(projectA, ns.Name, rest.DefaultUpdatedObjectInfo(terminating), rest.ValidateAllObjectFunc,
			rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
		require.NoError(t, err)
		removal := obj.(*api.Namespace)
		removal.Spec.Finalizers = nil
		removal.Annotations = nil
		_, _, err = replicas[0].finalize.Update(projectA, ns.Name, rest.DefaultUpdatedObjectInfo(removal), rest.ValidateAllObjectFunc,
			guardedNamespaceUpdate(ns.Name, "finalize"), false, &metav1.UpdateOptions{})
		require.True(t, apierrors.IsForbidden(err), "%v", err)
		obj, err = replicas[1].main.Get(projectA, ns.Name, &metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, "incomplete-release", obj.(*api.Namespace).Annotations[NamespaceReservationKey])
	})

	t.Run("concurrent reservation wins against previously validated delete", func(t *testing.T) {
		ns := createNamespaceFixture(t, projectA, replicas[0].main, "reserve-wins", false)
		validated := make(chan struct{})
		resume := make(chan struct{})
		var resumeOnce sync.Once
		release := func() { resumeOnce.Do(func() { close(resume) }) }
		t.Cleanup(release)
		var validations atomic.Int32
		validation := guardedNamespaceDelete(ns.Name)
		pausedValidation := func(ctx context.Context, obj runtime.Object) error {
			attempt := validations.Add(1)
			if err := validation(ctx, obj); err != nil {
				return err
			}
			if attempt == 1 {
				close(validated)
				select {
				case <-resume:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}
		deleted := make(chan error, 1)
		go func() {
			_, _, err := replicas[0].main.Delete(projectA, ns.Name, pausedValidation, &metav1.DeleteOptions{})
			deleted <- err
		}()
		select {
		case <-validated:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		reservation := ns.DeepCopy()
		reservation.Annotations = map[string]string{NamespaceReservationKey: "reservation-uid"}
		reservation.Finalizers = []string{"apps.miloapis.com/namespace-reservation"}
		// Simulate the future reservation transaction ONLY in test storage setup.
		// Admission acquisition remains denied; this CAS must win before DELETE.
		_, _, err := replicas[1].main.Update(projectA, ns.Name, rest.DefaultUpdatedObjectInfo(reservation), rest.ValidateAllObjectFunc,
			rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
		require.NoError(t, err)
		release()
		select {
		case err := <-deleted:
			require.True(t, apierrors.IsForbidden(err), "%v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		require.GreaterOrEqual(t, validations.Load(), int32(2), "DELETE must revalidate after its etcd compare fails")
		obj, err := replicas[1].main.Get(projectA, ns.Name, &metav1.GetOptions{})
		require.NoError(t, err)
		require.True(t, obj.(*api.Namespace).DeletionTimestamp.IsZero())
		require.Equal(t, "reservation-uid", obj.(*api.Namespace).Annotations[NamespaceReservationKey])
		// An update using the unreserved old resourceVersion cannot erase the hold.
		_, _, err = replicas[0].main.Update(projectA, ns.Name, rest.DefaultUpdatedObjectInfo(ns), rest.ValidateAllObjectFunc,
			guardedNamespaceUpdate(ns.Name, ""), false, &metav1.UpdateOptions{})
		require.True(t, apierrors.IsConflict(err), "%v", err)
	})

	t.Run("delete wins before reservation CAS", func(t *testing.T) {
		ns := createNamespaceFixture(t, projectA, replicas[0].main, "delete-wins", false)
		obj, _, err := replicas[0].main.Delete(projectA, ns.Name, guardedNamespaceDelete(ns.Name), &metav1.DeleteOptions{})
		require.NoError(t, err)
		ns.Annotations = map[string]string{NamespaceReservationKey: "reservation-uid"}
		ns.Finalizers = []string{"apps.miloapis.com/namespace-reservation"}
		_, _, err = replicas[1].main.Update(projectA, ns.Name, rest.DefaultUpdatedObjectInfo(ns), rest.ValidateAllObjectFunc,
			rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
		require.True(t, apierrors.IsConflict(err), "%v", err)
		// Even a refreshed fixture cannot add the recovery finalizer after deletion.
		terminating := obj.(*api.Namespace)
		terminating.Annotations = ns.Annotations
		terminating.Finalizers = ns.Finalizers
		_, _, err = replicas[1].main.Update(projectA, ns.Name, rest.DefaultUpdatedObjectInfo(terminating), rest.ValidateAllObjectFunc,
			rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
		require.True(t, apierrors.IsInvalid(err), "%v", err)
	})
}
