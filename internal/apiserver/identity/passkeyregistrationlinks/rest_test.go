package passkeyregistrationlinks_test

import (
	"context"
	"errors"
	"testing"

	"go.miloapis.com/milo/internal/apiserver/identity/passkeyregistrationlinks"
	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
)

type fakeBackend struct {
	calls   int
	gotLink *identityv1alpha1.PasskeyRegistrationLink
	created *identityv1alpha1.PasskeyRegistrationLink
	err     error
}

func (f *fakeBackend) CreatePasskeyRegistrationLink(_ context.Context, _ authuser.Info, link *identityv1alpha1.PasskeyRegistrationLink, _ *metav1.CreateOptions) (*identityv1alpha1.PasskeyRegistrationLink, error) {
	f.calls++
	f.gotLink = link
	if f.err != nil {
		return nil, f.err
	}
	return f.created, nil
}

func TestREST_Create_DelegatesToBackend(t *testing.T) {
	want := &identityv1alpha1.PasskeyRegistrationLink{
		ObjectMeta: metav1.ObjectMeta{Name: "prl-abc"},
		Status:     identityv1alpha1.PasskeyRegistrationLinkStatus{EmailName: "email-1"},
	}
	backend := &fakeBackend{created: want}
	r := passkeyregistrationlinks.NewREST(backend)

	u := &authuser.DefaultInfo{Name: "staff@example.com", UID: "staff-1"}
	ctx := apirequest.WithUser(context.Background(), u)

	in := &identityv1alpha1.PasskeyRegistrationLink{
		Spec: identityv1alpha1.PasskeyRegistrationLinkSpec{
			UserRef:     identityv1alpha1.PasskeyRegistrationLinkUserReference{Name: "user-2"},
			RequestedBy: "staff-1",
			Reason:      "ticket-123",
		},
	}
	got, err := r.Create(ctx, in, nil, &metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got != want {
		t.Fatalf("Create result = %v, want %v", got, want)
	}
	if backend.calls != 1 {
		t.Fatalf("backend calls = %d, want 1", backend.calls)
	}
	if backend.gotLink != in {
		t.Fatalf("backend got %v, want the request object", backend.gotLink)
	}
}

func TestREST_Create_RejectsWrongType(t *testing.T) {
	backend := &fakeBackend{}
	r := passkeyregistrationlinks.NewREST(backend)

	_, err := r.Create(context.Background(), &identityv1alpha1.Passkey{}, nil, &metav1.CreateOptions{})
	if !apierrors.IsBadRequest(err) {
		t.Fatalf("Create error = %v, want BadRequest", err)
	}
	if backend.calls != 0 {
		t.Fatalf("backend calls = %d, want 0", backend.calls)
	}
}

func TestREST_IsCreateOnly(t *testing.T) {
	r := passkeyregistrationlinks.NewREST(&fakeBackend{})
	if _, ok := interface{}(r).(rest.Lister); ok {
		t.Fatal("PasskeyRegistrationLink REST storage must not implement rest.Lister (create-only contract)")
	}
	if _, ok := interface{}(r).(rest.Getter); ok {
		t.Fatal("PasskeyRegistrationLink REST storage must not implement rest.Getter (create-only contract)")
	}
	if _, ok := interface{}(r).(rest.GracefulDeleter); ok {
		t.Fatal("PasskeyRegistrationLink REST storage must not implement rest.GracefulDeleter (create-only contract)")
	}
	if _, ok := interface{}(r).(rest.Updater); ok {
		t.Fatal("PasskeyRegistrationLink REST storage must not implement rest.Updater (create-only contract)")
	}
}

func TestREST_Create_ReturnsBackendErrorsUnchanged(t *testing.T) {
	tests := []struct {
		name    string
		err     *apierrors.StatusError
		matches func(error) bool
	}{
		{name: "bad request", err: apierrors.NewBadRequest("user has no passkeys"), matches: apierrors.IsBadRequest},
		{name: "internal error", err: apierrors.NewInternalError(errors.New("provider failed")), matches: apierrors.IsInternalError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := passkeyregistrationlinks.NewREST(&fakeBackend{err: tt.err})
			ctx := apirequest.WithUser(context.Background(), &authuser.DefaultInfo{Name: "staff@example.com"})
			in := &identityv1alpha1.PasskeyRegistrationLink{
				Spec: identityv1alpha1.PasskeyRegistrationLinkSpec{
					UserRef: identityv1alpha1.PasskeyRegistrationLinkUserReference{Name: "user-2"},
				},
			}

			got, err := r.Create(ctx, in, nil, &metav1.CreateOptions{})
			if got != nil {
				t.Fatalf("Create result = %v, want nil", got)
			}
			var statusErr *apierrors.StatusError
			if !errors.As(err, &statusErr) || statusErr != tt.err {
				t.Fatalf("Create error = %T %v, want the backend's StatusError unchanged", err, err)
			}
			if !tt.matches(err) {
				t.Fatalf("Create error = %v, lost its reason", err)
			}
		})
	}
}
