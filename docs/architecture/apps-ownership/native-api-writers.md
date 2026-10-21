# Optional native writers with API-only fixtures

This change is based on API-only fixture PR #868 (`6af93190`). It ports the
independent authentication-provenance change from #853 (`825094c8`), and only
the production status/finalizer contracts and unit tests from #861 (`ded614dd`)
and #862 (`873478b5`, published head `22de2a5d`). It does not depend on the
backend fixtures from #852/#860 or the storage-fence/reservation stack. No
namespace reservation or Project teardown behavior is introduced.

Default ownership enforcement still denies all marked writes. No deployment
configuration enables these optional writers, and no acquisition, release,
App installation, or trusted deletion-initiation capability is introduced.

## Native contract and configuration

Source reviewed for the exact finalizers:

- Compute `88a7101c2b91f7195c2bb5433c50f54c384a90ce`,
  `internal/controller/workload_controller.go:45`: finalizer
  `compute.datumapis.com/workload-controller`. Registration is at 748–750;
  ordinary Update at 97–105; cleanup at 523–562. Its legacy spec migration at
  121–132 is not a finalizer-only operation and remains denied.
- NSO #675 head `79245f00d6220c128df3d024475ff7b1566ce305`,
  `internal/controller/gateway_controller.go:69`: HTTPRoute finalizer
  `gateway.networking.datumapis.com/gateway-controller-gc`, added by ordinary
  Update at 2201–2209. `gateway_downstream_gc_controller.go:82–84,145–146`
  handles termination and removes it after downstream cleanup.

The normal `--admission-control-config-file` accepts Kubernetes
`AdmissionConfiguration`. The embedded Apps plugin configuration has
`apiVersion: apiserver.miloapis.com/v1alpha1` and
`kind: AppsOwnershipConfiguration`, with optional `statusWriters` and
`finalizerWriters` arrays. Each rule pins literal `username`, authenticated
`uid`, non-core `group`, `version`, plural `resource`, and `kind`.
Finalizer rules additionally pin a qualified `finalizer`. Matching is exact;
there are no wildcard grants. The combined rule limit is 32 and the input limit
is 64 KiB. Invalid or unknown fields, duplicate rules, and a missing root client
fail startup. Changing configuration requires restart. No plugin-disable switch
is introduced.

Both exceptions require private pre-impersonation request provenance and exact
configured authenticated/effective name and UID. Any `Impersonate-*` header,
including empty, unknown or self-impersonation, disqualifies the exception.
Groups and extras confer no privilege. This observes authentication at this Milo
hop only; upstream identity assertion and credential custody remain separate.

Both old and new native unstructured objects must have the exact GVK and request
GVR, complete nonempty Project/App UID labels, unchanged object UID/name/namespace,
no reserved annotations, and no external owner references. The routed Project
must pass a fresh root API GET within one second, match the ownership Project
UID and name, and not be terminating. There is no per-App GET.

Status rules permit only `/status` Update with all other fields preserved.
Finalizer rules permit only main-resource Update, preserving the entire object
except one configured finalizer addition/removal. Both comparisons exclude
resourceVersion and managedFields, which are managed by Kubernetes. Other
finalizers retain their order and content. Duplicate finalizers and no-op toggles
do not qualify. Addition is permitted only before termination; removal only
while the old object is already terminating. No rule grants spec edits, marker
acquisition/release, direct DELETE, or the other type of exception.

Removing the last finalizer **physically deletes an already-terminating object**
through Kubernetes' native update strategy. It cannot initiate termination.
The live root GET is not atomic with a later protected write or Project deletion.
A terminating root Project currently denies native finalizer completion, so
teardown coordination remains unresolved. These limitations are retained from
the original contract; passing these tests is not approval to activate it.

## API-only fixture boundary

The tests reuse #868's supported test-binary admission decorator. Its temporary
setup identity is restricted to a finite list of native resource names in one
namespace. All setup API requests finish before an out-of-band one-way latch
permanently closes. Assertions begin only after acknowledgement. No request
header, HTTP endpoint, production flag, or production identity can reopen it.
The setup identity is explicitly denied after sealing, including attempts to
create at an unused allowlisted name.

Every marked, malformed, foreign-owner and terminating fixture is created
through controller-runtime clients against Milo's project API URL. Ordinary API
DELETE with UID/resourceVersion preconditions begins fixture termination before
sealing; timestamps, identities and versions are assigned by Kubernetes. This
is test setup, not an implemented production Apps deletion workflow.

