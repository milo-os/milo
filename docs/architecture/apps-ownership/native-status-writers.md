# Optional native status writers

The default compiled guard still denies every write involving either reserved
ownership marker. This slice adds an explicit operator configuration for native
controllers that need to update **status only** on an already marked resource.
It combines the default-deny/native HTTP probe in PR #860 with authentication
provenance from PR #853. No deployed configuration enables a writer.

Supply the normal Kubernetes `AdmissionConfiguration` through
`--admission-control-config-file`. Only that configuration flag is exposed;
plugin-disable switches are not added and the mandatory guard check remains.
The Apps plugin's embedded configuration has version
`apiserver.miloapis.com/v1alpha1`, kind `AppsOwnershipConfiguration`, and a
`statusWriters` array. Each entry requires `username`, authenticated `uid`,
exact non-core `group`, `version`, plural `resource`, and `kind`. Wildcards,
subresource suffixes, missing identities, duplicates, unknown fields, more than
32 rules and files over 64 KiB fail startup. An absent configuration means zero
exceptions. A configured plugin without its root dynamic client fails startup.
Rules are process-start configuration; changing them requires a Milo restart.

An exception requires all of the following:

- Operation Update on precisely `/status`, matching the configured GVR/GVK.
- Server-private authentication provenance, captured before impersonation;
  authenticated and effective names/UIDs match the exact configured principal.
  Any `Impersonate-*` header presence rejects the exception, including empty,
  unsupported or self-impersonation. Groups and extras do not confer trust.
- Both old and new native unstructured objects, complete nonempty canonical
  project/App UID labels, no reserved annotations, unchanged resource UID and
  complete metadata/spec outside status. External owner references are denied.
  Only storage-owned resourceVersion and SSA-managed managedFields are excluded
  from comparison. Root/core/scale/finalize and ordinary spec updates do not
  qualify. No create, delete, marker acquisition or marker release exception.
- Server route context identifies the Project. A fresh root-scoped Project GET
  succeeds within a one-second context deadline, matches the ownership Project
  UID and name, and has no deletion timestamp. Missing, unavailable, deleting
  or same-name replacement Projects fail closed. There is no per-App lookup.

This exception is not Kubernetes authorization. Native IAM/RBAC must separately
allow the principal to update that status endpoint. The credential must be
issued and held outside consumer control. An upstream trusted authenticator
that asserts a principal is part of that credential trust boundary; this slice
captures provenance at this Milo hop only. It does not prove upstream actor
custody, durable install authority, atomic project teardown, release authority,
namespace protection or finalizer compatibility. The live Project read is not
atomic with teardown. No customer installs may be enabled based on this slice.

## Executable evidence

The real-HTTP test starts an isolated etcd and actual Milo command with the
configuration flag, real token authentication, public Project routes, exact
native CRD schemas from PR #860, and the complete middleware/admission chain.
Authorization is deliberately AlwaysAllow so impersonation reaches admission;
this is **not** production IAM or controller deployment evidence. Marker seeds
use compare-and-swap in only that disposable etcd after creating resources
through Milo. The test's exact principal uses the existing temporary
`apps-controller` fixture name; it represents a configured native status writer,
not installation authority or an enabled Apps resource controller.

Workload v1alpha and HTTPRoute v1/v1beta1 accept configured merge-patch, PUT and
server-side apply status changes. Other principals, impersonation (including
self/empty/unknown headers), marker-bearing creates, main-resource marker
stripping/finalizer mutations and deletion receive ownership denials. Reads
confirm denied operations changed neither resourceVersion nor marker metadata.
A same-name root Project UID change makes the previously valid status writer
fail. Unit cases cover missing/deleting/unavailable Project, changed identities,
wrong native version/kind/resource, incomplete markers, changed metadata/spec,
external owner references, missing provenance/client and default configuration.

Kubernetes CRD status strategy restores non-status fields **before** admission.
A status PUT attempting to replace spec/labels/finalizers therefore succeeds
only with those changes discarded. The real test verifies the stored spec,
UID, labels and absence of the attempted finalizer; it does not falsely expect
a denial for fields already removed by native strategy.

Commands (local Go 1.26.1, envtest etcd assets 1.35.0; CI installs 1.33.0 assets):

```sh
go test -race ./internal/apiserver/admission/plugin/appsownership ./pkg/server/filters -count=1
KUBEBUILDER_ASSETS=/path/to/envtest/assets go test -race ./cmd/milo/apiserver \
  -run '^(TestAppsStatusWriterHTTPIntegration|TestAppsOwnershipHTTPIntegration|TestDefaultChainPreservesAuthenticationProvenance)$' \
  -count=1 -timeout=180s
go vet ./internal/apiserver/admission/plugin/appsownership ./pkg/server/filters ./cmd/milo/apiserver
```

Lead results: unit/race 1.611s/1.997s; combined default-guard, status and provenance
real-HTTP race 40.615s. After adding status PUT/SSA and malicious non-status-field
preservation, the affected real-HTTP test passed in 21.697s. Vet and diff checks
passed. A temporary negative control removed the impersonation-presence check;
all three self/empty/unknown-header unit cases failed as expected (exit 1), then
the source was restored. Independent read-only security review found no
concrete bypass; it did not replace the real-HTTP run.

Initial development runs exposed a missing CLI configuration flag and the fact
that Milo already installs its own Project CRD. Those were corrected. HTTPRoute
native defaulting adds Gateway group/kind to status parentRef; the test now sends
that complete expected shape. No assertion was skipped or relaxed to hide it.

Removing the optional rules restores deny-all ownership behavior and may pause
native status reporting; it must not remove the compiled guard or clear markers.
Roll out rules only after reviewing identity custody, exact native kind/version
coverage, real IAM and the remaining ownership/lifecycle gates. No persisted
schema migration or production deployment is part of this PR.
