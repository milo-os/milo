# Root Project reservation prerequisite

The compiled guard reserves `apps.miloapis.com/project-reservation` in labels
and annotations on every kind. Mere presence, including empty or malformed
values, blocks creation, mutation and deletion. Old-object checks prevent
stripping. This check runs before every native status/finalizer exception and
also covers already-terminating reserved Projects whose last finalizer would
otherwise complete deletion. No principal can acquire or release this hold yet.

A held root Project must stay live because Milo starts purge after its deletion
timestamp is set. Another finalizer would preserve the root object but would
not stop that purge. This bootstrap therefore freezes all root writes,
including status; future narrowly authorized reservation/status/cleanup behavior
must be separately reviewed before any live Project is enrolled.

The two-replica HTTP test uses Milo's actual root Project CRD and real etcd.
Both replicas authenticate distinct fixture tokens under test-only AlwaysAllow
authorization. Real HTTP denies forged create, marker addition/removal,
ordinary update, apply, status, finalizer removal, direct DELETE and collection
DELETE while preserving the full stored object. Unmarked Projects still create,
update status, remove finalizers and delete. Reserved fixtures are seeded by
raw storage compare-and-swap solely for tests, not by a production bypass.

A transparent relay pauses an actual DELETE transaction after admission.
Another replica's storage fixture wins reservation CAS; the original DELETE
retries, sees the reserved old object and returns HTTP 403 without setting a
deletion timestamp. In the reverse order, stale acquisition cannot overwrite
the terminating Project or its same-name replacement. Old UID/version DELETE
also conflicts against the replacement.

Lead combined HTTP race validation with status, finalizer and commit-fence tests
passed in 65.806 seconds; admission race tests passed in 1.629 seconds. A
negative control removed only the three-line reservation hookup: the actual
DELETE race then returned HTTP 200 and set the deletion timestamp instead of
403 (10.259 seconds), proving the assertion detects the missing guard. The
exact source was restored afterward. An independent native-exception negative
control also failed all four qualifying status/finalizer label/annotation cases.

This hold does not stop child writes, coordinate storage-domain closure,
authorize Apps deletion/release, or establish production IAM and credential
custody. The native same-etcd Project commit fence and Apps authoring barrier
are separate pieces of the eventual drain protocol. No live reservation or
writer configuration is deployed by this prerequisite. Normal repository CI
includes the real HTTP test; focused race runs require the existing envtest
etcd asset and local listeners.
