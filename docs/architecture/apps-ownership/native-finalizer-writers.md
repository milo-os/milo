# Optional native finalizer writers

This slice is stacked on native status-writer PR #861, source
`ded614dd`. Default ownership enforcement remains deny-all. No deployed
configuration enables a finalizer or status writer. This is a prerequisite,
not authorization to install Apps or pass G1/G2.

## Source-backed native contract

Pinned source reviewed before implementing the exception:

- Compute `88a7101c2b91f7195c2bb5433c50f54c384a90ce`,
  `internal/controller/workload_controller.go:45` defines
  `compute.datumapis.com/workload-controller`. Registration is at 748–750,
  ordinary Update at 97–105, and cleanup at 523–562 waits for UID-indexed
  WorkloadDeployments to be gone. Its controller-runtime finalizer helper adds
  before termination and removes after cleanup. The separate legacy spec
  migration at 121–132 is **not** a finalizer-only operation and remains denied.
- NSO PR #675 head `79245f00d6220c128df3d024475ff7b1566ce305`,
  `internal/controller/gateway_controller.go:69` defines
  `gateway.networking.datumapis.com/gateway-controller-gc`.
  Lines 2201–2209 skip terminating HTTPRoutes and add this finalizer with
  ordinary Update. `internal/controller/gateway_downstream_gc_controller.go:82–84`
  processes only terminating objects with this finalizer; lines 145–146 remove
  it with ordinary Update after downstream cleanup. The distinct Gateway
  finalizer is not granted to HTTPRoutes by these tested rules.

The tested combinations are Workload `compute.datumapis.com/v1alpha` and
HTTPRoute `gateway.networking.k8s.io/v1` plus its `v1beta1` served alias.
Each version needs its own exact configuration entry. This does not add a
wildcard or assume another native kind has the same lifecycle contract.

## Optional operator configuration

The existing `AppsOwnershipConfiguration` accepts `finalizerWriters` in
addition to `statusWriters`. Each finalizer entry requires the same literal
`username`, authenticated `uid`, `group`, `version`, plural `resource`,
and `kind` fields, plus one fully qualified `finalizer` string. There is no
wildcard matching or automatic configuration from service identity names.

The combined status/finalizer array limit is 32 entries and the total
configuration bound remains 64 KiB. Missing principal/GVR/kind, invalid or
unqualified finalizer, duplicate identical rule, unknown field, or excess
entries fails startup. A configured plugin requires the root dynamic client.
Rules are process-start configuration and require restart to change.

The bounded exception requires all of the following:

- Native unstructured Update on the main resource, not any subresource.
- Server-private authentication provenance, exact authenticated and effective
  name/UID matching, and no `Impersonate-*` header presence. Groups or supplied
  extras confer no privilege. This records authentication at this Milo hop;
  upstream authenticator assertions remain a separate credential boundary.
- Exact configured GVR and GVK on the request and both objects; complete
  nonempty valid Project/App UID labels; same resource UID, name and namespace;
  no reserved annotations or external owner references.
- The complete object is unchanged except `metadata.resourceVersion`,
  `metadata.managedFields`, and the single configured finalizer toggle.
  Spec, status, ownership labels, annotations, deletion timestamp, grace period,
  and every other metadata field are preserved. The content/order of every
  other finalizer stays unchanged. Duplicate entries fail closed. A no-op
  does not qualify and multiple configured finalizer changes cannot be bundled.
- Adding the configured finalizer is permitted only before the old object's
  deletionTimestamp exists. Removing it is permitted only after the old object
  is already terminating. Removal before termination, re-addition while
  terminating, and attempts to initiate termination are not exceptions.
- The server route identifies a Project whose fresh root GET succeeds within
  one second, has the exact ownership Project UID and name, and is not deleting.
  There is no per-App GET. Missing/unavailable/recreated/deleting Projects fail.

Finalizer rules do not grant status access; status rules do not grant finalizer
access. Neither grants Kubernetes IAM/RBAC authorization, acquisition of markers,
spec edits, direct DELETE, ownership release, or an Apps controller identity.

## Last-finalizer removal deliberately completes existing termination

