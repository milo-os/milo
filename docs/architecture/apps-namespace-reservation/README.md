# Namespace reservation guard with API-only evidence

This replaces the test design in #855 (`b4ccbe57`) on top of the API-only
ownership/native-writer stack (`1128fb37`, #869; original equivalent source `23096da4`). The original production
change was a reserved metadata key checked by admission; it did not depend on
a storage factory. Its old test constructed Namespace REST/storage instances
and bypassed their validation callbacks. None of those helpers, imports,
backend transactions, or acquisition designs are carried forward here.

## Production contract

`apps.miloapis.com/namespace-reservation` is reserved in labels and annotations
on every resource kind. Presence, including an empty value, rejects creation,
updates and deletion. Updates inspect both old and new metadata; deletion
inspects the old object. Missing old objects fail closed. This also protects
Namespace status and `/finalize` updates from removing or bypassing the hold.

The check runs before optional native status/finalizer exceptions. This ordering
preserves #855's all-kinds contract when combined with #869: an unchanged hold
on a Workload must not accidentally qualify for those exceptions. Unit tests
prove rejection before any root Project lookup, and the HTTP test proves that
configured native writers still work on an otherwise identical unreserved
Workload. There is no new caller, username, group, release or acquisition
exception. The ordinary authentication and authorization chain is unchanged.

No deployment acquires a reservation and no installation is enabled. Inventory
existing uses of this key before rolling out the guard: those objects will be
frozen, including native status and finalizer writes. This is a deny-all
prerequisite, not an implementation of reservation acquisition or release.

## What the real API test establishes

`TestAppsNamespaceReservationAPIFixture` starts two actual Milo HTTP servers
using the existing disposable API fixture. They share the ordinary ephemeral
server backend through their normal startup configuration. Test code has no KV
client, backend keys, storage factory, direct registry calls or validation-skipping
storage interface. All data operations use controller-runtime clients.

The finite setup allowlist and one-way gate remain test-binary-only. A Namespace
CREATE has its own name as the Kubernetes admission namespace; the test
allowlist accounts for that native attribute convention while still requiring
an exact core resource, name and setup identity. Every setup request completes
before both gates are sealed. Acknowledgements are local process-control files,
not HTTP endpoints or trusted client headers. No command can reopen a gate.

Held and already-terminating Namespaces are created through the API. The latter
uses an ordinary UID/resourceVersion-preconditioned DELETE during setup, so
Kubernetes assigns its UID, resourceVersion and deletion timestamp. This does
not establish a production Apps-authorized deletion path.

For the concurrent case:

1. Replica A receives a customer DELETE on an unreserved Namespace, with its
   exact UID. A test-only admission decorator pauses after the real validator
   accepts the old object. The hook never reads or changes server storage.
2. Replica B commits the reservation annotation and a metadata finalizer through
   a normal resourceVersion-preconditioned API Update during setup. A client
   transport deliberately discards that successful response. GET recovers the
   exact Namespace UID, reservation and new resourceVersion without another
   write; an error response alone is not treated as proof of rollback.
3. Both setup gates seal. Host process control resumes A. Native Namespace
   deletion revalidates the changed object and receives the ownership denial.
   The test counts at least two actual admission invocations and verifies the
   complete recovered object remains unchanged and non-terminating.
4. Retries after sealing, including the lost-response setup retry, are denied
   on both replicas and cannot change the recovered resourceVersion.

The reverse ordering starts DELETE first. A stale reservation Update conflicts.
A refreshed attempt to add a new metadata finalizer to the terminating Namespace
is invalid under native Kubernetes rules. Normal `/finalize` removes that
unreserved Namespace, same-name recreation gets a fresh UID, and the stale
reservation request cannot adopt it. After sealing, even the replacement's fresh
UID/resourceVersion cannot authorize acquiring a hold.

Kubernetes v1.35 Namespace DELETE performs validation inside its conditional
update callback (`k8s.io/kubernetes/pkg/registry/core/namespace/storage/storage.go:
137–249`). The test observes that API behavior rather than calling those
internals. It is not a claim that an arbitrary root Project read is atomic with
a later component mutation, or that every backend/resource has this behavior.

## Additional checks and native API limits

Both replicas reject namespace hold forgery and acquisition from customers,
setup credentials after sealing, system:masters, and configured native writer
credentials. Customer and sealed setup requests also prove rejection of marker
stripping, PUT, merge PATCH, SSA, status updates, force-finalization, and
live-namespace DELETE. Configured native writers are separately denied status
and finalizer updates on a held Workload.
GET comparisons include UID/resourceVersion and all persisted fields. Tests
also cover empty markers, held ConfigMap DeleteCollection, root Project marker
forgery and an ordinary same-name Namespace in another Project that can delete
and finalize normally. Unit tests retain missing-old and label/annotation cases.

Namespace DeleteCollection is not served by the native API and returns method
not supported; that result is not counted as an ownership-admission proof.
Likewise, repeated DELETE of an already-terminating Namespace can return its
existing object without invoking DELETE admission. The test allows that native
no-op response and requires exact persisted equality. `/finalize` remains denied.

Authorization is AlwaysAllow only inside the isolated fixture, so these tests
prove admission behavior, not production IAM or native credential custody.
The Namespace controller, Project purge and native controllers are not running.
A Namespace finalizer alone does not prevent Kubernetes from starting content
cleanup once deletionTimestamp is set. The concurrent test therefore verifies
the reservation-winning Namespace remains non-terminating.

## Boundaries retained

This change does not authorize acquisition, release, partial installations,
incomplete release records, Project teardown or control-plane reclamation.
It neither introduces an Apps inventory lookup nor permits held object writes.
Project purge can act on namespaced contents before attempting Namespace DELETE;
this guard is not a Project drain barrier. No raw-storage fence or cross-object
transaction is implied by the Namespace-local API behavior.

Any future activation still needs an explicit API/controller authority and
lifecycle protocol. No such design is implemented or approved by this test.

## Reproduction

With existing Kubernetes 1.33.0 envtest assets:

```sh
KUBEBUILDER_ASSETS=/path/to/envtest/assets TASK_X_REMOTE_TASKFILES=1 \
  task test:apps-ownership-api
```

The task includes namespace, native writer, default ownership and provenance
HTTP/race tests and the ownership/provenance unit suites. The existing CI job
sets envtest assets and includes the untagged test in `go test ./...`; no
integration-tag storage test or new deployment job is required. No kubeconfig,
Docker or live cluster is used.

Local verification on the #869 base `1128fb37` used the Darwin arm64 assets at
`/Users/scotwells/Library/Application Support/io.kubebuilder.envtest/k8s/1.33.0-darwin-arm64`.
The complete task passed: HTTP race suite 105.019s, ownership unit race 1.632s,
and provenance/filter unit race 1.784s. The namespace-only HTTP race test passed
again after the negative control in 28.347s; affected-package vet also passed.

For the negative control, only reservation rejection for DELETE was temporarily
disabled. The namespace test failed in 11.687s at the resumed concurrent DELETE:
`expected guard denial: <nil>`. The source was restored byte-for-byte before the
passing rerun. This verifies that the race assertion detects the missing
protection, rather than succeeding solely because the fixture cannot delete.

Independent lead verification passed the combined Taskfile HTTP race suite in
105.700s, ownership unit race in 1.629s and provenance/filter race in 2.044s.
The author also passed the restored standalone namespace API test in 28.347s,
affected vet, and a negative control: bypassing only reservation DELETE denial
made the paused-race assertion fail in 11.687s. Source restoration was verified
byte-for-byte before the passing lead run. No live deployment was performed.
