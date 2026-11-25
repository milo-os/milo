Virtual Sessions API (identity.miloapis.com/v1alpha1)

This package implements Milo's public, virtual Sessions API. It exposes
identity.miloapis.com/v1alpha1, Resource=sessions without persisting anything
to the API server's storage. All operations are delegated to a provider API so RBAC and audit
reflect the end-user.

Components and flow
- StorageProvider (internal/apiserver/storage/identity/storageprovider.go)
  - Installs API group identity.miloapis.com/v1alpha1 and wires the sessions
    resource to the REST storage.
- REST storage (this package, rest.go)
  - Implements List/Get/Delete for a cluster-scoped virtual resource.
  - Extracts the request user from context and delegates to a Provider.
  - Implements Table output so kubectl get sessions.identity.miloapis.com
    works without -o json.
- Provider (this package, dynamic.go)
  - DynamicProvider uses a dynamic client with impersonation to call a
    provider GVR (configured via flags) and converts unstructured ↔ typed
    identity/v1alpha1 objects.
  - Strict pass-through: Milo does not mutate or default provider fields.

Configuration
- Enable the feature and point to a provider GVR via flags on the apiserver:
  - --feature-sessions=true
  - --sessions-provider-url=https://identity.miloapis.com
  - --sessions-provider-ca-file=
  - --sessions-provider-client-cert=
  - --sessions-provider-client-key=
  - Optional: --provider-timeout, --provider-retries,
    --forward-extras=org,tenant
- At startup Milo builds a GroupVersionResource from the flags and uses it in
  DynamicProvider for all requests.


Naming & structure
- internal/apiserver/storage/identity/storageprovider.go — group installer
- internal/apiserver/identity/sessions/rest.go — REST storage
- internal/apiserver/identity/sessions/dynamic.go — provider implementation

