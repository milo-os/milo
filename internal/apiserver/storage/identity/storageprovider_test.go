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

type fakePasskeyRegistrationLinksBackend struct{}

func (fakePasskeyRegistrationLinksBackend) CreatePasskeyRegistrationLink(context.Context, authuser.Info, *identityv1alpha1.PasskeyRegistrationLink, *metav1.CreateOptions) (*identityv1alpha1.PasskeyRegistrationLink, error) {
	return &identityv1alpha1.PasskeyRegistrationLink{}, nil
}

func TestStorageProvider_PasskeyRegistrationLinksOnlyWhenBackendSet(t *testing.T) {
	cases := map[string]struct {
		provider identitystorage.StorageProvider
		want     bool
	}{
		"backend set": {identitystorage.StorageProvider{PasskeyRegistrationLinks: fakePasskeyRegistrationLinksBackend{}}, true},
		"backend nil": {identitystorage.StorageProvider{}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			info, err := tc.provider.NewRESTStorage(nil, nil)
			if err != nil {
				t.Fatalf("NewRESTStorage: %v", err)
			}
			_, got := info.VersionedResourcesStorageMap["v1alpha1"]["passkeyregistrationlinks"]
			if got != tc.want {
				t.Fatalf(`"passkeyregistrationlinks" registered = %v, want %v`, got, tc.want)
			}
		})
	}
}
