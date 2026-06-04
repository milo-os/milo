package events

import (
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	serverstorage "k8s.io/apiserver/pkg/server/storage"
	controlplaneapiserver "k8s.io/kubernetes/pkg/controlplane/apiserver"
)

// CoreProviderWrapper wraps the core storage provider to inject events proxy storage
type CoreProviderWrapper struct {
	Inner         controlplaneapiserver.RESTStorageProvider
	EventsBackend Backend
}

// NewRESTStorage delegates to the inner provider and injects events proxy storage
func (w *CoreProviderWrapper) NewRESTStorage(
	apiResourceConfigSource serverstorage.APIResourceConfigSource,
	restOptionsGetter generic.RESTOptionsGetter,
) (genericapiserver.APIGroupInfo, error) {
	apiGroupInfo, err := w.Inner.NewRESTStorage(apiResourceConfigSource, restOptionsGetter)
	if err != nil {
		return apiGroupInfo, err
	}

	if w.EventsBackend != nil {
		v1Storage := apiGroupInfo.VersionedResourcesStorageMap["v1"]
		if v1Storage == nil {
			v1Storage = make(map[string]rest.Storage)
			apiGroupInfo.VersionedResourcesStorageMap["v1"] = v1Storage
		}
		v1Storage["events"] = NewREST(w.EventsBackend)
	}

	return apiGroupInfo, nil
}

// GroupName delegates to the inner provider
func (w *CoreProviderWrapper) GroupName() string {
	return w.Inner.GroupName()
}

// WrapCoreProvider wraps a core storage provider to inject events proxy storage
func WrapCoreProvider(inner controlplaneapiserver.RESTStorageProvider, backend Backend) controlplaneapiserver.RESTStorageProvider {
	if backend == nil {
		return inner
	}
	return &CoreProviderWrapper{
		Inner:         inner,
		EventsBackend: backend,
	}
}
