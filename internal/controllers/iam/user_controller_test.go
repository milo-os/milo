package iam

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUserNameNeedsReview(t *testing.T) {
	tests := []struct {
		name       string
		givenName  string
		familyName string
		want       bool
	}{
		{"distinct real names", "Ada", "Lovelace", false},
		{"short names", "Bo", "Li", false},
		{"hyphen, apostrophes and periods", "Jean-Luc", "O’Neill Jr.", false},
		{"non-Latin script", "Алексей", "Иванов", false},
		{"surrounding whitespace", " Ada ", "Lovelace ", false},
		{"identical names", "octocat", "octocat", true},
		{"identical names ignoring case", "Mario", "mario", true},
		{"both empty", "", "", false},
		{"email as family name", "Mario", "mario@example.com", true},
		{"email as given name", "julio@example.com", "Pena", true},
		{"email in a multi-word name", "Julio", "Pena julio@example.com", true},
		{"digits", "Julio", "juliopenayoo30030pppp", true},
		{"underscore handle", "git_happens", "Doe", true},
		{"starts with punctuation", "-Jane", "Doe", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, userNameNeedsReview(tt.givenName, tt.familyName))
		})
	}
}

func setupUserTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = iamv1alpha1.AddToScheme(scheme)
	return scheme
}

func newNameReviewTestUser(givenName, familyName string, annotations map[string]string) *iamv1alpha1.User {
	return &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "test-user", Annotations: annotations},
		Spec: iamv1alpha1.UserSpec{
			Email:      "mario@example.com",
			GivenName:  givenName,
			FamilyName: familyName,
		},
	}
}

func reconcileNameReview(t *testing.T, user *iamv1alpha1.User) *iamv1alpha1.User {
	t.Helper()
	ctx := context.Background()
	fakeClient := fake.NewClientBuilder().WithScheme(setupUserTestScheme()).WithObjects(user).Build()
	r := &UserController{Client: fakeClient}

	current := &iamv1alpha1.User{}
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: user.Name}, current))
	require.NoError(t, r.reconcileNameReviewAnnotation(ctx, current))

	updated := &iamv1alpha1.User{}
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: user.Name}, updated))
	return updated
}

func TestUserController_FlagsEmailShapedName(t *testing.T) {
	updated := reconcileNameReview(t, newNameReviewTestUser("Mario", "mario@example.com", nil))

	assert.Equal(t, "true", updated.Annotations[iamv1alpha1.UserNameReviewRequiredAnnotation])
}

func TestUserController_ClearsReviewOnceNameIsFixed(t *testing.T) {
	flagged := map[string]string{iamv1alpha1.UserNameReviewRequiredAnnotation: "true"}
	updated := reconcileNameReview(t, newNameReviewTestUser("Mario", "Rossi", flagged))

	assert.NotContains(t, updated.Annotations, iamv1alpha1.UserNameReviewRequiredAnnotation)
}
