package projectstorage

import (
	"time"

	generic "k8s.io/apiserver/pkg/registry/generic"
	apiserver "k8s.io/apiserver/pkg/server"
	serverstorage "k8s.io/apiserver/pkg/server/storage"
	controlplaneapiserver "k8s.io/kubernetes/pkg/controlplane/apiserver"
)

// ---------- common patching logic -------------------------------------
type baseDecorator struct {
	inner    controlplaneapiserver.RESTStorageProvider
	projects ProjectLookup
	drain    time.Duration
}

func (b baseDecorator) GroupName() string { return b.inner.GroupName() }

func (b baseDecorator) NewRESTStorage(
	cfg serverstorage.APIResourceConfigSource,
	getter generic.RESTOptionsGetter,
) (apiserver.APIGroupInfo, error) {

	getter = WithProjectAwareDecorator(getter, b.projects, b.drain)

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
// WrapProvider makes p's storage project-aware, each request's Project looked up in projects, and a
// gone Project's children destroyed after drain.
func WrapProvider(p controlplaneapiserver.RESTStorageProvider, projects ProjectLookup,
	drain time.Duration) controlplaneapiserver.RESTStorageProvider {
	b := baseDecorator{inner: p, projects: projects, drain: drain}
	if _, ok := p.(apiserver.PostStartHookProvider); ok {
		return withHook{b}
	}
	return b
}
