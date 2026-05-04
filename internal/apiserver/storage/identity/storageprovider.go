package identity

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	generic "k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	serverstorage "k8s.io/apiserver/pkg/server/storage"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	controlplaneapiserver "k8s.io/kubernetes/pkg/controlplane/apiserver"

	serviceaccountkeysregistry "go.miloapis.com/milo/internal/apiserver/identity/serviceaccountkeys"
	sessionsregistry "go.miloapis.com/milo/internal/apiserver/identity/sessions"
	useridentitiesregistry "go.miloapis.com/milo/internal/apiserver/identity/useridentities"
	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"
)

type StorageProvider struct {
	Sessions           sessionsregistry.Backend
	UserIdentities     useridentitiesregistry.Backend
	ServiceAccountKeys serviceaccountkeysregistry.Backend
}

func (p StorageProvider) GroupName() string { return identityv1alpha1.SchemeGroupVersion.Group }

func (p StorageProvider) NewRESTStorage(
	_ serverstorage.APIResourceConfigSource,
	_ generic.RESTOptionsGetter,
) (genericapiserver.APIGroupInfo, error) {
	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(
		identityv1alpha1.SchemeGroupVersion.Group,
		legacyscheme.Scheme,
		metav1.ParameterCodec,
		legacyscheme.Codecs,
	)

	storage := map[string]rest.Storage{
		"sessions":            sessionsregistry.NewREST(p.Sessions),
		"useridentities":      useridentitiesregistry.NewREST(p.UserIdentities),
		"serviceaccountkeys":  serviceaccountkeysregistry.NewREST(p.ServiceAccountKeys),
	}

	apiGroupInfo.VersionedResourcesStorageMap = map[string]map[string]rest.Storage{
		identityv1alpha1.SchemeGroupVersion.Version: storage,
	}

	return apiGroupInfo, nil
}

var _ controlplaneapiserver.RESTStorageProvider = StorageProvider{}
