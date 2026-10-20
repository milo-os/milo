package iam

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const testContactNamespace = "milo-system"

func setupUserContactTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = iamv1alpha1.AddToScheme(scheme)
	_ = notificationv1alpha1.AddToScheme(scheme)
	return scheme
}

func TestUserContactController_CreateContactForNewUser(t *testing.T) {
	scheme := setupUserContactTestScheme()

	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user",
			UID:  "test-user-uid",
		},
		Spec: iamv1alpha1.UserSpec{
			Email:      "test@example.com",
			GivenName:  "Test",
			FamilyName: "User",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(user).
		WithStatusSubresource(&notificationv1alpha1.Contact{}).
		WithStatusSubresource(&iamv1alpha1.User{}).
		WithIndex(&notificationv1alpha1.Contact{}, contactEmailIndexKey, indexContactByEmail).
		WithIndex(&notificationv1alpha1.Contact{}, contactSubjectNameIndexKey, indexContactBySubjectName).
		Build()

	controller := &UserContactController{
		Client:           fakeClient,
		ContactNamespace: testContactNamespace,
	}

	ctx := context.Background()

	// Reconciliation should create the contact
	result, err := controller.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: user.Name},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)

	// Verify the contact was created
	contact := &notificationv1alpha1.Contact{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "user-test-user", Namespace: testContactNamespace}, contact)
	require.NoError(t, err)

	assert.Equal(t, user.Spec.Email, contact.Spec.Email)
	assert.Equal(t, user.Spec.GivenName, contact.Spec.GivenName)
	assert.Equal(t, user.Spec.FamilyName, contact.Spec.FamilyName)
	assert.NotNil(t, contact.Spec.SubjectRef)
	assert.Equal(t, "User", contact.Spec.SubjectRef.Kind)
	assert.Equal(t, user.Name, contact.Spec.SubjectRef.Name)
	assert.Equal(t, iamv1alpha1.SchemeGroupVersion.Group, contact.Spec.SubjectRef.APIGroup)
}

func TestUserContactController_UpdateExistingContactWithUserReference(t *testing.T) {
	scheme := setupUserContactTestScheme()

	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user",
			UID:  "test-user-uid",
		},
		Spec: iamv1alpha1.UserSpec{
			Email:      "test@example.com",
			GivenName:  "Test",
			FamilyName: "User",
		},
	}

	// Pre-existing contact without SubjectRef
	existingContact := &notificationv1alpha1.Contact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-contact",
			Namespace: testContactNamespace,
		},
		Spec: notificationv1alpha1.ContactSpec{
			Email:      "test@example.com",
			GivenName:  "Existing",
			FamilyName: "Contact",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(user, existingContact).
		WithStatusSubresource(&notificationv1alpha1.Contact{}).
		WithStatusSubresource(&iamv1alpha1.User{}).
		WithIndex(&notificationv1alpha1.Contact{}, contactEmailIndexKey, indexContactByEmail).
		WithIndex(&notificationv1alpha1.Contact{}, contactSubjectNameIndexKey, indexContactBySubjectName).
		Build()

	controller := &UserContactController{
		Client:           fakeClient,
		ContactNamespace: testContactNamespace,
	}

	ctx := context.Background()

	result, err := controller.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: user.Name},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)

	// Verify the existing contact was updated with SubjectRef
	updatedContact := &notificationv1alpha1.Contact{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "existing-contact", Namespace: testContactNamespace}, updatedContact)
	require.NoError(t, err)

	assert.NotNil(t, updatedContact.Spec.SubjectRef)
	assert.Equal(t, "User", updatedContact.Spec.SubjectRef.Kind)
	assert.Equal(t, user.Name, updatedContact.Spec.SubjectRef.Name)
	// Contact fields should be synced with user data
	assert.Equal(t, user.Spec.GivenName, updatedContact.Spec.GivenName)
	assert.Equal(t, user.Spec.FamilyName, updatedContact.Spec.FamilyName)
}

