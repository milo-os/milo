# Apps namespace reservation prerequisite

The compiled ownership guard reserves
`apps.miloapis.com/namespace-reservation` in labels and annotations on every
resource kind. Presence, including an empty value, denies create, update, and
delete. Old metadata is checked on update/delete, so removing the key and
force-finalizing a reserved Namespace are denied. Missing old objects fail
closed. Ordinary namespaces retain their existing behavior.

There is no trusted acquisition or removal identity in this change. Even
system:masters cannot create a reservation through admission. No App installation
is enabled, and no namespace is newly reserved by deployment. Inventory any
existing use of this key before deploying: such objects will be frozen, including
status/finalization. Do not use this prerequisite as evidence of completed
Apps lifecycle protection or a safe project purge protocol.

## Why namespace-local metadata can close the acquisition/delete race

Milo wraps core REST storage providers with project-aware storage
(`cmd/milo/apiserver/server.go:405-416`,
`internal/apiserver/storage/project/providerwrap.go:20-24`). Its project mux
forwards `GuaranteedUpdate` to the selected project's store
(`internal/apiserver/storage/project/mux.go:346-354`).

Kubernetes v1.35's Namespace REST DELETE performs `deleteValidation` **inside**
the storage `GuaranteedUpdate` callback, before setting deletionTimestamp and
phase Terminating. A concurrent namespace reservation mutation that wins the
etcd compare causes DELETE to retry and validate the new stored metadata. The
callback cannot rely on an earlier unreserved copy after the compare fails.
See upstream `pkg/registry/core/namespace/storage/storage.go:137-249` and
`k8s.io/apiserver/pkg/registry/rest/delete.go:178-202` for the admission adapter.

The integration test starts real local etcd and two independent Namespace REST
instances using Milo's project-aware decorator. It pauses one DELETE after its
first successful validation, commits a reservation through the other store, then
resumes DELETE. The second validation must observe the reservation and reject;
the namespace must remain non-terminating. The reverse ordering proves a stale
reservation CAS conflicts after deletion, and Kubernetes refuses a new metadata
finalizer even after refreshing the terminating namespace.

The test also proves deletion/finalization of an unreserved same-name namespace
in another project, rejection on both stores, stale resourceVersion rejection,
and preservation of an already-terminating reserved namespace against finalize.
Its `incomplete-release` value is a fixture, not an AppRelease implementation.
Upstream may return an already-terminating namespace unchanged on a repeated
DELETE while its spec finalizers remain, without invoking delete validation.
That response performs no deletion mutation; the finalization path is separately
protected. Do not interpret this storage test as proving every repeated HTTP
DELETE returns Forbidden.

This proves a storage invariant with independent REST/store instances, not an
entire distributed acquisition/release protocol. The test uses uncached inner
storage with the real Milo project mux; it does not start full HTTP API servers,
exercise authentication, or test production watch-cache configuration. The
guard callback is the real compiled plugin, invoked through Kubernetes' actual
REST admission adapters. Reserved state is seeded only through test storage
validation callbacks; no bypass flag or trusted caller is added to production.

## Required acquisition and release contract before allowing writers

1. Resolve authoritative Project and Namespace UIDs and require healthy compiled
   guard coverage on every serving endpoint. A route's project name alone is
   insufficient. Reject new acquisition when the project is quiescing, either
   ancestor is terminating, or authority/guard health is unknown.
2. Before any component write, atomically CAS a protected reservation identifier
   and recovery metadata finalizer onto the exact Namespace UID/resourceVersion.
   The identifier represents a namespace hold shared by its Apps, not a single
   App ownership pair. The exact structured reservation API and trusted writer
   credential remain to be implemented. Never infer authority from marker text.
3. After successful acquisition, record the pending installation/release work
   durably and retain the namespace hold through partial creation, crashes,
   uninstall, and incomplete AppRelease records. Marker absence must never be
   inferred from an eventually consistent component inventory alone.
