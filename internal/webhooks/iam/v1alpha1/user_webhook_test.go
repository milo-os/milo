package v1alpha1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// ValidateCreate creates three resources as side effects, and validating
// admission runs before the etcd write — so a retried or concurrent signup
// re-enters with some already present. Without AlreadyExists tolerance it fails
// admission on the User, which zitadel-provider's userprovision.EnsureUser
// cannot recognise as success (it tests IsAlreadyExists), turning a benign
// retry into a 500.

const idemUserName = "idem-user"

func newIdemUser() *iamv1alpha1.User {
	return &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: idemUserName, UID: types.UID("user-uid-1")},
		Spec:       iamv1alpha1.UserSpec{Email: "idem@example.test"},
	}
}

func newIdemValidator(objs ...client.Object) *UserValidator {
	return &UserValidator{
		client: fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(objs...).Build(),
		scheme: runtimeScheme,
	}
}

// admissionCtx supplies the AdmissionRequest ValidateCreate reads from context.
func admissionCtx() context.Context {
	return admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{Name: idemUserName},
	})
}

// Calling ValidateCreate twice must not error the second time — a retried signup.
func TestValidateCreate_CalledTwice_IsIdempotent(t *testing.T) {
	v := newIdemValidator()
	user := newIdemUser()

	_, err := v.ValidateCreate(admissionCtx(), user)
	require.NoError(t, err, "first create must succeed")

	_, err = v.ValidateCreate(admissionCtx(), user)
	assert.NoError(t, err, "second create must be a no-op, not an admission denial")
}

// One case per site: a fix covering one but missing the others fails loudly.
func TestValidateCreate_PreExistingSelfManageBinding_Tolerated(t *testing.T) {
	existing := &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "user-self-manage-" + idemUserName},
	}
	v := newIdemValidator(existing)

	_, err := v.ValidateCreate(admissionCtx(), newIdemUser())
	assert.NoError(t, err)
}

// The webhook must not "repair" an existing binding's UID: the apiserver stamps
// a fresh UID before admission on every create attempt, including re-creates of
// an already-persisted User, so a repair keyed on it would pin the binding to a
// UID that never persists. spec.resourceSelector is immutable.
func TestValidateCreate_ExistingBindingIsNeverRewritten(t *testing.T) {
	const persistedUID = "the-uid-that-actually-persisted"
	existing := &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "user-self-manage-" + idemUserName},
		Spec: iamv1alpha1.PolicyBindingSpec{
			Subjects: []iamv1alpha1.Subject{{Kind: "User", Name: idemUserName, UID: persistedUID}},
		},
	}
	v := newIdemValidator(existing)

	// A retry carrying a different, never-to-be-persisted UID.
	retry := newIdemUser()
	retry.UID = types.UID("fresh-uid-that-will-never-persist")

	_, err := v.ValidateCreate(admissionCtx(), retry)
	require.NoError(t, err)

	var binding iamv1alpha1.PolicyBinding
	require.NoError(t, v.client.Get(context.Background(),
		client.ObjectKey{Name: "user-self-manage-" + idemUserName}, &binding))
	require.NotEmpty(t, binding.Spec.Subjects)
	assert.Equal(t, persistedUID, binding.Spec.Subjects[0].UID,
		"an existing binding must be left alone; rewriting it to a per-attempt UID destroys a correct record")
}

func TestValidateCreate_PreExistingUserPreferencePolicyBinding_Tolerated(t *testing.T) {
	existing := &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "userpreference-self-manage-" + idemUserName},
	}
	v := newIdemValidator(existing)

	_, err := v.ValidateCreate(admissionCtx(), newIdemUser())
	assert.NoError(t, err)
}

// createUserPreference returns the object and the third PolicyBinding stamps
// userPreference.UID into its ResourceRef. Create leaves that UID empty on
// conflict, so tolerating AlreadyExists without re-reading emits an
// authorization record pointing at UID "".
func TestValidateCreate_PreExistingUserPreference_RereadsForUID(t *testing.T) {
	const wantUID = types.UID("userpref-uid-42")
	existing := &iamv1alpha1.UserPreference{
		ObjectMeta: metav1.ObjectMeta{Name: "userpreference-" + idemUserName, UID: wantUID},
		Spec: iamv1alpha1.UserPreferenceSpec{
			UserRef: iamv1alpha1.UserReference{Name: idemUserName},
			Theme:   "system",
		},
	}
	v := newIdemValidator(existing)

	_, err := v.ValidateCreate(admissionCtx(), newIdemUser())
	require.NoError(t, err)

	var binding iamv1alpha1.PolicyBinding
	require.NoError(t, v.client.Get(context.Background(),
		client.ObjectKey{Name: "userpreference-self-manage-" + idemUserName}, &binding))

	require.NotNil(t, binding.Spec.ResourceSelector.ResourceRef)
	assert.Equal(t, string(wantUID), binding.Spec.ResourceSelector.ResourceRef.UID,
		"the UserPreference must be re-read on conflict; an empty UID here is a silently wrong authorization record")
}

// All three already present — the steady state user_sweep.go produces.
func TestValidateCreate_AllThreePreExisting_Tolerated(t *testing.T) {
	v := newIdemValidator(
		&iamv1alpha1.PolicyBinding{ObjectMeta: metav1.ObjectMeta{Name: "user-self-manage-" + idemUserName}},
		&iamv1alpha1.UserPreference{
			ObjectMeta: metav1.ObjectMeta{Name: "userpreference-" + idemUserName, UID: types.UID("u-1")},
		},
		&iamv1alpha1.PolicyBinding{ObjectMeta: metav1.ObjectMeta{Name: "userpreference-self-manage-" + idemUserName}},
	)

	_, err := v.ValidateCreate(admissionCtx(), newIdemUser())
	assert.NoError(t, err)
}