func TestUserContactController_ContactWithDifferentUserReferenceIsHeld(t *testing.T) {
	scheme := setupUserContactTestScheme()

	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user",
			UID:  "test-user-uid",
		},
		Spec: iamv1alpha1.UserSpec{
			Email:      "test@example.com",
			GivenName:  "Test",
			FamilyName: "User",
		},
	}

	// Pre-existing contact with SubjectRef pointing to another user
	existingContact := &notificationv1alpha1.Contact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-contact",
			Namespace: testContactNamespace,
		},
		Spec: notificationv1alpha1.ContactSpec{
			Email: "test@example.com",
			SubjectRef: &notificationv1alpha1.SubjectReference{
				APIGroup: iamv1alpha1.SchemeGroupVersion.Group,
				Kind:     "User",
				Name:     "another-user",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(user, existingContact).
		WithStatusSubresource(&notificationv1alpha1.Contact{}).
		WithStatusSubresource(&iamv1alpha1.User{}).
		WithIndex(&notificationv1alpha1.Contact{}, contactEmailIndexKey, indexContactByEmail).
		WithIndex(&notificationv1alpha1.Contact{}, contactSubjectNameIndexKey, indexContactBySubjectName).
		Build()

	controller := &UserContactController{
		Client:           fakeClient,
		ContactNamespace: testContactNamespace,
	}

	ctx := context.Background()

	result, err := controller.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: user.Name},
	})
	require.NoError(t, err, "a condition only another object can change is reported, not retried")
	assert.False(t, result.Requeue)

	// Verify the existing contact still points to the other user: the Contact webhook refuses a changed subject
	updatedContact := &notificationv1alpha1.Contact{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "existing-contact", Namespace: testContactNamespace}, updatedContact)
	require.NoError(t, err)

	assert.NotNil(t, updatedContact.Spec.SubjectRef)
	assert.Equal(t, "another-user", updatedContact.Spec.SubjectRef.Name, "SubjectRef should stay with the other user")

	// Verify the user reports the held email
	updatedUser := &iamv1alpha1.User{}
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: user.Name}, updatedUser))
	condition := meta.FindStatusCondition(updatedUser.Status.Conditions, UserContactSyncedCondition)
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Equal(t, UserContactEmailHeldReason, condition.Reason)
}

func TestUserContactController_CleanupDanglingReferenceOnContactReconcile(t *testing.T) {
	// This test verifies that when a Contact references a User that no longer exists,
	// the SubjectRef is removed during Contact reconciliation.
	scheme := setupUserContactTestScheme()

	// Contact with SubjectRef pointing to a NON-EXISTENT user
	contact := &notificationv1alpha1.Contact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orphaned-contact",
			Namespace: testContactNamespace,
		},
		Spec: notificationv1alpha1.ContactSpec{
			Email:      "test@example.com",
			GivenName:  "Test",
			FamilyName: "User",
			SubjectRef: &notificationv1alpha1.SubjectReference{
				APIGroup: iamv1alpha1.SchemeGroupVersion.Group,
				Kind:     "User",
				Name:     "deleted-user", // This user doesn't exist
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(contact). // No user!
		WithStatusSubresource(&notificationv1alpha1.Contact{}).
		WithIndex(&notificationv1alpha1.Contact{}, contactEmailIndexKey, indexContactByEmail).
		WithIndex(&notificationv1alpha1.Contact{}, contactSubjectNameIndexKey, indexContactBySubjectName).
		Build()

	controller := &UserContactController{
		Client:           fakeClient,
		ContactNamespace: testContactNamespace,
	}

	ctx := context.Background()

	// Call ReconcileContact directly
	result, err := controller.ReconcileContact(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: contact.Name, Namespace: contact.Namespace},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)

	// Verify the SubjectRef was removed
	updatedContact := &notificationv1alpha1.Contact{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: contact.Name, Namespace: testContactNamespace}, updatedContact)
	require.NoError(t, err)

	assert.Nil(t, updatedContact.Spec.SubjectRef, "SubjectRef should be removed when user doesn't exist")
}

func TestUserContactController_SyncEmailOnUserEmailChange(t *testing.T) {
	// This test verifies that when a user's email changes, the contact email is updated
	scheme := setupUserContactTestScheme()

	// User with a NEW email address
	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user",
			UID:  "test-user-uid",
		},
		Spec: iamv1alpha1.UserSpec{
			Email:      "new-email@example.com", // New email
			GivenName:  "Updated",
			FamilyName: "Name",
		},
	}

	// Existing contact with OLD email but SubjectRef pointing to this user
	existingContact := &notificationv1alpha1.Contact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-test-user",
			Namespace: testContactNamespace,
		},
		Spec: notificationv1alpha1.ContactSpec{
			Email: "old-email@example.com", // Old email
			SubjectRef: &notificationv1alpha1.SubjectReference{
				APIGroup: iamv1alpha1.SchemeGroupVersion.Group,
				Kind:     "User",
				Name:     user.Name,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(user, existingContact).
		WithStatusSubresource(&notificationv1alpha1.Contact{}).
		WithStatusSubresource(&iamv1alpha1.User{}).
		WithIndex(&notificationv1alpha1.Contact{}, contactEmailIndexKey, indexContactByEmail).
		WithIndex(&notificationv1alpha1.Contact{}, contactSubjectNameIndexKey, indexContactBySubjectName).
		Build()

	controller := &UserContactController{
		Client:           fakeClient,
		ContactNamespace: testContactNamespace,
	}

	ctx := context.Background()

	result, err := controller.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: user.Name},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)

	// Verify the contact was updated with the new email
	updatedContact := &notificationv1alpha1.Contact{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "user-test-user", Namespace: testContactNamespace}, updatedContact)
	require.NoError(t, err)

	assert.Equal(t, "new-email@example.com", updatedContact.Spec.Email, "Contact email should be updated")
	assert.NotNil(t, updatedContact.Spec.SubjectRef)
	assert.Equal(t, user.Name, updatedContact.Spec.SubjectRef.Name)
}

