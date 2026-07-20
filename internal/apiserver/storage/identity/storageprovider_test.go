package identity_test

import (
	"context"
	"testing"

	identitystorage "go.miloapis.com/milo/internal/apiserver/storage/identity"
	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	authuser "k8s.io/apiserver/pkg/authentication/user"
)

type fakePasskeysBackend struct{}

func (fakePasskeysBackend) ListPasskeys(context.Context, authuser.Info, *metav1.ListOptions) (*identityv1alpha1.PasskeyList, error) {
	return &identityv1alpha1.PasskeyList{}, nil
}
func (fakePasskeysBackend) GetPasskey(context.Context, authuser.Info, string) (*identityv1alpha1.Passkey, error) {
	return &identityv1alpha1.Passkey{}, nil
}

func TestStorageProvider_RegistersPasskeysWhenBackendSet(t *testing.T) {
	provider := identitystorage.StorageProvider{Passkeys: fakePasskeysBackend{}}

	info, err := provider.NewRESTStorage(nil, nil)
	if err != nil {
		t.Fatalf("NewRESTStorage: %v", err)
	}

	versioned, ok := info.VersionedResourcesStorageMap["v1alpha1"]
	if !ok {
		t.Fatal("missing v1alpha1 storage map")
	}
	if _, ok := versioned["passkeys"]; !ok {
		t.Fatal(`expected "passkeys" resource to be registered`)
	}
}
