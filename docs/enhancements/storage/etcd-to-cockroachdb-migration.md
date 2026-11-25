---
status: proposed
---

# Moving Milo's storage from etcd to CockroachDB

> Tracks [milo#596](https://github.com/milo-os/milo/issues/596). Builds on [CockroachDB storage for Milo's API server](cockroachdb-storage.md), which replaces its short cutover section.

## Summary

This RFC describes how a running Milo moves its objects from etcd to CockroachDB without downtime, and how it goes back if it has to.

Reads and watches keep working for the whole move, and writes are rejected for a few minutes. A migration controller copies Milo's keys into CockroachDB with their resource versions, verifies the copy, starts CockroachDB-backed API servers beside the etcd-backed ones, moves traffic over and allows writes again. Each client lists again once as it reconnects to the new servers.

A `StorageMigration` resource describes each move, and the controller works through it phase by phase, stopping at gates an operator removes. Going back is another `StorageMigration` in the opposite direction. Rehearsals in an environment built like production come before staging.

## Problem

The storage RFC settles that the move is one coordinated cutover, and leaves the details open. Production adds the constraints that decide them:

- **Reads can't go down.** Every part of the platform reads through Milo, users included, so failed reads are an outage. Rejecting writes for a few minutes is acceptable.
- **No write pause exists today.** Almost all of Milo's requests come from clients in the `system:masters` group, which API Priority and Fairness exempts, so it can't hold writes back. Milo has no read-only mode.
- **Several controllers keep their leader-election leases in Milo,** some with short leases, so they restart during any write pause longer than their renew deadline.
- **API servers take tens of seconds to become ready,** so the new servers have to start while the old ones still serve.
- **Clients hold long-lived connections,** and edge cells connect through TLS passthrough, so switching the Service alone doesn't move existing connections.
- **etcd is shared.** Other API servers keep their data in the same etcd under their own prefixes, and keep using it after the move. etcd's revision keeps moving with their writes while Milo is paused. Milo's keys sit under `/datum-core` for shared resources and `/projects/<name>` for each project.
- **etcd has no backups today,** so until a snapshot is taken, the data exists in one place only.
- **Clients act on what they read.** Controllers delete what a list no longer shows, and other systems key their records by object UID, so a partial or altered copy would cause real damage, not just errors.

## Design

### How a move works

1. The etcd-backed API servers become read-only. They keep serving reads and watches, and reject writes.
2. A migration controller snapshots etcd, copies Milo's keys into CockroachDB with their resource versions, and checks the copy.
3. CockroachDB-backed API servers start beside the old ones, also read-only.
4. The Service moves to the new servers, and the old servers drain, so their clients reconnect to the new ones.
5. Once no old server is left, the controller makes a final check and allows writes on the new servers.

Writes are rejected from step 1 to step 5, and reads keep working throughout. The sections below cover the read-only mode, the copy and its checks, how resource versions carry over, the resource the controller works from, what clients see, and how to go back.

### Read-only mode

Milo gains a read-only mode. A server runs in one of three modes:

- **Read-write** serves everything, as today.
- **Read-only** keeps serving reads and watches, and rejects every write that would persist with `429` and a retry hint, including writes from `system:masters` and the server's own loopback.
- **Read-only except start-up** also allows the server's own loopback writes, so start-up work such as the CRD bootstrap can finish.

Requests that look like writes but store nothing, such as TokenReview and SubjectAccessReview, pass in every mode, so aggregated API servers keep authenticating through Milo.

A server follows a mode only when it is started with a migration setting that names a `StorageMigration`. Without that setting, which is the normal case, a server is always read-write and never consults the hosting cluster.

A server with the setting reads its mode from the resource's status, and isn't ready until it has read it once. It exports the mode it has applied as a metric, next to the API server's existing count of mutating requests in flight, and the controller reads both from every pod. Start-up work has to succeed without writing when its objects already exist, so a server that starts read-only still becomes ready.

Two writers bypass the modes. The etcd-backed servers refresh a peer-endpoint lease key every 10 seconds, which carries no data, so the copy skips it and the audit ignores it. CockroachDB-backed background work, such as the expiry sweeper, stays off until writes are allowed.

### Copy and checks

The copy reads Milo's keys from etcd at one revision, RV, described in the next section.

- **Keys.** A key under `/projects/<name>/` goes to that project, and a key under `/datum-core/` goes to the shared scope. The copy touches nothing else in etcd.
- **Values** are copied as bytes. Both backends encode and authenticate values the same way, so the same bytes are valid on both sides.
- **Resource versions.** Each object keeps its etcd revision, so preconditions clients hold keep working.
- **No expiring keys.** Read-only mode can't stop etcd from expiring a key attached to a lease, so Milo must store none, apart from the peer-endpoint lease key. Production has none, because Events go to the activity service. Preflight checks this, and the copy refuses any it meets.

Three checks guard the move:

- **The digest check** compares a digest of every key, revision and value, per project, between etcd at RV and CockroachDB, and names any key that differs.
- **The audit** watches Milo's prefixes from before the switch to read-only. It has to show progress past a revision read after the drain, with no Milo write after RV, including expiry. A disconnect, a compaction or a gap stops the move, because silence alone isn't proof.
- **The start-up check** compares the destination with the digest once every new server has started and gone read-only. Start-up may only change objects the server owns and reconciles: built-in CRDs, API Priority and Fairness defaults and default RBAC. Nothing writes to CockroachDB from then until writes are allowed, and a final check confirms it.

The copy and the digest check can run against live etcd ahead of time, without changing anything, to measure their duration and prove the key mapping.

### Revisions

Clients treat a resource version as a position in a stream of changes. A client that resumes a watch from revision X expects every change after X, and nothing at or before it. The move has to keep that true while clients switch from etcd's revisions to CockroachDB's.

etcd keeps one revision for the whole cluster, while CockroachDB counts each project separately. The move uses two values:

- **RV** is etcd's revision once every etcd-backed server is read-only with no writes in flight. Milo's data doesn't change after it.
- **RV + b** is where every project's revisions in CockroachDB start, and where new projects start too.

**Why CockroachDB can't start right after RV.** Milo's data stops changing at RV, but etcd's revision keeps climbing, because the other API servers on etcd keep writing. The old servers keep answering reads until they drain, and each response carries etcd's latest revision. So a client can leave them holding RV + 300, for example.

The new servers tell an old revision from a current one by comparing it with where CockroachDB's revisions start. If CockroachDB started at RV + 1, RV + 300 would look like one of its own. The client's watch would resume from it instead of getting `410`, and it would never see CockroachDB's first 300 changes.

Starting at RV + b, with b larger than any revision etcd reaches before the old servers are gone, keeps every revision a client can hold below where CockroachDB starts, so every one of them gets `410`.

**How large b is.** b only has to cover the other API servers' writes between the switch to read-only and the end of the drain. Their write rate is etcd's revision rate minus Milo's own stored writes, both in production's metrics, taken at the busiest time of day. b is that rate multiplied by the longest the read-only period and the drain can last before the controller's timeouts stop the move, with a wide margin on top. Writes resume only after no etcd-backed server is left and etcd's revision is still below RV + b, which proves that no client could have received a revision at or above it.

**The floor is stored.** The copy writes RV + b to the destination once, before any server starts, and every server reads it at start-up. A project without a counter row, whether new or copied with no keys, is at that floor for reads, its first write and the watch feed.

**The feed starts after the import.** Imported objects keep their sparse etcd revisions, and the watch feed releases each project's changes in consecutive order, so replaying the import would stall it. The copy records when it finished, and a server's feed never starts from before that point, on its first start or any restart.

**Every client lists again once.** The CockroachDB-backed servers answer any revision below RV + b with `410`: a watch, an exact read, or a list continue token from the old servers. The client then lists again from current data and watches from there, as it does after any `410`. A read without a revision answers from current data, as usual. Because every revision a client can hold is below RV + b, no client can resume from a revision the new servers would misread, and the new servers need no history from before their own start.

### The StorageMigration resource and controller

The migration controller runs as its own Deployment in the hosting cluster, added for a move and removed after it. It can't live in Milo's controller manager, which keeps its lease in Milo and loses it once Milo is read-only. It reaches etcd and CockroachDB with the Secrets the spec references, which are the ones each set of API servers already uses. It has permission to scale the two Deployments, change the Service's selector, and read the API server pods and their metrics.

```yaml
apiVersion: storage.miloapis.com/v1alpha1
kind: StorageMigration
metadata:
  name: etcd-to-cockroachdb
spec:
  source:
    type: Etcd
    deploymentRef:
      name: milo-apiserver              # the API servers the move starts from
    credentialsRef:
      name: <secret>                    # the Secret those API servers use to reach etcd
  destination:
    type: CockroachDB
    deploymentRef:
      name: milo-apiserver-cockroachdb  # the API servers the move ends on
    credentialsRef:
      name: <secret>
  serviceRef:
    name: milo-apiserver                # the Service whose selector moves
  gates:                                # like a pod's schedulingGates
    - name: ReadOnly
    - name: Switch
    - name: Finish
status:
  observedGeneration: 1
  step: Copy                            # the phase in progress, for display
  revision: 731954112                   # RV
  revisionBuffer: 100000                # b
  servers:                              # every API server pod, read from its metrics
    - name: <pod>
      deploymentRef:
        name: milo-apiserver
      mode: ReadOnly
      writesInFlight: 0
  conditions:                           # standard conditions, one per check
    - type: SourceReadOnly
      status: "True"
      reason: AllServersReadOnly
      message: 3 of 3 servers are read-only with no writes in flight
      observedGeneration: 1
      lastTransitionTime: "2026-11-20T06:02:11Z"
    - type: CopyVerified
      status: Unknown
      reason: Copying
      observedGeneration: 1
      lastTransitionTime: "2026-11-20T06:02:40Z"
```

The resource follows the usual API conventions. It is namespaced, and its references point at objects in its own namespace. Progress is reported as standard conditions, one per check, each with a reason and the generation it saw: `SourceReadOnly`, `SnapshotRestored`, `CopyVerified`, `AuditClean`, `StartUpVerified`, `SourceDrained` and `BelowRevisionBuffer`, and a `Ready` condition once the move is done. `status.step` names the phase in progress for people reading the resource, but the conditions are what the controller and any tooling act on.

Gates work like a pod's scheduling gates: the controller won't start a phase while a gate with its name is listed. The resource starts with three, and the operator removes each one when ready, directly or through Git. The phases, in order:

| Phase | What the controller does | Writes |
|---|---|---|
| Preflight | Checks versions, the schema, CockroachDB's health and a recent backup, capacity, that every route into Milo ends at the Service in the spec, and that no Milo key in etcd expires. | accepted |
| ReadOnly | Sets the source servers to read-only, and waits until every pod's metrics show that mode and no writes in flight. Records RV. | rejected from here |
| Snapshot | Snapshots etcd, restores it elsewhere to check it, and stores it outside the cluster. | rejected |
| Copy | Copies Milo's keys at RV, stores the floor RV + b and the time the copy finished, and runs the digest check. | rejected |
| Start | Scales up the destination servers in the read-only-except-start-up mode. | rejected |
| VerifyStartUp | Once every destination server has finished starting, sets them to read-only and runs the start-up check and the audit. | rejected |
| Switch | Points the Service's selector at the destination servers. The source servers keep serving the connections they hold. | rejected |
| Drain | Shuts the source servers down gracefully and scales them to zero, so their clients reconnect to the destination. | rejected |
| Check | Confirms etcd's revision is below RV + b, the audit saw no Milo write after RV, and the destination hasn't changed since the start-up check. | rejected |
| AllowWrites | Sets the destination servers to read-write and starts their background work. | accepted |
| Monitor | Reports relists, lease recovery, errors and feed lag. | accepted |
| Finish | Marks the move complete. | accepted |

Each phase is idempotent, and the status holds its progress, so a restarted controller continues where it stopped. Each phase has a timeout in the controller's settings.

The `Switch` gate is the last point where stopping costs nothing, so removing it is the decision to expose CockroachDB. Switch, Drain, Check and AllowWrites then run without waiting on the operator, so nobody holds writes paused while deciding. A failed check stops the controller in its phase, with the reason in the status, and the operator decides between fixing the cause and going back.

### What clients see

- **Reads and watches keep working** throughout, from the old servers, then both, then the new ones.
- **Writes get `429` for a few minutes,** from the ReadOnly phase to AllowWrites. client-go and the gateway retry on their own. The pause is the sum of its phases:
  - the snapshot and the copy, from etcd's database size, confirmed by a copy against live etcd
  - the new servers' start-up, from their time to become ready
  - the drain, from the API servers' shutdown delay and grace period
- **Controllers whose leases live in Milo restart once,** after their renew deadline passes while Milo is read-only, and take their leases back when writes resume.
- **Every client lists again once,** when its watch or paged list reaches a new server and gets `410`.
- **While connections move,** the new servers' start-up changes show only on the new servers, and a client with connections to both sides sees them after its relist.
- **A burst of writes** follows AllowWrites as controllers catch up.

### Going back

Going back is supported from every phase. What it takes depends on how far the move got, because clients start receiving CockroachDB's revisions at Switch, and client writes reach CockroachDB at AllowWrites:

| The move stopped | How to go back | What it costs |
|---|---|---|
| Before Switch | Delete the `StorageMigration`. The controller sets the source servers back to read-write, scales the destination down and removes its data before letting the resource go. | Writes resume on etcd. Nothing changes for clients. |
| From Switch until AllowWrites | Create a `StorageMigration` from CockroachDB back to etcd, which raises etcd's revision with a short run of writes. | Writes stay paused a little longer, and every client relists. The other API servers on etcd stay up, but their old watches get `410`. |
| After AllowWrites | Create a `StorageMigration` from CockroachDB back to etcd, which restores etcd with a revision bump. | A short outage of the other API servers on etcd, and every client relists. |

In the last two cases the forward resource stays in place as the record of what happened, with its `Ready` condition false and the reason `Abandoned`.

#### Before writes resume

CockroachDB has taken no client writes yet, but start-up has changed server-owned objects such as built-in CRDs, and clients may have seen those changes. So the controller:

1. Sets the CockroachDB-backed servers fully read-only and drains them, then reads CockroachDB's highest revision.
2. Raises etcd's revision past it with a short run of single writes on a key outside Milo's prefixes. The gap is about b, so this takes little time.
3. Compacts etcd at the new revision, so every watch from before gets `410` and every client lists again.
4. Continues from Start for the etcd-backed servers, whose own start-up reconciles the server-owned objects.

#### After writes resume

Once clients have written to CockroachDB, its data has to go back to etcd, and etcd's revision has to end up above everything CockroachDB has issued, however long it has run. Here RV is CockroachDB's highest revision across all projects. etcd's own snapshot restore covers both in one step: `etcdutl snapshot restore` with `--bump-revision` adds a fixed amount to the snapshot's revision, and `--mark-compacted` marks every earlier revision as compacted, which ends every watch from before the restore.

So the controller replaces the Copy phase with these steps, while the CockroachDB-backed servers stay read-only and keep serving reads:

1. **Pause the other API servers on etcd.** A restore starts a new etcd cluster from a snapshot, so they are scaled down for it. None of their writes lands after the snapshot, and their reads are unavailable for the restore's few minutes.
2. **Snapshot and restore.** The controller snapshots etcd, keeps that snapshot outside the cluster, and restores it as the new cluster with its revision bumped past RV + b and every earlier revision marked compacted.
3. **Mirror.** It makes Milo's prefixes in the restored etcd match CockroachDB exactly, writing every live object and deleting keys CockroachDB no longer has, then verifies the result, including projects deleted entirely.
4. **Bring the other API servers on etcd back.** Their watches ended at the restore, so their clients relist.

The move then continues from Start for the etcd-backed Milo servers.

The bump comes before anything is written, so every mirrored object gets a revision no client has seen. Otherwise an object's new revision could equal a stale one a client holds, and that client's update would pass its precondition. Every client relists, because every revision it holds is below the restored cluster's, and relisting discards data that existed only in CockroachDB.

Going back this way also takes the other API servers on etcd down for a few minutes, so it runs as a maintenance window for them as well as Milo. It restores Milo's data but not actions controllers took outside Milo meanwhile, which reconcile toward the restored data.

#### The snapshot is not a way back

The etcd snapshot a move takes is for disaster recovery. Restoring Milo to it would drop every write made on CockroachDB since, along with the other API servers' writes since the snapshot, so the controller never does it. If it's ever needed, it is a manual recovery, covered in the runbook below, with the same revision bump so that clients relist.

## The operator's role

An operator runs the move, and announces the window on the status page beforehand. The controller does the work, and the operator decides when it may go on.

The controller comes with a runbook, written alongside it and used in every rehearsal. It names the dashboards and checks to look at before each gate, gives the manual steps for every phase with `etcdctl`, `etcdutl` and `cockroach sql`, and covers going back from each phase. The operator's steps:

1. **Ahead of the window,** apply the controller and the `StorageMigration` with its three gates in place. While the `ReadOnly` gate is listed, the resource tells every server to stay read-write. Wait until its status shows that.
2. **Then roll out the migration setting** to the etcd-backed servers, like any other change. Each server reads its mode, read-write, and becomes ready as before. The order matters: servers with the setting can't become ready until the resource exists.
3. **At the window,** suspend Flux's reconciliation of Milo's Kustomizations, so Flux doesn't undo the controller's changes.
4. **Check Preflight** in the resource's status, and remove the `ReadOnly` gate when it passes.
5. **Follow the move** through the status and the dashboards the runbook names. Before removing the `Switch` gate, confirm the copy, the audit and the start-up check, since stopping still costs nothing then.
6. **After writes are allowed,** watch relists, lease recovery and errors, and remove the `Finish` gate once they settle.
7. **Afterwards,** make the CockroachDB-backed servers the declared state in Git, remove the migration setting, the controller and the resource, and resume Flux.

The operator can stop the controller before any phase it hasn't started by adding a gate with that phase's name. At any point they can also work on the stores directly, with `etcdctl` and `etcdutl` against etcd and `cockroach sql` against CockroachDB, to inspect what the controller did or to fix what it can't. The controller continues from its status once the gate is removed.

For development, the same phases run as `milo storage migrate`.

## Rehearsal

Rehearsals answer one question: does the move run correctly in an environment that behaves like production?

- **Production's definitions.** A dedicated cluster is built from the infrastructure repository's production overlays, with secrets and endpoints swapped, so the Milo bundle, flags, feature gates, webhooks, API services, etcd setup and lease timings match production.
- **Production's data shape.** A scrubbed copy of production's data keeps the key layout, the other API servers' prefixes and the revision height, with secrets replaced and personal and payment data faked, built from production backups. Generated data shaped like production is the fallback, with its gaps stated.
- **Production's clients.** The real lease holders, the controllers that delete what a list no longer shows, and the heavy watchers run against it, with outbound traffic blocked and test keys, and edge-dependent controllers stubbed downstream.

A rehearsal passes when:

- a probe client's reads are never unanswered for longer than a normal retry
- every closed watch recovers within a set bound
- the write pause meets its target
- digests and object counts match
- deletes stay at their normal rate
- controllers recover within a set time

Each series also goes back cleanly at least once. Before staging, the move and going back run cleanly in consecutive rehearsals.

## Alternatives

| Alternative | Why it lost |
|---|---|
| Cut the old servers off, then switch | Every request fails for a few seconds. Draining after the switch keeps reads up, and checking etcd's revision after the drain gives the same safety. |
| Stop the world | No read-only mode to build, but reads go down for the whole window. |
| Move one project at a time | Short pauses and a real canary, but both backends in every server, runtime storage swaps, and still a global read-only window for the shared scope. |
| Keep CockroachDB in sync ahead of time | A pause of seconds, but warm servers' writes race the catch-up, which needs a standby mode on top of read-only mode. |
| Follow etcd's watch instead of copying | Exact deletes, but reconnects, checkpoints and compaction to handle, for data that copies in seconds. |
| Servers always follow a mode | Every server would depend on the hosting cluster at start-up, for something only a migration needs. |
| Run the move from CI or an engineer's terminal | A run that dies mid-window can't pick up where it stopped, and its progress lives only in a log, so the next operator can't see what happened. The terminal stays for development. |
| Start CockroachDB's revisions at 1 | Clients would hold revisions above new ones and miss events. |
| Resume watches across the switch | Most clients would skip the relist. But the new servers would have to treat every revision from RV up to RV + b as current, answer exact reads in that range from the copy, and keep their history from RV + b until writes are allowed. Those rules run only during the switch, so production would exercise them once. The relist they save is one list per open watch, a load the performance tests' LIST rates and watch ramps cover, and the going-back paths need anyway. Revisions still have to start at RV + b either way. |
| Go back after writes resume by raising etcd's revision with writes | No outage for the other API servers on etcd, but one write per revision of difference, which grows with every CockroachDB write and limits how long going back stays possible. |
| Make the storage itself read-only, through etcd's per-prefix permissions and CockroachDB privileges | The storage would enforce read-only mode whatever any server does, which removes the mode from Milo. But production's etcd runs without authentication, and turning it on needs an etcd user for Milo and for each of the other API servers on etcd first, and any left without one is locked out. Writes would also fail with an internal error instead of `429`. Worth revisiting if etcd authentication is turned on for other reasons. |

## Failure modes

| Failure | What clients see | Recovery |
|---|---|---|
| Some servers don't go read-only | Writes succeed on some servers | Set the source back to read-write, then retry |
| A write gets past read-only mode, or the audit loses coverage | Writes stay paused | The audit stops the move before AllowWrites. Go back as [Going back](#going-back) says for the phase the move stopped in, then retry |
| The digest or start-up check fails | Writes stay paused | Set the source back to read-write and investigate |
| Destination servers don't become ready | Writes stay paused | Set the source back to read-write |
| A source server doesn't drain | Writes stay paused, reads work | Delete the pod after its grace period |
| etcd's revision passed RV + b at Check | Writes stay paused, reads work | Move every project's start past etcd's revision and restart the destination before AllowWrites, with clients relisting. Otherwise go back |
| The relist after the switch, or the burst after AllowWrites, overloads the new servers | Slow requests for a few minutes | The relist is one list per open watch, spread out by client backoff, which production's watch counts and list sizes bound |
| The controller fails mid-move | The move pauses | A restarted controller resumes from the status |
| Going back fails to verify | Milo stays read-only on CockroachDB | Repair Milo's prefixes in the restored etcd and verify again, before the other API servers on etcd come back |
| The etcd restore fails when going back | The other API servers on etcd stay down, and Milo stays read-only on CockroachDB | Restore again from the same snapshot, which is kept outside the cluster |
| A problem after AllowWrites | Depends on the problem | Fix forward, or go back |

## Before the window

- etcd snapshots and CockroachDB backups, each with a tested restore.
- A licensed CockroachDB cluster on the version and settings the performance tests used, with the schema migrated.
- A copy and digest check against live etcd that confirms how long they take, and b computed from production's metrics.
- Clean rehearsals in the production-like environment, then on staging. Client problems the rehearsals surface are fixed before staging.
- Going back rehearsed from each row of its table, including the etcd restore and the other API servers' recovery, with an `etcdutl` that supports the revision bump on the etcd version production runs.
- The runbook, tested in those rehearsals.
- The controller, the resource and the migration setting in place, in the order the operator's steps give.

## Decisions

- Draining after the switch was chosen over cutting the old servers off, because reads stay up.
- Resource versions are kept, and CockroachDB starts at RV + b, so preconditions keep working and no client misses an event.
- Every client relists once at the switch rather than resuming its watch, because that removes the rules only the switch would run, at the cost of one list per open watch.
- The copy happens inside the window, because Milo's data copies in seconds to a minute.
- A separate controller driving a `StorageMigration` resource, with gates an operator removes, was chosen over CI or a terminal, because every step is recorded and resumable and rehearsals run the same code.
- Servers follow a mode only with the migration setting, because nothing else needs it.
- Milo keeps no expiring keys in etcd during the move, because read-only mode can't stop expiry.
- Going back after writes resume restores etcd from a snapshot with a revision bump, because the bump has no size limit and ends every stale watch, at the cost of a short outage of the shared etcd.
- Rehearsals in an environment built like production come before staging.

## Open questions

- Scrubbed or generated data for rehearsals.
- How long the other API servers on etcd can be down for a restore when going back.