func TestUserContactController_DeleteUnlinkedContactOnEmailChange(t *testing.T) {
	// This test verifies that when a user changes their email to match an existing
	// unlinked newsletter contact, the unlinked contact is deleted to prevent duplicates.
	scheme := setupUserContactTestScheme()

	// User with a NEW email address that matches an unlinked contact
	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user",
			UID:  "test-user-uid",
		},
		Spec: iamv1alpha1.UserSpec{
			Email:      "newsletter@example.com", // New email that matches unlinked contact
			GivenName:  "Test",
			FamilyName: "User",
		},
	}

	// Existing contact linked to the user with OLD email
	userContact := &notificationv1alpha1.Contact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-test-user",
			Namespace: testContactNamespace,
		},
		Spec: notificationv1alpha1.ContactSpec{
			Email: "old-email@example.com", // Old email
			SubjectRef: &notificationv1alpha1.SubjectReference{
				APIGroup: iamv1alpha1.SchemeGroupVersion.Group,
				Kind:     "User",
				Name:     user.Name,
			},
		},
	}

	// Unlinked newsletter contact with the email the user is changing to
	newsletterContact := &notificationv1alpha1.Contact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "newsletter-signup",
			Namespace: testContactNamespace,
		},
		Spec: notificationv1alpha1.ContactSpec{
			Email:      "newsletter@example.com", // Same email as user's new email
			GivenName:  "Newsletter",
			FamilyName: "Subscriber",
			// No SubjectRef - this is an unlinked newsletter signup
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(user, userContact, newsletterContact).
		WithStatusSubresource(&notificationv1alpha1.Contact{}).
		WithStatusSubresource(&iamv1alpha1.User{}).
		WithIndex(&notificationv1alpha1.Contact{}, contactEmailIndexKey, indexContactByEmail).
		WithIndex(&notificationv1alpha1.Contact{}, contactSubjectNameIndexKey, indexContactBySubjectName).
		Build()

	controller := &UserContactController{
		Client:           fakeClient,
		ContactNamespace: testContactNamespace,
	}

	ctx := context.Background()

	result, err := controller.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: user.Name},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)

	// Verify the user's contact was updated with the new email
	updatedContact := &notificationv1alpha1.Contact{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "user-test-user", Namespace: testContactNamespace}, updatedContact)
	require.NoError(t, err)
	assert.Equal(t, "newsletter@example.com", updatedContact.Spec.Email)

	// Verify the unlinked newsletter contact was deleted
	deletedContact := &notificationv1alpha1.Contact{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "newsletter-signup", Namespace: testContactNamespace}, deletedContact)
	assert.True(t, apierrors.IsNotFound(err), "Unlinked newsletter contact should be deleted")
}

func TestUserContactController_UserNotFound(t *testing.T) {
	scheme := setupUserContactTestScheme()

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&notificationv1alpha1.Contact{}, contactEmailIndexKey, indexContactByEmail).
		WithIndex(&notificationv1alpha1.Contact{}, contactSubjectNameIndexKey, indexContactBySubjectName).
		Build()

	controller := &UserContactController{
		Client:           fakeClient,
		ContactNamespace: testContactNamespace,
	}

	ctx := context.Background()

	result, err := controller.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "non-existent-user"},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)
}

