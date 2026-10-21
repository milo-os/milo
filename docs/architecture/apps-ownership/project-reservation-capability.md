# Server-local Project reservation capability

This is a storage prerequisite, not an enabled coordinator or API permission.
The public guard still denies creating, changing, preserving, or removing
`apps.miloapis.com/project-reservation` on any admitted object. No production
caller, HTTP endpoint, username exception, release operation, or Apps enrollment
is introduced. An in-process holder of the capability is trusted code; it is
not proof of customer IAM permission or a cross-database drain acknowledgement.

## Binding and acquisition

The capability belongs to one API server's existing root Project storage
registry. Only registration of the actual root Project store supplies its
storage interface and key function. Project-scoped stores cannot supply or
replace it. Callers supply an exact Project name, UID, canonical positive
resourceVersion, and nonzero canonical UUID reservation identity. They cannot
supply an etcd key, prefix, resource kind, backend, object body, or transformer.

Acquire uses Kubernetes GuaranteedUpdate with both UID and resourceVersion
preconditions, no cached object, and no create-on-missing behavior. The callback
accepts only an existing root v1alpha1 Project with no deletion timestamp or
grace period and no reservation in either metadata spelling. It rejects Apps
ownership markers. The only object change is the canonical reservation
annotation; the storage layer assigns the new resourceVersion. Every other
field is preserved. Existing holds conflict even if their identity matches.

A lost reply is recovered by ReadHeld with the original Project UID and
reservation UUID. This performs a linearizable read and no write. It rejects
unknown holds, terminating Projects, label aliases, malformed metadata and
same-name replacements. The future coordinator must durably retain its chosen
reservation identity before acquisition; discovering an arbitrary hold does
not authorize adopting it. Neither timeouts nor uncertain outcomes release a
hold. Each operation has a two-second context bound and forwards cancellation
without forwarding request identities or private native mutation proofs.

## Strict decoding and lifecycle

Kubernetes CRD storage decoding repairs malformed ObjectMeta fields. For
example, a non-string annotation can cause a malformed annotation map to be
dropped before an ordinary update callback sees it. Therefore the capability
uses a private second etcd3 storage view built from the same actual root
configuration, client, codec, encryption transformer and registered key function.
Its decoder validates decrypted JSON before the normal CRD codec runs. No
raw-etcd read/write or alternate key calculation is used by the capability.

This view accepts JSON records up to 2 MiB and depth 64. It rejects duplicate
keys, trailing content, unknown/case-aliased ObjectMeta fields, malformed
metadata, invalid UTF-8 bytes, and null/non-string label or annotation entries.
The decoded object must equal the persisted JSON semantically except for the
storage-assigned resourceVersion; codec pruning/defaulting/repair therefore
cannot silently change unrelated spec or metadata. This deliberately rejects
records requiring schema migration. JSON escape-level unpaired surrogate
validation is not added; the strict checks focus on unambiguous keys and maps.
Errors from strict
or delegate decoding do not return record bytes. Unsupported/corrupt records
fail closed; public API decoding remains unchanged.

No second transport is opened. Both views close with the owning registration;
the shared client is released once. Operations hold registration stable until
their bounded storage call completes. Destruction of any attached root store
permanently retires this capability until server restart, even if an equivalent
registration remains. This conservative rule avoids switching to a retired
store. Ordinary replacement CRD stores still initialize and serve after
retirement. Conflicting root registrations invalidate existing handles. Child
store destruction does not retire the root capability.

## Tests and boundaries

The HTTP test starts two real Milo replicas and ephemeral etcd. Only the test
binary accepts internal calls over inherited anonymous pipes; there is no
production test switch or network endpoint for acquisition. Authentication uses
the existing AlwaysAllow fixture and is not production credential evidence.

A relay pauses an actual transaction. When acquisition wins, a previously
validated DELETE retries and the public reservation guard rejects it. In the
reverse order, acquisition loses its UID/RV CAS and cannot reserve the already
terminating Project. Fresh terminating RVs and same-name replacement UIDs also
fail. Public marker forgery, strip, status, finalizer and deletecollection
requests remain denied. Lost-reply recovery preserves the exact object and RV.
Raw-storage seeding is used only for corrupt-record fixtures, never acquisition.
Those cases assert unchanged stored bytes and modification revision.

The registered-store test uses the real AES-GCM transformer, verifies ciphertext
contains neither the reservation key nor UUID, and reads the held object back
through the capability. It also exercises child isolation, equivalent root
replacement, retirement without breaking ordinary CRUD, and conflicting root
registration. Unit tests cover malformed shapes, identity/precondition errors,
strict decoding bounds, and registry lifetime.

A root hold changes the root revision, so previously admitted native ownership
writes lose their existing commit fence. Fresh native status/finalizer writes
may still proceed while the root remains live: this is a deletion hold, not a
native write-drain barrier. Root Project status/finalizer updates are themselves
blocked by the public reservation guard. Holding during initialization can stop
Project finalizer setup; holding for the authoring lifetime freezes subsequent
Project status changes. A separately reviewed preserving-update contract is
required before activation.

Apps policy-v2 enrollment, authenticated participant coordination, native
closure/cleanup, safe release, and Project control-plane reclamation remain
unimplemented. The capability grants no native ownership or deletion privilege.

## Executed review evidence

Final affected unit race tests passed (storage 1.549s, admission 2.072s), the
AES-GCM registered-store/lifecycle test passed (1.864s), and the two-replica
capability HTTP suite passed (12.099s). The existing default-deny, native status,
finalizer, root commit-fence and reservation HTTP suites passed together in
82.443s before the final capability-only strict-decoder refinements. Affected
package vet and `git diff --check` passed.

A discriminating negative control replaced only the private strict decoder
with the ordinary decoder. The real malformed-storage HTTP assertion failed
because acquisition incorrectly succeeded (10.942s). The exact source was
restored before the final passing focused tests. This demonstrates the reason
for the separate strict view; public decoding was not changed.

Focused commands are defined in the temporary review wrapper
`/tmp/apps-reservation-capability-tasks.yml` (`unit`, `etcd`, `http`, `regression`,
`vet`). Tests use envtest etcd assets from
`/Users/scotwells/Library/Application Support/io.kubebuilder.envtest/k8s/1.33.0-darwin-arm64`.
The repository's normal package tests also discover the added tests; short mode
explicitly skips process-based checks. No live cluster or production operation
was performed.


Independent lead validation of the final source passed existing ownership,
native status/finalizer, Project fence and reservation HTTP races in 84.961
seconds; storage/admission race suites in 9.502/1.987 seconds; the encrypted
registered-store test in 1.899 seconds; and the capability's two-replica HTTP
races in 12.572 seconds. Affected vet checks passed. The strict-decoder
negative control returned erroneous acquisition over malformed records and
failed the expected assertion in 10.942 seconds; exact source was restored
before final validation. These are isolated real-API/storage tests, not
production IAM, deployed capability, or Apps drain acceptance evidence.
