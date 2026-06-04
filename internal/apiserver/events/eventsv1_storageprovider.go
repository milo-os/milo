package events

import (
	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	serverstorage "k8s.io/apiserver/pkg/server/storage"
)

// EventsV1StorageProvider provides REST storage for the events.k8s.io/v1 API group.
type EventsV1StorageProvider struct {
	Backend EventsV1Backend
}

// NewRESTStorage returns APIGroupInfo for the events.k8s.io/v1 API group
func (p *EventsV1StorageProvider) NewRESTStorage(
	apiResourceConfigSource serverstorage.APIResourceConfigSource,
	restOptionsGetter generic.RESTOptionsGetter,
) (genericapiserver.APIGroupInfo, error) {
	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(eventsv1.GroupName, Scheme, ParameterCodec, Codecs)

	if p.Backend != nil {
		v1Storage := map[string]rest.Storage{
			"events": NewEventsV1REST(p.Backend),
		}
		apiGroupInfo.VersionedResourcesStorageMap["v1"] = v1Storage
	}

	return apiGroupInfo, nil
}

// GroupName returns the API group name
func (p *EventsV1StorageProvider) GroupName() string {
	return eventsv1.GroupName
}
