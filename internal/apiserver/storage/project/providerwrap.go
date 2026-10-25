package projectstorage

import (
	generic "k8s.io/apiserver/pkg/registry/generic"
	apiserver "k8s.io/apiserver/pkg/server"
	serverstorage "k8s.io/apiserver/pkg/server/storage"
	controlplaneapiserver "k8s.io/kubernetes/pkg/controlplane/apiserver"
)

// ---------- common patching logic -------------------------------------
type baseDecorator struct {
	inner controlplaneapiserver.RESTStorageProvider
}

func (b baseDecorator) GroupName() string { return b.inner.GroupName() }

func (b baseDecorator) NewRESTStorage(
	cfg serverstorage.APIResourceConfigSource,
	getter generic.RESTOptionsGetter,
) (apiserver.APIGroupInfo, error) {

	getter = WithProjectAwareDecorator(getter)

	agi, err := b.inner.NewRESTStorage(cfg, getter)
	if err != nil {
		return agi, err
	}

	return agi, nil
}

// ---------- wrapper *with* PostStartHook -------------------------------
type withHook struct{ baseDecorator }

func (w withHook) PostStartHook() (string, apiserver.PostStartHookFunc, error) {
	return w.inner.(apiserver.PostStartHookProvider).PostStartHook()
}

// ---------- factory ----------------------------------------------------
func WrapProvider(p controlplaneapiserver.RESTStorageProvider) controlplaneapiserver.RESTStorageProvider {
	if _, ok := p.(apiserver.PostStartHookProvider); ok {
		return withHook{baseDecorator{inner: p}}
	}
	return baseDecorator{inner: p}
}
