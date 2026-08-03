package resourcemanager

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

func newOrganizationTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(resourcemanagerv1alpha.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	return scheme
}

func TestOrganizationController_Reconcile_addsFinalizerAndOwnerReference(t *testing.T) {
	t.Parallel()

	scheme := newOrganizationTestScheme(t)
	org := &resourcemanagerv1alpha.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: "org-abc123",
			UID:  "org-uid-abc123",
		},
	}
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "organization-org-abc123"},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(org, namespace).Build()
	controller := &OrganizationController{Client: fakeClient}

	if _, err := controller.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: org.Name}}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var updatedNamespace corev1.Namespace
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: namespace.Name}, &updatedNamespace); err != nil {
		t.Fatalf("getting namespace: %v", err)
	}
	assertOwnedByOrganization(t, updatedNamespace.OwnerReferences, org)

	var updatedOrg resourcemanagerv1alpha.Organization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: org.Name}, &updatedOrg); err != nil {
		t.Fatalf("getting organization: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&updatedOrg, OrganizationNamespaceFinalizer) {
		t.Fatalf("finalizers = %#v, want %q", updatedOrg.Finalizers, OrganizationNamespaceFinalizer)
	}
}

func TestOrganizationController_Reconcile_namespaceNotYetCreated(t *testing.T) {
	t.Parallel()

	scheme := newOrganizationTestScheme(t)
	org := &resourcemanagerv1alpha.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: "org-abc123",
			UID:  "org-uid-abc123",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(org).Build()
	controller := &OrganizationController{Client: fakeClient}

	// The namespace doesn't exist yet (e.g. the webhook's Create hasn't landed
	// in this reconcile's view). Reconcile must not error, and should still
	// add the finalizer so a subsequent reconcile can pick up the owner
	// reference once the namespace shows up.
	if _, err := controller.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: org.Name}}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var updatedOrg resourcemanagerv1alpha.Organization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: org.Name}, &updatedOrg); err != nil {
		t.Fatalf("getting organization: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&updatedOrg, OrganizationNamespaceFinalizer) {
		t.Fatalf("finalizers = %#v, want %q", updatedOrg.Finalizers, OrganizationNamespaceFinalizer)
	}
}

func TestOrganizationController_Reconcile_deletion(t *testing.T) {
	t.Parallel()

	t.Run("sets the owner reference before releasing the finalizer", func(t *testing.T) {
		scheme := newOrganizationTestScheme(t)
		now := metav1.Now()
		org := &resourcemanagerv1alpha.Organization{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "org-abc123",
				UID:               "org-uid-abc123",
				Finalizers:        []string{OrganizationNamespaceFinalizer},
				DeletionTimestamp: &now,
			},
		}
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-org-abc123"},
		}

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(org, namespace).WithStatusSubresource(org).Build()
		controller := &OrganizationController{Client: fakeClient}

		if _, err := controller.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: org.Name}}); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}

		var updatedNamespace corev1.Namespace
		if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: namespace.Name}, &updatedNamespace); err != nil {
			t.Fatalf("getting namespace: %v", err)
		}
		assertOwnedByOrganization(t, updatedNamespace.OwnerReferences, org)

		// Removing the last finalizer on an object already marked for
		// deletion lets the store actually remove it -- same as real
		// Kubernetes. Its absence here is proof the finalizer was released.
		var updatedOrg resourcemanagerv1alpha.Organization
		if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: org.Name}, &updatedOrg); !apierrors.IsNotFound(err) {
			t.Fatalf("getting organization: got err = %v, want NotFound now that its last finalizer is released", err)
		}
	})

	t.Run("releases the finalizer when there is no namespace to protect", func(t *testing.T) {
		scheme := newOrganizationTestScheme(t)
		now := metav1.Now()
		org := &resourcemanagerv1alpha.Organization{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "org-abc123",
				UID:               "org-uid-abc123",
				Finalizers:        []string{OrganizationNamespaceFinalizer},
				DeletionTimestamp: &now,
			},
		}

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(org).Build()
		controller := &OrganizationController{Client: fakeClient}

		if _, err := controller.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: org.Name}}); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}

		var updatedOrg resourcemanagerv1alpha.Organization
		if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: org.Name}, &updatedOrg); !apierrors.IsNotFound(err) {
			t.Fatalf("getting organization: got err = %v, want NotFound now that its last finalizer is released", err)
		}
	})

	t.Run("is a no-op for a pre-existing organization with no finalizer", func(t *testing.T) {
		scheme := newOrganizationTestScheme(t)
		now := metav1.Now()
		org := &resourcemanagerv1alpha.Organization{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "org-abc123",
				UID:               "org-uid-abc123",
				DeletionTimestamp: &now,
				Finalizers:        []string{"some-other-finalizer"},
			},
		}
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-org-abc123"},
		}

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(org, namespace).Build()
		controller := &OrganizationController{Client: fakeClient}

		if _, err := controller.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: org.Name}}); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}

		var updatedNamespace corev1.Namespace
		if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: namespace.Name}, &updatedNamespace); err != nil {
			t.Fatalf("getting namespace: %v", err)
		}
		if len(updatedNamespace.OwnerReferences) != 0 {
			t.Fatalf("owner references = %#v, want untouched", updatedNamespace.OwnerReferences)
		}
	})
}
