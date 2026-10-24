# Apps ownership bootstrap guard

This slice reserves `apps.miloapis.com/project-uid` and
`apps.miloapis.com/app-uid` before Apps installation is available. The intended
owner is the immutable pair of project UID and App UID; names are insufficient.
The compiled `AppsOwnershipEnforcement` admission plugin rejects create, update,
and delete when either old or new metadata contains either reserved key, in
labels or annotations. Empty values and partial pairs are reserved too.

There are no authorized writers in this bootstrap slice. Customer, controller,
release-worker, impersonated and `system:masters` identities all receive the
same denial. Status and finalizer writes are deliberately denied on marked
objects. No customer App may create a managed resource until the later identity,
release, service-controller and ancestor-deletion contracts are implemented and
verified. This is groundwork, not completed A1, A3 or G1 protection.

The guard inspects materialized admission objects, including the old object on
update/delete. It does not consult VAP, a binding, an App, inventory, or a cache.
Missing policies cannot disable it. Kubernetes supplies DeleteOptions as operation
options, and its deletion adapter supplies each stored object as the old object;
the adapter is covered by a regression test. It applies without relying on ProjectID,
including direct internal endpoints. Milo startup rejects configuration that
removes, disables or omits this plugin, including deprecated admission-control
selection. Existing unmarked objects retain their normal behavior. Non-object
or missing-old-object mutations fail closed because ownership cannot be checked.

## Current source evidence

Baseline: `milo-os/milo@ba4b06e8dde99dd70e9a2f34e011d635e4408a76`.
Product/architecture source: enhancements PR 929 head
`4db04b51b9c1274e24bf1e34ffafcb0f1b2f0d05`, both Apps READMEs read in full.

- `pkg/server/filters/projects.go:20-75` rewrites the public project API path and
  supplies ProjectID; `cmd/milo/apiserver/config.go:434-458` wires the handler
  chain into the API server. Authentication and impersonation precede
  authorization (`config.go:535-573`).
- `cmd/milo/apiserver/server.go:351-365` wires the CRD API server through the
  project-aware storage decorator. `internal/apiserver/storage/project/mux.go:
  241-253,291-319` selects the `/projects/<name>` storage prefix. Workload and
  HTTPRoute are CRDs; this is their admission/storage route when served here.
  An aggregated or external backend is not proven by this path and must be
  separately admitted before it becomes an Apps component.
- `cmd/milo/apiserver/admission.go:19-70` orders compiled plugins before VAP and
  webhooks. `config/discovery/core/discovery-context-policy.yaml:27-29` exposes
  admissionregistration only in Platform discovery. VAP installation is not a
  prerequisite or authorization mechanism for this bootstrap guard.
- `internal/apiserver/admission/plugin/namespace/lifecycle/admission.go:94-120`
  permits deletion of ordinary namespaces. It has no managed-resource check.
- `internal/controllers/resourcemanager/project_controller.go:100-236` begins a
  generic purge after Project deletion and waits for resource drain before
  deleting ProjectControlPlane. This is a cleanup backstop, not an admission
  barrier preventing the Project deletion timestamp or an Apps-safe purge order.
- `pkg/multicluster-runtime/milo/provider.go:43-69,89-118,253-265` selects Project
  versus ProjectControlPlane and external versus internal addresses. It discovers
  serially. `provider.go:163-173` defines HasSynced as processed once, including
  failed registration, rather than project availability or guard readiness.
- `provider.go:282-297` passes the reconciliation context to WaitForCacheSync
  without a provider-owned timeout. The diagnostic in `experiments/` records
  this with a deliberately stalled cache and bounded test cancellation. This
  diagnostic does not contact a project and is not real-environment evidence.

Line references above describe the baseline revision, before this patch.

## Contracts needed before enabling writes

1. Bind each trusted role to a platform-issued credential and immutable
   principal identity with project-scoped native permissions. Caller-controlled
   user names, groups, field managers and user extras are not capabilities.
   Preserve authenticated pre-impersonation provenance in server context before
   Kubernetes' impersonation filter replaces user.Info and strips headers; deny
   impersonation into reserved roles independently of customer RBAC.
2. Check request project UID against the marker through trusted bootstrap state;
   public URL routing currently supplies the project name. Pin resource UID in
   inventory and use UID/resourceVersion preconditions for writes and deletes.
3. Authorize exact service identities for exact GVR status changes and finalizer
   names. Compare old/new objects after removing only the allowed paths; do not
   authorize all metadata or all changes during deletion. No external ownerRefs
   may be added to App-owned top-level resources.
4. Serialize ancestor teardown and ownership acquisition. An unsynchronized
   list-before-namespace-delete has a create/delete race, and inventory can lag
   the resource write. A durable controller-owned project/namespace reservation
   established before resource creation needs a quiescing handshake for cleanup
   and release. Namespace DELETE must fail while ownership or releases remain;
   Platform Project/PCP teardown must wait before deleting the control plane.
5. Define controller-only immutable AppRelease snapshot writes and independent
   release authority. Release workers verify App UID absence and resource UID
   before marker removal. No permission to create or mutate component specs is
   needed by the release worker.
6. Verify all enabled subresources expose old parent ownership metadata. A scale
   or custom subresource that hides the parent's labels needs explicit handling.
   Do not claim generic resource coverage from Workload/HTTPRoute unit fixtures.
7. Exercise real public project APIs and distinct credentials, PATCH/SSA,
   garbage collection, namespace/project teardown, missing policy/binding,
   legitimate status/finalizer writers and release ordering in test-infra.

## Validation and rollout

Executed on the isolated clone:

- `go test ./internal/apiserver/admission/plugin/appsownership ./cmd/milo/apiserver -count=1 -timeout=120s` — passed.
- `KUBEBUILDER_ASSETS='<local envtest 1.33.0 directory>' go test ./pkg/multicluster-runtime/milo -count=1 -timeout=120s` — passed, including the existing real-envtest engagement-deadlock regression.
- Temporary diagnostic `go test ./pkg/multicluster-runtime/milo -run TestAppsUnavailableCacheExperiment -count=1 -v -timeout=15s` — passed by observing no cache-sync deadline and completion only after cancellation. Reproduce by copying `experiments/unavailable_cache_test.go.txt` to that package as a `_test.go` file, then remove the copy after running.

The first sandboxed test attempt could not write the existing Go cache; rerunning
with permitted access to that cache passed. No production calls or writes were
performed. TokenReview, Eviction, Binding and Scale request-shape compatibility tests pass.
Scale intentionally illustrates the coverage limit: its parent marker is not
present in the admission object. Full repository CI and the real Milo/test-infra
gate remain required.

Before deployment, inventory existing uses of the two reserved keys. This slice
will freeze any such objects, including their status and cleanup. It creates none
itself. Deploying a binary without the guard would remove these reservations;
that rollback is unacceptable after any managed resources are enabled. Keep Apps
installation disabled throughout this bootstrap rollout.