Only Milo accesses its normal ephemeral envtest backend process. Tests have no
KV client, storage factory, backend key, record scan, direct registry access,
or fabricated UID/resourceVersion/deletion timestamp. Project deletion and
same-name replacement also use ordinary root API operations.

The native schemas are complete upstream files, hash checked before API
installation:

| Fixture | Source | SHA-256 |
|---|---|---|
| Workload | Compute `88a7101c2b91f7195c2bb5433c50f54c384a90ce` | `ba38aeadb5d6c7a97c9b05a031d9955099820f5e7edd206a43fe3dd7f031b889` |
| HTTPRoute | Gateway API v1.5.1 standard bundle, pinned by reviewed NSO | `682203b109baecb18d38073a578f7bc2c17d22fe9b0c27bf030850abe014c370` |

The HTTPRoute fixture receives only the Milo Project discovery annotation during
API installation. The schema is unchanged. Native controller processes,
webhooks, external references and downstream cleanup are not run.

## Assertions and reproduction

For Workload v1alpha and HTTPRoute v1/v1beta1, the real HTTP suite checks:

- Configured status PUT, merge PATCH and SSA preserve all non-status fields.
  The native status strategy discards attempted spec/label/finalizer changes
  before admission; tests check the persisted result rather than claiming those
  discarded request fields must produce a denial.
- Finalizer PUT, merge PATCH and SSA add the exact finalizer before termination,
  remove it during termination while preserving another finalizer, and remove
  a last finalizer so GET returns NotFound for the old resource.
- Same-name recreation gets a new UID; stale finalizer cleanup cannot change it.
- Customers, setup credentials after sealing, system:masters, same usernames
  with wrong UIDs, cross-capability principals and impersonation are denied.
- Mixed metadata/spec/owner-reference changes, marker acquisition/removal,
  malformed ownership and foreign Project UID markers are denied with exact
  full-object GET equality, including unchanged resourceVersion.
- Direct DELETE, attempted initiation by UPDATE, premature finalizer removal,
  terminating re-addition and removal of another controller's finalizer fail.
- A successful finalizer dry-run leaves the stored object unchanged.
- A live Project transitioning through terminating, absent and same-name new
  UID states cannot authorize previously marked resources.

AlwaysAllow authorization deliberately lets real impersonation requests reach
the ownership guard. This isolates the guard and does not prove production IAM.
The Project controller is not running: ordinary root API deletion does not purge
project resources in this fixture. Their surviving old markers make the
same-name UID rejection observable; this is not a Project teardown test.

Run with Kubernetes 1.33.0 envtest assets installed:

```sh
KUBEBUILDER_ASSETS=/path/to/envtest/assets TASK_X_REMOTE_TASKFILES=1 \
  task test:apps-ownership-api
```

The existing CI job sets these assets and runs `go test ./...`, so these untagged
API tests are included. The dedicated task additionally enables the race detector
and runs the ownership/provenance unit suites. It does not use a kubeconfig or
live cluster.

## Local validation

Using `/Users/scotwells/Library/Application Support/io.kubebuilder.envtest/k8s/1.33.0-darwin-arm64`,
the repository task passed the combined real HTTP/default-guard/provenance suite
in 79.695s, ownership unit race tests in 1.637s, and filter race tests in 2.067s.
Affected-package vet and `git diff --check` passed.

A discriminating negative control temporarily removed the old-object termination
requirement from finalizer removal. The actual API test
`TestAppsNativeWritersAPIFixture/workload/finalizers-put` then failed because
premature removal unexpectedly succeeded. The production source was restored
byte-for-byte. That API case passed again in 39.711s, ownership/filter race tests
passed in 1.623s/2.020s, and vet passed again. No weakened source remains.

API fixture development also exposed two native SSA semantics that the previous
raw fixtures did not model: deletion by omission requires field ownership, and
omitting another manager's finalizer preserves that entry. Setup now adds the
native finalizer through SSA under the tested manager before API deletion, and
the negative test uses PUT to request actual removal of both controllers'
finalizers. Production comparison or termination checks were not relaxed.

Independent lead verification repeated the repository task: combined real HTTP
race tests passed in 78.717s, ownership unit race in 1.615s and provenance filter
race in 1.802s. Affected-package vet passed. Independent read-only review found
no actionable bypass, while retaining the terminating-Project cleanup and
production identity limitations above.
