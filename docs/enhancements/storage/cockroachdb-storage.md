---
status: proposed
---

# CockroachDB storage for Milo's API server

> Tracks [milo#596](https://github.com/milo-os/milo/issues/596).

## Summary

Milo's API server keeps a full Kubernetes storage stack for every project in every replica. Its memory and goroutines grow with the number of projects, busy or not.

This RFC proposes storing Milo's objects in a self-hosted CockroachDB cluster, through storage that Milo owns. There's no per-project cache. Reads go to the database, each project numbers its writes with a counter, and one change stream per API server replica feeds every watch.

Clients see the same Kubernetes API, including resource versions, watch resume, `410 Gone` and bookmarks. The cost is a new database to run, a ceiling on how fast one project can write, and a watch layer that Milo maintains itself.

## Problem

Each project gets its own Kubernetes API. For each project and each resource, every API server replica builds an upstream watch cache. The cache holds a full copy of the objects, a ring of recent events, an etcd watch and the goroutines that drive them. An idle project costs about as much as a busy one.

Production has over 500 projects and growing. Each API server pod holds tens of thousands of watch caches, and each project costs about 15 MiB of memory and a thousand goroutines before it does anything. Staging's API servers already run at their memory limit and restart several times a week.

Supporting many more users ([infra#7310](https://github.com/datum-cloud/infra/issues/7310)) needs Milo's cost to follow traffic, not project count.

Sharing one upstream cache across projects doesn't fix this. The cache keys objects by namespace and name only, so sharing it means tracking each object's project in a side table, wrapping key functions and filtering every event by project. Those workarounds depend on upstream internals, which Kubernetes has changed several times already. A shared cache also still holds every object in every replica, which becomes the next memory ceiling.

The cache exists to shield etcd, a single consensus group, from read load. A database that spreads reads across its nodes doesn't need that shield.

### Requirements

The goal for this design is to meet what production needs today, with about 10x headroom:

1. **Watch delivery in milliseconds.** Several flows chain controller watch events inside a single user's request, so a delay per hop multiplies.
2. **Writes.** A few dozen stored writes per second at peak, and about a dozen in the busiest scope. That's the resources every project shares, which handle every signup and membership change.
3. **Reads.** Several hundred GETs and a few hundred LISTs per second at peak, over collections of up to a few thousand objects.
4. **Full Kubernetes semantics.** Resource versions, lists at a revision, continue tokens, watch resume, `410 Gone`, bookmarks and preconditions.
5. **Memory that doesn't grow with idle projects.**

## Design

### Overview

Today etcd stores the objects, numbers every change and keeps a history of changes. The watch cache serves lists and watches from memory.

In this design, CockroachDB stores the objects and keeps their recent older versions. Milo numbers each project's changes with a revision counter. A hub in each API server replica keeps a few minutes of recent changes for watches, and lists read the database directly. An idle project leaves only a small record in the API server, with no cached objects.

```
                      API server replica

client ──write──▶ 1 admission ──▶ 2 group commit ──▶ 3 transaction ──┐
                                                                     ▼
client ◀─watch─── 6 hub ◀──────── 5 changefeed ◀──── 4 objects table
                                                       (CockroachDB)
client ──list──────────────────────────────────────▶ reads the table
```

1. Admission caps the writes in flight, per project and per server.
2. Group commit combines a project's concurrent writes into one transaction.
3. The transaction locks the project's counter, numbers the writes and stores the rows.
4. CockroachDB commits the rows, replicated across zones.
5. Each replica's changefeed streams every row change.
6. The hub orders each project's changes and delivers them to watches.

The team has already settled on CockroachDB. Three of its features apply to this design nicely:

- **Reads scale out.** Any node serves reads, so read load spreads across the cluster.
- **Reads at a past moment.** A read can ask for the data as it was at an earlier time, which is what a list at a revision needs.
- **Changefeeds.** A changefeed streams every row change with the values before and after it, which is what watches need.

### Resource versions

A project's resource version is a counter. Every write in the project takes the next number.

Every object is a row in one `objects` table, keyed by project and storage key, and each project has one counter row. For example, project `acme` has two pods, and its counter reads 41:

| project | key | revision |
|---|---|---|
| `acme` | (counter) | 41 |
| `acme` | `/pods/default/web` | 40 |
| `acme` | `/pods/default/db` | 41 |

Updating `web` locks the counter, writes `web` at 42 and moves the counter to 42, all in one transaction. A concurrent write to `db` waits for the lock, then gets 43. Revisions follow commit order and never skip a number.

A delete leaves a tombstone with its own revision, so watches see deletes like any other change.

Counting per project works because no Milo list or watch spans projects. Two projects can both be at 42, and nothing ever compares them. Resources that every project shares live in one more scope with its own counter, called the shared scope.

The counter serializes a project's writes. In return, it usually sits in the same range as the objects it numbers, so most writes are single-range transactions, the cheapest kind in CockroachDB. That's the common case, not a guarantee, because large projects span several ranges and the indexes for expired objects and tombstones are ranges of their own.

### Reads

GET and LIST read the database directly. A LIST reads one snapshot, and its resource version is the counter in that same snapshot, so the two always agree.

Two kinds of read need the data as it was at a past revision:

- the next page behind a continue token
- an exact-revision list, which informers send when they relist where they left off

These read as of the revision's commit time. Milo configures CockroachDB to keep each row's older versions for an hour, through the table's garbage-collection time-to-live (GC TTL), so any moment in that hour can be read.

For example, a client pages through 3,000 `acme` policy bindings at revision 42 while other writes move the project to 50. Every page reads as of 42's commit, so the list stays consistent.

Commit times come from the change stream, so every replica learns them, and any replica can serve the next page. A replica that no longer knows 42's commit time but knows 43's reads as of the instant before 43 committed. Nothing else in the project commits in between, so that's the same state. Replicas learn commit times in revision order, so this only happens once 42's has aged out, which means 42 and 43 committed separately.

Reads at a past revision reach back a few minutes, as far as watch replay. An older revision gets `410 Gone`, as it would after an etcd compaction.

### Watch ordering

The hub is a component in each API server replica. It orders incoming changes, keeps the recent ones, and hands them to watches.

Each replica runs one sinkless changefeed over the `objects` table. Sinkless means the API server reads the change stream directly over a database connection, with no message queue or webhook in between. That keeps delivery fast and adds no infrastructure to run.

The changefeed delivers row changes within milliseconds, but not in order across keys. The hub releases each project's revisions one at a time, after the last one it delivered, and never past the counter.

For example, the hub has delivered `acme`'s revision 41. Revision 43 arrives, and so does the counter's move to 43, but 42 hasn't arrived yet. The hub holds 43. When 42 arrives a few milliseconds later, the hub delivers 42, then 43. The counter tells the hub how far writes have committed, and consecutive revisions tell it nothing is missing. In tests, delivery from commit to watch took single-digit milliseconds.

The changefeed's own ordering signal, the resolved timestamp, trails commits by most of a second, so the hub doesn't order events by it. It tracks where a dropped changefeed resumes, when a starting replica has caught up, and how far the hub lags behind.

Each replica runs one changefeed, rather than one per project, because CockroachDB recommends a small number of changefeeds per cluster. Each replica's changefeed sees the writes of every replica, so replicas never need to coordinate.

### Watch resume and replica startup

Milo keeps two kinds of history, for two different purposes:

- **The hub** keeps a few minutes of each project's events in memory, so watches can resume.
- **CockroachDB** keeps an hour of row versions, so reads at a past revision work and a dropped changefeed can resume.

A client that drops at revision 40 reconnects from 40 and gets 41 onward from the hub, with no relist. A client gone longer than the replay window gets `410 Gone` and lists again, as it would after an etcd compaction.

A starting replica preloads the last few minutes from the changefeed, and reports ready once it has caught up. Without the preload, every rolling restart would send all of a replica's clients back to listing.

The hub also handles the edge cases that the upstream cache handles today:

- **Racing replicas.** A client lists on one replica at revision 43, then watches on another whose changefeed is only at 42. The watch waits the few milliseconds until that changefeed reaches 43.
- **Slow clients.** A watch that falls far behind is closed, and its client resumes from the recent events.
- **Changefeed trouble.** A dropped changefeed resumes where it stopped and loses nothing. One that can't resume starts again from the current state, and open watches get `410 Gone` and relist.

### Group commit and admission

The counter limits a project to about one commit per consensus round, around a hundred writes per second within one region. That's several times today's busiest scope (shared objects), but it grows with the platform.

Group commit trades a little latency for throughput. When writes queue up for a project, they commit together in one transaction.

For example, 30 controllers update `acme` objects at the same moment. Without group commit, they take 30 turns on the counter. With it, the first write commits alone, and the other 29 commit together as revisions 43 to 71. A lone write still commits right away. In tests, one project sustained 800 writes per second with group commit, the highest rate tried, against about a hundred without it.

Admission keeps one project from starving the rest. Each project gets a few writes in flight, and each API server keeps its transactions below its connection pool. A flood of writes to `acme` waits about a second for a slot, then gets `429 Too Many Requests` with a retry hint, which client-go already honors. Other projects keep writing at full speed.

### Topology

The cluster survives a zone failure, with nodes spread across one region's zones. The rest of Milo's infrastructure already lives in one region.

Surviving a region failure would mean placing database nodes in different regions, so every write would wait on a round trip between them. That can wait until the rest of the platform has to survive a region failure too.

The latency and throughput numbers in this RFC come from tests on a single machine, with latency between the database nodes simulated. They show which designs hold up and how they compare, not production's numbers. A benchmark on a real multi-zone cluster comes before enabling the backend.

### Upsides

- **Memory follows traffic.** An idle project costs a counter row in the database and a small record in the API server, with no cached objects. What's left per project is the cost of its open watches.
- **Reads scale with the database,** not with a full cache in every replica.
- **Durability can come from the database.** CockroachDB replicates across zones and supports scheduled backups, which production has to set up. Milo's etcd has neither backups nor a restore runbook today.
- **No tenancy workarounds** in upstream code, and nothing that breaks when upstream changes its cache.

### Downsides

- **A new database to run,** with its own upgrades, backups, capacity planning and on-call knowledge.
- **Sinkless changefeeds get less attention in CockroachDB's docs.** The docs position them mainly for prototyping, and they have no built-in metrics, so the hub exports its own. They held up well in tests, but their support could change in a future release.
- **Every read costs the database.** Today the caches absorb reads. At 10x today's peak, the cluster needs several cores just for reads.
- **One project's writes have a ceiling.** Group commit raises it well past production's needs, at the cost of latency in a busy project.
- **Milo owns the watch layer.** Correctness the upstream cache provided now rests on Milo's code, held to the upstream storage conformance tests.
- **Replay is bounded.** Each project keeps a few minutes of events, capped by count and by size, so a client away longer relists, as it would after an etcd compaction.

## Alternatives

The team has settled on CockroachDB, and the Problem section covers why etcd with a cache per project doesn't scale. These are the other CockroachDB designs that were explored, and what each one runs into.

### Commit timestamps as resource versions

CockroachDB gives every transaction an ordered commit timestamp. Using it as the resource version needs no counter, so a project's writes wouldn't queue, and the per-project ceiling would go away.

A transaction can't read its own commit timestamp without fixing it early. Normally CockroachDB moves a transaction's timestamp forward when it conflicts with concurrent reads. A fixed timestamp can't move, so the transaction aborts and retries instead, and under contention the retries pile up.

Turning timestamps into plain numbers has its own problem. Rounding them made concurrent writes collide, which caused thousands of retries in tests, and keeping full precision can still misorder writes.

A counter avoids both, at the cost of the per-project ceiling, which group commit raises well past production's needs.

### Watches ordered by resolved timestamps

A changefeed periodically sends a resolved timestamp, a promise that every change before it has arrived. A watch layer could hold events until a resolved timestamp covers them, then release them in order. That needs no counter.

Resolved timestamps trail commits by most of a second, even at the most frequent documented setting, so every watch event would wait that long. Several Milo flows chain controllers on watch events inside a user's request, so a second per hop becomes several seconds of waiting.

### A counter per project and resource

A finer design keeps one counter per project and resource. A project's pods and its config maps would count separately, so a burst on one wouldn't delay the other.

Within one region, tests showed worse tail latency for a busy project with per-resource counters than with one per project. It also multiplies the counter rows and the scopes the hub tracks.

Per-resource counters only pay off across regions, where each write's round trip is long. If the cluster ever needs to survive a region failure, this design is worth revisiting.

## Failure modes

| Failure | What clients see | Mitigation |
|---|---|---|
| A CockroachDB node fails | Brief latency while its ranges move | Replication across zones, and retries on conflict |
| A changefeed drops or stalls | Watch events pause, and nothing is lost | A stall watchdog, then resuming where the changefeed stopped |
| A changefeed can't resume, or falls hopelessly behind | `410 Gone`, and watches relist | Starting again from the current state |
| A replica's changefeed never starts | That replica stays out of service | Readiness waits for the changefeed's first update |
| A changefeed breaks after startup and can't resume | Watches on that replica stall, while reads and writes keep working | Lag keeps growing, for alerts. Readiness can follow lag too, but that's off by default, because lag usually comes from the cluster and would take every replica out at once |
| One project floods writes | `429` with a retry hint, for that project only | Per-project caps, and group commit |
| A commit's outcome is unknown | An error on a write that may have landed | Clients retry with preconditions, as with etcd timeouts |
| An API server restarts | Its watches reconnect and resume | The replay preload at startup |
| The database keeps old versions too briefly | The server refuses to start | A startup check that old versions outlive the replay window |

## Enablement and Rollback

The backend sits behind a storage-engine setting, and etcd stays the default.

Migrations set up the schema and the cluster settings. They run either as a separate step before new servers start, or as each server starts. Servers that migrate as they start take turns, and only the first finds anything to apply. Production uses the separate step, which keeps the rights to change the schema away from the servers.

A server won't start on a schema older than the one it was built for. Migrations only add, so older and newer servers both run against the same schema during a rollout and after a rollback.

The switch from etcd is one coordinated cutover, rehearsed on staging first:

1. Copy an etcd snapshot into CockroachDB, keeping resource versions, then follow etcd's changes to stay current. Every project's counter starts above the highest etcd revision, so any revision a client holds is smaller than any new one.
2. Mirror read traffic to the new backend and compare its answers with etcd's.
3. Pause writes for a few seconds with `429`, well short of the time it takes controllers to lose their leases. Let the copy catch up, then switch the API servers over.

Watches relist once after the switch, because their old revisions fall outside the new replay history.

### Before enabling in production

- **Migration tooling.** The snapshot-and-follow copy from etcd, keeping resource versions.
- **Comparison criteria.** What counts as a matching answer when mirrored reads are compared, and how long they must match before the switch.
- **Backups.** A schedule, a destination and a retention period for the cluster, and a tested restore.
- **A rehearsed rollback.** A way back to etcd after the switch, practiced on staging.
- **A multi-zone benchmark.** Write latency, delivery and read capacity, measured on a real cluster spread across zones.

## Open questions

- **Rollback after the switch.** A reverse copy into etcd, or a rehearsed restore.
- **Per-watch cost.** Once per-project caches are gone, each open watch's HTTP state is the largest remaining cost per project, and the server's watch handling could shrink it.
