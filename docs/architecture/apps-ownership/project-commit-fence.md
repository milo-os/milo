# Native ownership Project commit fence

This slice closes the interval between the native ownership guard's live root
Project read and the actual CRD storage commit. It adds no writer permission:
ownership acquisition, customer writes to owned objects, direct deletion and
release remain disabled. The optional status/finalizer rules remain off in
all deployment configurations.

## Storage contract

Successful ownership admission records the exact live Project resourceVersion
and immutable Project/App/resource identities in private request context.
Each admission attempt clears earlier evidence. Neither headers nor metadata
can provide this context. The authoritative decoded old and new objects at
storage bind the evidence to the actual target key and project storage scope.
Missing evidence, mismatched identities, malformed markers or unsupported root
storage fail closed. Ordinary unmarked CRD writes keep their existing behavior,
including server-side apply creating an object with a zero-value old object.

The actual root Project CRD storage registers its key function, prefix, etcd
transport and observed cluster ID. Project child stores cannot register it.
Conflicting registrations permanently invalidate the resolver for that server.
Both root and child must use the same configured transport and observed etcd
cluster. This assumes a stable correctly configured etcd backend; changing a
network endpoint between unrelated etcd clusters during a request is unsupported.

The final native transaction compares both the target object's expected
modification revision and the validated root Project modification revision.
The same comparison covers a physical deletion caused by removing the last
finalizer from an already-terminating object. A changed, missing or replaced
Project causes a terminal HTTP 409. A target-only conflict retains upstream
compare-and-swap retries and current-object retrieval. A harmless root status
update can conservatively cause a conflict; the client must retry with fresh
admission. A no-op storage update can return success without writing.

The wrapper sits below the cacher, using the existing codec and encryption
transformer; it does not inspect encoded/encrypted values. Its enrollment is
unstructured CRD storage, matching the currently permitted native writer
contract. Typed built-in resources, remote aggregated storage and additional
storage domains are not covered by this fence and cannot be enabled on this
proof. The root lookup adds an etcd read before each protected transaction;
latency and scale measurements remain future work.

## Executed evidence

The real HTTP test starts two independent Milo API replicas and real etcd. A
transparent test-only gRPC relay pauses replica A's actual target transaction
after admission; replica B completes root Project DELETE with a held test
finalizer. A then resumes. All six combinations of status/last-finalizer and
PUT/merge-patch/server-side-apply reject the stale commit with HTTP 409 and
preserve the full target object. Ownership and termination are seeded directly
only in the fixture, since acquisition and trusted deletion are not enabled.
The test uses Workload's pinned native CRD and AlwaysAllow test authorization;
it is not production IAM or credential-custody evidence.

The negative control removes only the two root revision comparisons while
leaving all other production code and assertions unchanged. All six cases
then fail: each stale mutation returns HTTP 200 instead of 409 (14.530 seconds).
The source is restored immediately afterward. The combined ownership, status,
finalizer, provenance and fence race suite passed in 74.315 seconds before this
negative control. An ordinary unmarked SSA creation regression was reproduced
and fixed; every fence subcase now creates its initial Workload through SSA.

Direct real-etcd tests also cover valid put/delete, changed or missing root,
same-name root delete/recreate, target-only contention and fresh retries,
wrong target binding, conflicting root registration and cluster-ID mismatch.
The final full repository test task passed, including this HTTP test in
12.13 seconds and the direct-etcd test in 8.43 seconds; affected packages also
passed vet. The extended direct-etcd race test passed in 9.102 seconds.
Private-context and decoded-object tests cover missing evidence, target/key
rebinding, malformed marker forms and the zero-value SSA old object.

Run the repository's full test task with envtest assets available:

```sh
task test:unit
```

Focused race checks can run the Project fence HTTP test and the new
`etcdshared` transaction test. Normal CI's full package test command includes
both real-process tests; neither silently skips missing etcd assets outside
short mode.

## Remaining Project teardown protocol

This is a same-etcd liveness fence, not complete Project drain or IAM revocation.
Milo purge starts when the root Project becomes terminating; adding another
finalizer alone does not defer that start. Native cleanup must finish while
that Project remains live. A future reservation must block root deletion until
all enrolled storage domains have durably stopped writes and completed cleanup.
Apps has separate etcd storage, so it needs its own irreversible closure record
and an authenticated acknowledgement tied to the exact Project UID and drain
generation. Missing/unavailable participants must hold teardown.

Milo's namespace-oriented purge skips cluster-scoped Apps authoring/history.
Successful listing, a timeout, a replica-local wait group or an expired request
lease cannot establish absence of later commits. The full lifecycle protocol,
namespace reservation integration, history retention, infrastructure reclamation
and downstream native effects remain separate work. No customer installation
or complete protection gate is claimed by this change.
