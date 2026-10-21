# Exact native grant CRD registration, with grant writes disabled

This optional compiled admission contract permits root native CREATE of one exact
AppInstallGrant CRD and narrowly bounded status updates needed by Kubernetes CRD
controllers. It adds no authorization privilege, principal exemption, grant
issuance/revocation path, acceptance permit, native resource writer or deployment
activation. The grant-family and whole Apps-group APIService reservations remain
in force. Without configuration, #875's deny-all behavior is unchanged.

The only supported `AppsOwnershipConfiguration.grantRegistration` value is:

```
disabled-consumption-v1@sha256:33c9be9ec2f9ac76c1945776b53568c18159bb50743f48144cd28bbc6e039d6d
```

Omission disables the allowance; unknown values fail startup. This value selects
a compiled contract, not an operator-supplied hash/schema. There is no deployment
manifest enabling it. The source CRD remains Apps #46
`e1be5c515cbffb798a67bc2da8278e0d522dddc4`; the embedded immutable copy is checked
against that exact SHA256 at startup. Schema migration requires another reviewed
versioned contract.

## Root and object predicates

Normal native authorization must already allow the request. The compiled guard
additionally requires private authentication provenance, nonempty authenticated
UID/name, no impersonation header, and matching effective/captured identity,
groups and extras. It rejects any Project, Organization or User context, including
empty context values, and any parent-scope extras. It does not trust a submitted
Project name or caller-provided provenance. Credentials without a stable UID do
not receive a name-only fallback.

CREATE is restricted to the exact native apiextensions v1 CRD, empty namespace,
and trusted RequestInfo verb `create` with an empty collection name. SSA PATCH
that invokes Create admission is denied. The entire typed spec must equal the
compiled source after native Kubernetes defaulting; there is no broad nil/empty
normalization. No conversion webhook, extra version, schema change or new
subresource is allowed. Metadata has only its exact name and
`discovery.miloapis.com/parent-contexts: Project` annotation, plus native
server-managed fields. Labels, additional annotations, owner references,
finalizers, deletion state and generateName are forbidden. The final decoded
object must have the native initial generation/status. The guard does not claim
to inspect raw fields pruned before admission.

Only `/status` UPDATE/PATCH may subsequently change the CRD. Old and new must be
live, exact-contract objects with identical UID and resourceVersion. Every field
outside status/resourceVersion/managedFields is preserved. storedVersions stays
exactly v1alpha1. Pending acceptedNames may contain only empty or canonical fields;
once complete it cannot regress. Conditions are bounded and unique, limited to
NamesAccepted, Established, NonStructuralSchema and
KubernetesAPIApprovalPolicyConformant. Established=True requires canonical names
and NamesAccepted=True. No Terminating condition is permitted. Unknown/corrupt old
state cannot be repaired through this path. Native RV conflicts remain ordinary
optimistic concurrency; there is no cross-object transaction or teardown fence.

Root principals with native CRD-status permission may change these bounded
conditions; this is deliberately not a claim of exclusive controller authorship.
Such updates can affect diagnostics/availability but cannot change the schema,
routing, grant custody or install authority. Main-resource UPDATE, PATCH, SSA and
DELETE remain denied, including no-op apply. All remaining ownership and admission
checks still run after the registration allowance.

## Native lifecycle and bootstrap limits

Kubernetes v0.35.0's apiextensions server creates its naming, establishment,
structural-schema and approval controllers from the native loopback client
(`pkg/apiserver/apiserver.go:164–185,215–222`). Naming writes acceptedNames and
conditions (`pkg/controller/status/naming_controller.go:261–274`); establishment
then sets Established (`pkg/controller/establish/establishing_controller.go:127–166`).
The native create strategy seeds storedVersions, and status has optimistic update
semantics (`pkg/registry/customresourcedefinition/strategy.go:73–85,267–285`).
The loopback identity has a per-completion random UID (apiserver
`pkg/server/config.go:1173–1197`), so no fixed controller username exception is used.

Milo's existing generic CRD bootstrap (`config/crd/bootstrap.go:142–178`) updates
an existing CRD unconditionally. This protected CRD must not be added to that
embedded updater: restart would try a denied update. Its Apps experimental source
manifest can be registered by an authorized root native POST. Lost responses and
AlreadyExists recover through read-only exact contract/identity verification,
never overwrite/repair. Apply-style orchestration is not supported by this slice.

