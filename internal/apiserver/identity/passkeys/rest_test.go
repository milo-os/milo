package passkeys_test

import (
	"context"
	"testing"

	"go.miloapis.com/milo/internal/apiserver/identity/passkeys"
	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"

	"k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
)

type fakeBackend struct {
	listOpts *metav1.ListOptions
	getName  string
	list     *identityv1alpha1.PasskeyList
	get      *identityv1alpha1.Passkey
}

func (f *fakeBackend) ListPasskeys(_ context.Context, _ authuser.Info, opts *metav1.ListOptions) (*identityv1alpha1.PasskeyList, error) {
	f.listOpts = opts
	return f.list, nil
}

func (f *fakeBackend) GetPasskey(_ context.Context, _ authuser.Info, name string) (*identityv1alpha1.Passkey, error) {
	f.getName = name
	return f.get, nil
}

func TestREST_List_ForwardsFieldSelector(t *testing.T) {
	want := &identityv1alpha1.PasskeyList{Items: []identityv1alpha1.Passkey{
		{Status: identityv1alpha1.PasskeyStatus{DisplayName: "Touch ID"}},
	}}
	backend := &fakeBackend{list: want}
	r := passkeys.NewREST(backend)

	u := &authuser.DefaultInfo{Name: "alice", UID: "user-1"}
	ctx := apirequest.WithUser(context.Background(), u)

	sel, err := fields.ParseSelector("status.userUID=user-2")
	if err != nil {
		t.Fatalf("ParseSelector: %v", err)
	}
	opts := &internalversion.ListOptions{FieldSelector: sel}

	got, err := r.List(ctx, opts)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got != want {
		t.Fatalf("List result = %v, want %v", got, want)
	}
	if backend.listOpts == nil || backend.listOpts.FieldSelector != "status.userUID=user-2" {
		t.Fatalf("backend field selector = %+v, want status.userUID=user-2", backend.listOpts)
	}
}

func TestREST_Get_DelegatesToBackend(t *testing.T) {
	want := &identityv1alpha1.Passkey{Status: identityv1alpha1.PasskeyStatus{DisplayName: "Touch ID"}}
	backend := &fakeBackend{get: want}
	r := passkeys.NewREST(backend)

	u := &authuser.DefaultInfo{Name: "alice", UID: "user-1"}
	ctx := apirequest.WithUser(context.Background(), u)

	got, err := r.Get(ctx, "passkey-abc", &metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != want {
		t.Fatalf("Get result = %v, want %v", got, want)
	}
	if backend.getName != "passkey-abc" {
		t.Fatalf("backend.getName = %q, want %q", backend.getName, "passkey-abc")
	}
}

func TestREST_IsReadOnly(t *testing.T) {
	r := passkeys.NewREST(&fakeBackend{})
	if _, ok := interface{}(r).(rest.GracefulDeleter); ok {
		t.Fatal("Passkey REST storage must not implement rest.GracefulDeleter (read-only contract)")
	}
	if _, ok := interface{}(r).(rest.Creater); ok {
		t.Fatal("Passkey REST storage must not implement rest.Creater (read-only contract)")
	}
	if _, ok := interface{}(r).(rest.Updater); ok {
		t.Fatal("Passkey REST storage must not implement rest.Updater (read-only contract)")
	}
}