Kubernetes 1.35's generic store checks `ShouldDeleteDuringUpdate` after Update
validation (`pkg/registry/generic/registry/store.go:773–798`). If the old
object is terminating, the new finalizer list is empty, and grace is nil/zero,
it calls `deleteWithoutFinalizers`. That method uses
`rest.ValidateAllObjectFunc` rather than separate DELETE admission
(lines 560–615). Last-finalizer removal is therefore a physical-deletion
capability on an object whose termination was already initiated.

The reviewed decision is to permit that native completion, including the last
configured finalizer. No independent Apps finalizer is invented. The writer
cannot initiate deletion through DELETE or change deletionTimestamp via Update.
An already-started termination fixture is necessary to exercise completion;
this slice does not establish that an Apps-authorized deletion flow exists.

Enabling these rules still requires independent proof of credential custody,
trusted deletion initiation and all remaining G1/G2 lifecycle gates. The
one-second Project GET is not atomic with project teardown/IAM revocation.
A root Project already marked deleting denies even finalizer completion, so
project drain/teardown coordination remains unresolved and is not enabled here.
The exception is native-controller-scoped over configured resource kinds,
not an App-specific inventory capability. Native callers must preserve their
ordinary UID/RV preconditions; it does not add a per-App intent lookup.

## Executable real-API evidence

The HTTP test starts an isolated real Milo API and etcd, loads actual Workload
and Gateway API HTTPRoute CRD fixtures, and uses project API URLs. AlwaysAllow
authorization and temporary exact token identities are test-only: they ensure
generic impersonation reaches the guard, and prove no production IAM/custody.

Test storage directly seeds ownership markers and deletionTimestamp, clearly
outside the served API. This is not an authorized Apps acquisition/deletion
implementation. Native controller processes and downstream cleanup are not run.

For each of Workload v1alpha and HTTPRoute v1/v1beta1, PUT, merge PATCH and SSA
add only the configured finalizer, preserve the unrelated finalizer, remove the
configured one only once termination is seeded, and complete last-finalizer
deletion. GET confirms 404 for the old UID; recreating the name yields a fresh
UID, and stale cleanup cannot touch the replacement. Tests also exercise:

- configured writer cannot initiate DELETE or termination through Update;
- other principals, self/empty/unknown-header impersonation deny;
- mixed spec/labels/external owner references/other finalizer edits deny;
- finalizer-only rules cannot update status or create ownership markers;
- re-addition during termination, foreign Project markers and Project name
  reuse deny, with before/after object equality on rejected writes.

The main-resource native CRD strategy can discard supplied status mutations
before validating admission. Comparisons protect the resulting persisted
object; they do not claim to reject already-discarded request fields.

Commands, using existing envtest etcd 1.33.0 assets:

```sh
go test -race ./internal/apiserver/admission/plugin/appsownership ./pkg/server/filters -count=1
KUBEBUILDER_ASSETS=/path/to/envtest/assets go test -race ./cmd/milo/apiserver   -run '^(TestAppsFinalizerWriterHTTPIntegration|TestAppsStatusWriterHTTPIntegration|TestAppsOwnershipHTTPIntegration|TestDefaultChainPreservesAuthenticationProvenance)$'   -count=1 -timeout=240s
go vet ./internal/apiserver/admission/plugin/appsownership ./pkg/server/filters ./cmd/milo/apiserver
```

Local assets: `/Users/scotwells/Library/Application Support/io.kubebuilder.envtest/k8s/1.33.0-darwin-arm64`.
The finalizer-only HTTP race run after strict finalizer-array validation passed
in 22.829s. The combined default-deny/status/finalizer/provenance HTTP regressions
passed in 62.191s. Restored final unit/provenance race tests passed in
1.679s/2.054s; affected-package `go vet` and `git diff --check` passed.

A discriminating negative control temporarily removed the already-terminating
requirement from finalizer removal.
`TestConfiguredFinalizerBoundary/remove_before_termination` then failed because
the guard incorrectly permitted removal. The original source was restored and
the affected race tests and vet passed again. No weakened source is committed.

Removing the optional rules restores default-deny behavior and may stall native
cleanup/status reporting. It must not remove the compiled guard or clear ownership.
No deployment, identity provisioning, production rollout, or native install occurs.