func TestUserContactController_UpdateContactInDifferentNamespace(t *testing.T) {
	// This test verifies that the controller finds and updates contacts in any namespace,
	// not just the ContactNamespace configured for creating new contacts.
	scheme := setupUserContactTestScheme()

	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user",
			UID:  "test-user-uid",
		},
		Spec: iamv1alpha1.UserSpec{
			Email:      "test@example.com",
			GivenName:  "Test",
			FamilyName: "User",
		},
	}

	// Pre-existing contact in a DIFFERENT namespace
	differentNamespace := "different-namespace"
	existingContact := &notificationv1alpha1.Contact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-contact",
			Namespace: differentNamespace,
		},
		Spec: notificationv1alpha1.ContactSpec{
			Email:      "test@example.com",
			GivenName:  "Existing",
			FamilyName: "Contact",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(user, existingContact).
		WithStatusSubresource(&notificationv1alpha1.Contact{}).
		WithStatusSubresource(&iamv1alpha1.User{}).
		WithIndex(&notificationv1alpha1.Contact{}, contactEmailIndexKey, indexContactByEmail).
		WithIndex(&notificationv1alpha1.Contact{}, contactSubjectNameIndexKey, indexContactBySubjectName).
		Build()

	controller := &UserContactController{
		Client:           fakeClient,
		ContactNamespace: testContactNamespace, // Different from where the contact exists
	}

	ctx := context.Background()

	result, err := controller.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: user.Name},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)

	// Verify the existing contact in the different namespace was updated with SubjectRef
	updatedContact := &notificationv1alpha1.Contact{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "existing-contact", Namespace: differentNamespace}, updatedContact)
	require.NoError(t, err)

	assert.NotNil(t, updatedContact.Spec.SubjectRef)
	assert.Equal(t, "User", updatedContact.Spec.SubjectRef.Kind)
	assert.Equal(t, user.Name, updatedContact.Spec.SubjectRef.Name)
}

func TestUserContactController_ContactReconcileWithValidUser(t *testing.T) {
	// This test verifies that ReconcileContact does nothing when the referenced User exists
	scheme := setupUserContactTestScheme()

	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user",
			UID:  "test-user-uid",
		},
		Spec: iamv1alpha1.UserSpec{
			Email: "test@example.com",
		},
	}

	contact := &notificationv1alpha1.Contact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-test-user",
			Namespace: testContactNamespace,
		},
		Spec: notificationv1alpha1.ContactSpec{
			Email: "test@example.com",
			SubjectRef: &notificationv1alpha1.SubjectReference{
				APIGroup: iamv1alpha1.SchemeGroupVersion.Group,
				Kind:     "User",
				Name:     user.Name,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(user, contact).
		WithIndex(&notificationv1alpha1.Contact{}, contactEmailIndexKey, indexContactByEmail).
		WithIndex(&notificationv1alpha1.Contact{}, contactSubjectNameIndexKey, indexContactBySubjectName).
		Build()

	controller := &UserContactController{
		Client:           fakeClient,
		ContactNamespace: testContactNamespace,
	}

	ctx := context.Background()

	// Call ReconcileContact
	result, err := controller.ReconcileContact(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: contact.Name, Namespace: contact.Namespace},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)

	// Verify the SubjectRef is still present
	updatedContact := &notificationv1alpha1.Contact{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: contact.Name, Namespace: testContactNamespace}, updatedContact)
	require.NoError(t, err)

	assert.NotNil(t, updatedContact.Spec.SubjectRef, "SubjectRef should remain when user exists")
	assert.Equal(t, user.Name, updatedContact.Spec.SubjectRef.Name)
}

func TestUserContactController_SkipUserBeingDeleted(t *testing.T) {
	// This test verifies that users with DeletionTimestamp are skipped
	scheme := setupUserContactTestScheme()

	now := metav1.Now()
	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-user",
			UID:               "test-user-uid",
			DeletionTimestamp: &now,
			Finalizers:        []string{"some-other-finalizer"}, // Needs a finalizer to not be garbage collected
		},
		Spec: iamv1alpha1.UserSpec{
			Email: "test@example.com",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(user).
		WithIndex(&notificationv1alpha1.Contact{}, contactEmailIndexKey, indexContactByEmail).
		WithIndex(&notificationv1alpha1.Contact{}, contactSubjectNameIndexKey, indexContactBySubjectName).
		Build()

	controller := &UserContactController{
		Client:           fakeClient,
		ContactNamespace: testContactNamespace,
	}

	ctx := context.Background()

	result, err := controller.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: user.Name},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)

	// Verify no contact was created
	contactList := &notificationv1alpha1.ContactList{}
	err = fakeClient.List(ctx, contactList)
	require.NoError(t, err)
	assert.Len(t, contactList.Items, 0, "No contact should be created for user being deleted")
}