Before activation, all serving replicas must run compatible mandatory custody.
Read-only native inventory must cover every Apps-group APIService, the grant CRD
and all existing grants in relevant Project UIDs and root scope. Existing remote
aggregation, unknown grants, incompatible/corrupt schemas, or a terminating CRD
require separate reviewed migration. No old grant becomes trusted merely because
its CRD matches. A terminating CRD is particularly unsafe to adopt: Kubernetes'
CRD finalizer deletes instances through its internal REST storage deleter
(`pkg/controller/finalizer/crd_finalizer.go:181–222`), outside per-instance admission.
This is API-server-owned behavior, not an Apps persistence interface.

The aggregator also attempts a service-less local APIService registration for a
native CRD group/version. The unchanged Apps-group reservation rejects those
writes, producing controller retry diagnostics. This slice does not add a local
APIService exception. Native root/Project discovery, dynamic RESTMapper resolution
and readiness are tested explicitly below; direct known-GVR success alone is not
claimed as discovery evidence. Retry noise remains a deployment consideration.

## Evidence and remaining authority gates

Milo base #875 is `2e3263477cc9a920340100449e701f1c29855941`. Unit tests cover default
configuration, native internal conversion/defaults, scope/provenance, schema and
metadata changes, partial status, corrupt old state and identity/CAS mismatches.
The native HTTP test seals all setup before the first grant CRD request; normal
CRD controllers must establish it through production predicates. A real TLS
native authorization webhook denies one subject's CRD mutations while permitting
its discovery; this proves that authorization is retained, not platform IAM.
All fixture setup/assertions use controller-runtime/native HTTP APIs. Only Milo
accesses its ephemeral API-server-owned backend. No fixture KV/storage adapter or
production bypass exists.

The native suite checks Project routing, missing UID and self-impersonation,
schema weakening and conversion/extra-version/metadata attacks, SSA-create denial,
dry-run absence, concurrent CREATE, read-only identity verification, safe status,
main-resource mutation/DELETE denial, and continued grant issuance denial. Each
compiled-denial assertion requires the exact reservation error. Shapes rejected
by native schema first are labeled separately, never counted as guard evidence.

No lost-response injection or process-restart trial is claimed in this slice.
The native post-CREATE GET verifies identity/spec without rewriting the CRD; it is
a recovery building block, not a fault-injection result.

Fresh authenticated issuance, mint permission versus component permission,
request/source/history custody and a consumable protocol remain unimplemented.
Old pending-only-v1 never accepts. Future revokeRequested support must account
for Project suspension: the current plugin blocks live-object updates except
access reviews or system:masters (`projectsuspension/admission.go:213–238`). A
narrow revoke-only accommodation needs separate review; broad credentials are not
a substitute. Project drain and writes in flight remain unsolved. This optional
registration contract enables no customer installations.

Run the native suite with envtest 1.33.0 assets:

```sh
KUBEBUILDER_ASSETS=/path/to/assets task test:apps-ownership-api
```

The tests are untagged and included by existing CI. No ordinary overlay, image or
live registration changed.

Local results (2026-10-21, envtest `1.33.0-darwin-arm64`): focused registration
HTTP race PASS 23.891s; restored full Task HTTP suite PASS 144.101s, ownership race
1.662s, provenance/filter race 1.600s. Expanded default/enabled grant-family matrix
race PASS 1.718s. Affected `go vet` and `git diff --check` passed. Both root and
Project `/apis` and native resource discovery advertised the grant, fresh dynamic
RESTMappers resolved its exact GVR, root `/readyz` returned 200, and the Apps
APIService remained absent. This proves the tested native paths despite the
local APIService auto-registration retries; it does not promise quiet controllers
or prove live rollout compatibility.

The discriminating negative control removed only full typed-spec equality. A
native CREATE with a weakened envelope CEL rule then succeeded, failing the exact
Forbidden assertion (10.966s). The comparison was restored byte-for-byte before
the full suite. An initial fixture-only authorizer failure denied discovery before
the intended mutation; the authorizer now denies only the selected subject's CRD
CREATE/UPDATE/PATCH, so denial evidence reaches the actual resource authorization
path. No production predicate was relaxed to fix this test.
