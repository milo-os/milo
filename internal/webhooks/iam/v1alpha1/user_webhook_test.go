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

// C-IDEM. ValidateCreate is a *validating* webhook with side effects: it
// creates three resources. Kubernetes runs validating admission BEFORE the
// etcd write, so a retried or concurrent signup re-enters this code with some
// or all of those three already present.
//
// Without AlreadyExists tolerance the webhook fails admission on the User
// itself. That is worse than a noisy duplicate, because zitadel-provider's
// userprovision.EnsureUser treats apierrors.IsAlreadyExists(err) as success —
// and an admission denial never satisfies that predicate. A benign retry
// becomes a 500. Both callers depend on this: the actions handler and
// user_sweep.go, whose whole design is calling EnsureUser repeatedly.

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

// The headline case: calling ValidateCreate twice must not error the second
// time. This is exactly what a retried signup does.
func TestValidateCreate_CalledTwice_IsIdempotent(t *testing.T) {
	v := newIdemValidator()
	user := newIdemUser()

	_, err := v.ValidateCreate(admissionCtx(), user)
	require.NoError(t, err, "first create must succeed")

	_, err = v.ValidateCreate(admissionCtx(), user)
	assert.NoError(t, err, "second create must be a no-op, not an admission denial")
}

// One case per site, so a fix that covers one and misses the others fails
// loudly rather than passing on the aggregate.
func TestValidateCreate_PreExistingSelfManageBinding_Tolerated(t *testing.T) {
	existing := &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "user-self-manage-" + idemUserName},
	}
	v := newIdemValidator(existing)

	_, err := v.ValidateCreate(admissionCtx(), newIdemUser())
	assert.NoError(t, err)
}

// The webhook must NOT try to "repair" an existing binding's UID.
//
// The apiserver stamps a fresh UID on every create attempt before admission
// runs, so a re-create of an ALREADY-PERSISTED User — which is exactly what
// user_sweep.go does on every pass — arrives carrying a UID that will never be
// persisted. A repair keyed on that UID would delete a correct binding and
// recreate it pinned to a phantom, and spec.resourceSelector is immutable so
// the damage would be permanent and would recur every sweep.
//
// This test pins the safe behaviour: existing binding observed, untouched.
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

// The trap, and the one place where "it stopped erroring" and "it is correct"
// diverge. createUserPreference RETURNS the object and the third PolicyBinding
// stamps userPreference.UID into its ResourceRef. Create populates that UID on
// success but leaves it empty on conflict — so merely tolerating AlreadyExists
// would emit an authorization record pointing at UID "", which no
// assert.NoError would ever catch.
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

// All three already present — the steady state user_sweep.go produces every
// time it re-runs against an existing user.
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
