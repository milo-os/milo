# Compiled custody for permanently pending Apps

This slice protects the existing `pending-only-v1` protocol independently of
optional Apps webhooks. It does not enable installation, grant issuance,
AppRevision writes, a new request protocol, or native resource writes. There is
no privileged username exception and no backend access. Normal native API
authorization still applies.

The mandatory guard reserves the `apps` and `apprevisions` resource families on
all versions/subresources, plus their exact native CRD registrations. Omitted
`pendingRequests` configuration denies their mutations. The only supported
configuration value is the source-pinned `PendingRequestsV1` constant. There is
no deployment overlay or ordinary runtime flag enabling it here. Activation
requires inspecting existing CRDs and objects first; mismatched registration or
legacy unprotected objects are not adopted or repaired by this code.

When explicitly configured, exact root POST registration and safe native CRD
status are permitted using the same predicate as grant registration. Schema,
conversion, ownerrefs, finalizers, metadata and deletion are protected. The whole
Apps-group APIService reservation remains unchanged, including its known local
autoregistration retry limitation documented in `../apps-grant-registration`.

App CREATE requires trusted Project routing, private authenticated provenance
without impersonation, stable effective actor UID, a matching current root
Project UID, and the complete canonical pending spec/digest/provenance/finalizer.
It rejects unknown protocols and accepted/history/inventory state. The exact
`project-uid`, `app-uid` and `namespace-reservation` keys under
`apps.miloapis.com/` are forbidden in both labels and annotations, even when
empty, before any configured native status/finalizer writer exception. Validation
runs after mutation and independently checks the final object: an Apps mutator
may prepare it but its annotations are not themselves proof that a hook ran.
A correctly populated CREATE can pass without the mutator; it remains an inert
pending request, never an installation.

The exact JSON parser and canonical numeric rules are source-pinned from Apps.
Inputs remain JSON text; large integers and exact decimals never pass through a
floating-point representation. Invalid Unicode, duplicates, trailing values,
unknown spec fields, noncanonical input text and mismatched digests are denied.
These checks apply to the final decoded object: native CRD pruning may remove
unknown raw fields before admission. No pre-pruning strict transport guarantee is
claimed. Only the same-Project revision selection is supported. Errors contain fixed
operation descriptions, never private input values.

UPDATE preserves the complete object except managedFields and the narrow
status/cancellation transitions. This conservative slice also freezes ordinary
labels/annotations and other finalizers; it does not introduce a metadata edit
exception. Status can become only deterministic `Pending`. DeleteCollection is not enabled;
use named DELETE with nonempty UID and resourceVersion preconditions matching
the exact old object. Missing, malformed, stale or replacement preconditions
are denied; request options come from native admission operation options. DELETE starts native
termination while the hold retains the exact request. Once already terminating,
an otherwise authorized updater may remove only the pending finalizer while
preserving every other field/finalizer and UID/RV. Removing the last finalizer
implicitly deletes that UID. This is safe only because pending-only-v1 can never
accept; the rule must not be copied into a future acceptance protocol. A new
client can recover persisted pending cancellation after worker loss; it cannot
reopen a cancelled request or act on a same-name replacement UID.

Root Project termination permits existing pending cancellation, but new CREATE
is denied. A missing/unavailable/replaced Project fails closed. These native
reads are not atomic with Project teardown. No drain/in-flight-write fence or
cleanup after the Project UID itself disappears is claimed.

## Deliberate source eligibility gap

This guard does not read Templates/revisions, call SAR, validate input-schema
bindings, or derive a plan. The current Apps source-eligibility webhook remains
responsible for those checks. If it is removed, a syntactically valid pending
request can reference nonexistent source UIDs; it still cannot accept, create
history or mint a grant. This is identity/spec/lifetime custody, not independent
source admission or durable install authority. The grant guard continues to deny
all instance mutations, including under the pending configuration.

## Sources and evidence scope

- Milo runtime base #876 `042e005d93856f46f74d07ce20760881b20de89d`; signed
  test-only follow-up `8416ae0c63a120a40a61c6793dc823a049c0c514` is the branch base.
- Apps connected source `71d7bf5ed3563bd44e8071867212a146fc6ecf14`:
  `internal/pendingapp/admission.go`, `controller.go`, `internal/appcontract`,
  `internal/snapshot`, `internal/jsontext`, and experimental App/AppRevision CRDs.
- Exact schema SHA-256:
  Apps `6ac865e64f72656a55d8760cca188bb6d02dc623e2dd22a86c9c8fa14479eb38`;
  AppRevision `2b3833d67ca202d0758095fb495f5be92c79d11a075a1b82caff0141e3f875c9`.

The fixed precision/spec-digest vector was independently checked by the original
Apps `internal/appcontract.SpecDigest` at the pinned revision, in a separate local
clone. The oracle test is recorded with the verification results.

The native fixture seals its existing API-only setup before registration. Real
CRD controllers establish both schemas through production status admission.
A real dynamic hook is installed, observed denying a valid request, then removed
through the native API. Subsequent adversarial requests must receive the exact
compiled denial, not schema/RBAC/404 failures. That fixture hook is not the Apps
source validator. The fixture deliberately uses synthetic source UIDs to expose
the eligibility limitation rather than claim a completed source chain.

Cancellation uses native DELETE and a fresh controller-runtime client against a
second Milo replica. This proves persisted API recovery, not an Apps manager
process restart or real IAM credential deployment. The local authorization
webhook proves the native authorization chain can still deny registration; it
is not a real Milo/FGA policy fixture. Test-infra must prove actual connected Apps
manager, Project service identities, hook removal, cleanup and IAM before any
customer-facing activation.

Run the existing `task test:apps-ownership-api` with local envtest assets. The
focused development wrapper and exact results are recorded in the handoff; no
live cluster, deployment or production availability is asserted.

## Local verification (2026-10-21)

With `GOCACHE=/tmp/apps-milo-grant-gocache` and
`KUBEBUILDER_ASSETS=/Users/scotwells/Library/Application Support/io.kubebuilder.envtest/k8s/1.33.0-darwin-arm64`,
`task test:apps-ownership-api` passed: combined native HTTP race suite 176.430s,
ownership units 1.749s, pure pending wire units 1.173s, provenance filters 1.611s.
The Task includes this fixture, and CI path filters include embedded contracts
and the pure wire conformance vectors. `go vet` for the ownership packages and
API-server package passed through the focused Task wrapper; `git diff --check`
passed. Independent review reran the focused native API race fixture (32.223s)
and the original Apps canonical-spec oracle (1.488s).

A discriminating negative control temporarily removed the App custody check:
a live finalizer-strip PATCH succeeded after the observed dynamic hook was
removed, causing the required compiled-denial assertion to fail (21.30s).
The guard was restored before the final marker/precondition hardening and full
regression run. Earlier fixture compile issues and a no-op schema-weakening test
were corrected; the latter now changes actual schema CEL. An initial sandbox
listener denial was resolved with scoped permission for local API servers only.

These results establish local native HTTP behavior. They do not establish a
connected Apps manager restart, deployed Milo IAM identities, source eligibility,
Project drain, or customer installation availability. No deployment was performed.