4. For release, first durably stop accepting new work and fence/quiesce every
   installation writer. Complete UID-pinned cleanup or accepted Retain transfer;
   drain in-flight writes; verify all releases finished. Only then may a narrowly
   authorized release operation clear the namespace reservation. A controller
   process mutex cannot provide this across API replicas or leader handoff.
5. Prove stale-worker fencing and namespace/project generation reuse with real
   HTTP APIs before enabling acquisition. The CAS test here establishes the
   first namespace transition only; it does not prove last-holder removal safe.

The namespace DELETE decision can inspect its stored old metadata without any
App GET, inventory scan, or remote authorization service. Creation/cleanup may
consult authoritative reservation/grant state at lifecycle transitions. This
does not remove the need to authorize every component mutation or to fence stale
writers during teardown. Finalizers are only recovery backstops: they do not
prevent Kubernetes from setting deletionTimestamp or starting namespace cleanup.

## Project purge is not solved by this namespace guard

Current source below is Milo `ba4b06e8dde99dd70e9a2f34e011d635e4408a76`.

- `internal/controllers/resourcemanager/project_controller.go:100-235` starts
  generic purge after Project deletion. The first sweep and the later slow-poll
  sweep both need an Apps-specific quiesce/drain gate before they run.
- `internal/controllers/projectpurge/purge.go:130-216` first issues namespaced
  DeleteCollection, then Namespace DELETE. Parallel deletion can reach App
  records and native resources in any order. Blanket purger exemptions would
  defeat ownership; allowing the marker to block forever would deadlock cleanup.
- `projectpurge/purge.go:234-243,388-407` treats connection refused or NotFound
  while listing namespaces as a completed drain. A transient unavailable API is
  not evidence that Apps cleanup finished. `project_controller.go:118-155` can
  then reclaim ProjectControlPlane and remove the Project finalizer.
- `projectpurge/purge.go:343-380` discovers only namespaced kinds for deletion;
  cluster-scoped Apps release/grant records cannot be assumed drained by it.
- `internal/controllers/namespace/namespace_controller.go:232-243,313-315`
  enqueues terminating namespaces and invokes Kubernetes' resource deleter.
  A namespace finalizer alone cannot stop that deleter from removing contents.
- Direct deletion of ProjectControlPlane or its infrastructure namespace is
  outside this Milo project-API plugin boundary. The infrastructure admission
  and provisioning path must also require the drain acknowledgment; it is not
  proven by this change. Organization deletion/garbage collection must converge
  through the same Project drain barrier rather than bypassing it.

The next project lifecycle change needs a durable, UID-pinned handshake before
both generic purge entry and control-plane reclaim: request Apps quiescence,
complete all accepted releases while the project API still answers, then require
a trusted `AppsDrained` acknowledgment covering the current teardown generation.
Unavailable guard, authority, or endpoint must keep that acknowledgment pending.
Do not mark an unavailable project drained or remove the PCP based solely on
connection refusal. Preserve cleanup credentials until this barrier completes;
do not wait on an IAM finalizer which itself waits for Milo's drain.

This slice deliberately does not add a speculative controller identity, release
API, project status field, purger exemption, or acquisition bypass. Those parts
need the scoped authority and release contracts and independent race tests.

## Reproduce validation

```sh
go test ./internal/apiserver/admission/plugin/appsownership ./cmd/milo/apiserver -count=1 -timeout=120s

# Use an existing setup-envtest asset directory containing etcd.
KUBEBUILDER_ASSETS=/path/to/envtest/assets \
  go test -race -tags=integration ./internal/apiserver/admission/plugin/appsownership -count=1 -timeout=120s
```

The integration tag keeps ordinary unit tests independent of local binaries.
The existing `.github/workflows/testing.yaml` runs the race-enabled integration
command explicitly after installing envtest assets. When selected, missing
assets are a failure, not a silent skip. Tests start only local ephemeral etcd
and clean it up; they need no cluster credentials, Docker, or production
operations. Full Milo HTTP/HA lifecycle checks are still required before any
managed installation is enabled.
