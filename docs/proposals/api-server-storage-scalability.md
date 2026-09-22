# RFC: API server storage scalability for multi-tenant projects

- Addresses: https://github.com/milo-os/milo/issues/596 (storage does not scale with project count)
- Related: https://github.com/milo-os/milo/issues/631 (controller-manager OOMKill), https://github.com/milo-os/milo/issues/640 (controller-manager sharding RFC)
- POCs: https://github.com/milo-os/milo/issues/635 (kplane implementation), https://github.com/milo-os/milo/issues/647 (upstream key-injection implementation),

## Problem

Milo's virtualized project control plane allocates **dedicated storage infrastructure per project** — a full watchcache, etcd connections, and informer factories — and never reclaims them. Every project that receives an API call permanently increases the API server's memory and etcd connection footprint until the process restarts. This is the storage-layer analogue to the goroutine-stack problem documented in #631, but it lives in the API server rather than the controller-manager.

The two root causes are distinct:

1. **Per-project watchcaches.** Each project gets its own `cacher.Cacher` instance, which holds an in-memory btree, a list/watch goroutine stack, and an etcd watch stream. These resources are never freed.
2. **Per-project etcd watch streams.** etcd has practical limits on concurrent watch streams, total key count, and write throughput. With one watch stream per project per resource, a single etcd cluster reaches its limits well before the project counts a SaaS product requires.

### What the scale benchmark shows

Both candidate PRs (#635, #647) include a benchmark harness (`task test:scale`, introduced in #615). At 100 projects across 5 runs, both implementations record 0 etcd watchers per project, confirming that shared watchcaches are the fix.

### Why this is separate from #640

#640 addresses the **goroutine-stack floor** in the controller-manager (~1,100 goroutines per project, 1.51 GiB stack at 395 projects). That is a distinct process with a distinct fix (sharding cluster engagements). The two efforts are orthogonal: this RFC fixes the API server's per-project watchcache and etcd watch problem; #640 fixes the controller-manager's per-project cluster engagement problem. Both are required to meet #596's success criteria. Neither blocks the other.

## Goal

A single Milo API server instance can serve tens of thousands of projects without per-project growth in watchcache count, etcd watch stream count, or in-memory storage footprint. Adding a new project does not allocate new storage infrastructure.

Non-goals: reducing the controller-manager goroutine floor (see #640); changing etcd topology or adopting alternative storage backends; key-layout migration tooling (tracked separately once an approach is chosen); eviction policy (planned as a follow-on once reduced-overhead work lands, no issue tracked yet).

## Plan

Today Milo allocates dedicated infrastructure per project — watchcaches, etcd watch streams, goroutine pools — and never reclaims it. Memory and connection count grow linearly with total project count, and the system hits its ceiling in the hundreds of projects (#631). The benchmark harness introduced in #615 makes that ceiling measurable and regression-detectable going forward.

**Reduce per-project overhead.** The first step is to make the per-project cost small enough that the linear growth is no longer a crisis. This RFC does that for the API server (shared watchcaches, zero etcd watch streams per project); #640 does it for the controller-manager (eliminating per-project goroutine stacks via replica sharding). These two efforts are independent and together target an instance that can serve a meaningfully larger project count without degradation.

**Introduce an eviction policy.** Reduced overhead alone still leaves memory growing with _total_ project count. The more powerful property to establish is that cost tracks _concurrently active_ projects, not total projects — because at SaaS scale, most projects are idle most of the time. An eviction policy tears down the in-memory infrastructure for projects that haven't been accessed recently and rebuilds it on demand when a new request arrives. The cold-start cost of re-engaging an evicted project (re-warming watches, re-syncing caches) is the explicit trade-off: it introduces latency on the first request after eviction. This reduced-overhead work is a prerequisite — eviction only makes sense once the cost of re-engagement is low.

**Shard across replicas.** With eviction in place, the constraint shifts from total project count to _peak concurrent active_ project count per instance. Sharding splits that active set across replicas so each instance holds a proportional share. This is a later-stage problem: eviction should land first and its observed ceiling should drive the decision of whether and when sharding is actually needed.

**Replace the storage backend.** etcd's watch stream pressure — the most immediate bottleneck — is eliminated by this RFC. The remaining etcd ceilings (write throughput, total key count, operational complexity at scale) are much higher and will only bite at project counts we have not yet characterized. The key-layout change in this RFC — embedding project identity in the storage key — is the prerequisite for swappability: once tenant isolation lives in the key structure rather than in separate storage instances, the backend becomes replaceable. Concrete paths exist in the kplane ecosystem ([`kplane-dev/spanner`](https://github.com/kplane-dev/spanner), [`kplane-dev/kplane-kine`](https://github.com/kplane-dev/kplane-kine), [KPEP-0001](https://github.com/kplane-dev/enhancements/pull/1)), and approach (b) in this RFC preserves the same option via the upstream `storage.Interface`. This work is deferred until the etcd ceiling is actually observed.

## Approaches

Both candidate PRs reach the same headline numbers but differ structurally.

### (a) Adopt the kplane library (PR #635)

Replace Milo's per-project storage mux with `github.com/kplane-dev/apiserver`, which embeds tenant identity in the storage key and provides shared watchcaches across tenants. The approach calls `kplanestorage.WithKplaneStorage(getter)` at `CreateServerChain`, adds a `kplanestorage.WithProjectAsCluster` middleware to bridge Milo's `request.WithProject(ctx)` to kplane's `mc.WithCluster(ctx)`, and removes the per-project `RESTStorageProvider` wrapping.

**Dependency profile.** kplane achieves shared watchcaches by patching the upstream `k8s.io/apiserver` watchcache. Those patches are not in upstream Kubernetes. Adopting kplane therefore requires pinning all `k8s.io/*` modules to the kplane-dev fork of Kubernetes:

```
k8s.io/api => github.com/kplane-dev/kubernetes/staging/src/k8s.io/api
k8s.io/apiserver => github.com/kplane-dev/kubernetes/staging/src/k8s.io/apiserver
k8s.io/client-go => github.com/kplane-dev/kubernetes/staging/src/k8s.io/client-go
... (7 modules total)
```

PR #635 also bumps Go 1.25 → 1.26.3 and `k8s.io/*` v0.35 → v0.36 (required by the kplane fork), and carries `k8s.io/client-go v1.5.2` — kplane's own versioning scheme for the forked module, not the upstream semver.

**Pros:**

- Small call-site diff; the complexity lives inside the kplane library.
- kplane provides concrete alternative storage backend implementations — [`kplane-dev/spanner`](https://github.com/kplane-dev/spanner) (Google Cloud Spanner) and [`kplane-dev/kplane-kine`](https://github.com/kplane-dev/kplane-kine) (SQL-backed) — and [KPEP-0001](https://github.com/kplane-dev/enhancements/pull/1) proposes a pluggable backend registry that lets additional non-etcd backends slot in without further fork modification. These are concrete paths beyond etcd if etcd proves to be the bottleneck (#596 explicitly anticipates this).

**Cons:**

- Every upstream `k8s.io/*` security patch or feature requires kplane to pick it up before Milo can consume it. Milo's upgrade cadence becomes coupled to an external maintainer.
- The fork replaces the entire `k8s.io/apiserver` watchcache — a large, critical surface area — with code Milo doesnt own.

### (b) Embed project identity in the storage key (PR #647)

Stay on the upstream `k8s.io/*` packages. Create one shared `storage.Interface` per resource. Inject a scope segment (`/clusters/<projectID>/`) into every etcd key by wrapping the `storage.Interface` at the `RESTOptionsGetter` layer. Tenant isolation is enforced by disjoint key subtrees in etcd and by aligning the shared cacher's in-memory btree with those subtrees.

The implementation uses three cooperating layers:

- **`projectKeyRewriter`** — a `storage.Interface` decorator that rewrites incoming keys from `<prefix>/<suffix>` to `<prefix>/clusters/<projectID>/<suffix>` (or `/root/<suffix>` for requests with no project context) before any read or write reaches etcd.
- **`tenantTransformer`** — a `value.Transformer` decorator that prepends a small framing header (`\x7f milo | keyLen | key | body`) to bytes flowing _up_ from etcd, carrying the etcd key into the codec without touching the object itself.
- **`tenantCodec`** — a `runtime.Codec` decorator that parses the header on decode, strips it before handing object bytes to the real codec, and records `(object UID → tenant)` in a per-cacher side channel (`tenantMap`). The watchcache's `keyFunc` is then wrapped to look up tenant by UID and produce in-memory btree keys that include the tenant segment, aligning the btree with the etcd key layout.

Nothing tenant-related is written onto the object itself — no annotations, no labels. Tenant identity is entirely off-object, invisible to admission, audit, webhooks, and API clients.

**Pros:**

- No fork dependency. Milo stays on upstream `k8s.io/*` and upgrades on its own schedule.
- The entire implementation lives in Milo's own codebase, visible and auditable.

**Cons:**

- The codec/transformer coupling is bespoke surgery on the watchcache's internal btree. It does not have upstream validation.
- The framing header protocol must remain stable forever, since bytes already written to etcd carry it. A mistake in the discriminator byte selection or framing layout is a data corruption risk.
- Any future upstream changes to the watchcache's internal key or decode path must be verified against this implementation.

**Security failure semantics.** Failures in this layer are designed to make objects invisible rather than visible to the wrong tenant. A missed or stale tenant mapping routes the affected object outside any project's key scope, so it cannot appear in project-scoped reads or watches. A detected divergence between the expected and recorded keys surfaces as a hard error. The mapping is kept current through deletion tracking and periodic reconciliation.

### Scale benchmark results:

| Metric (100 project)      | PR #635 (kplane) | PR #647 (upstream) |
| ------------------------- | ---------------: | -----------------: |
| Heap per project          |          3.0 MiB |            3.0 MiB |
| Sys per project           |          5.0 MiB |            5.0 MiB |
| Goroutines per project    |               90 |                 56 |
| etcd watchers per project |                0 |                  0 |

Both eliminate per-project etcd watch streams.

## Key-layout migration

Both approaches change the etcd key structure for every project-scoped resource. Existing keys (`<prefix>/<suffix>`) must be migrated to scoped keys (`<prefix>/clusters/<projectID>/<suffix>`) before the new storage layer is activated, or a dual-read migration must bridge the transition.

Migration design is out of scope for this RFC and should be tracked as a follow-on issue once this RFC is accepted. The migration must address:

- **Read-during-migration.** The API server must serve existing objects correctly while the migration is in progress. Either a dual-read wrapper (try scoped key first, fall back to legacy key, write back to scoped key) or an offline migration with a maintenance window.
- **Project identification.** The migration must determine the project ID for each existing key. For resources stored under namespaces that follow the `organization-{name}` naming convention, the project ID can be inferred from namespace + object labels; this must be verified against every stored resource type.
- **Rollback.** A failed migration must not leave the etcd keyspace in a state that prevents reverting to the previous binary.

## Rollout and validation plan

1. **Baseline.** Record `etcd_debugging_mvcc_db_total_size_in_bytes`, `etcd_server_watch_streams`, API server goroutine count, and API server working set at current project count.
2. **Unit tests.** The `tenantKeyRewriter`, `tenantTransformer`, and `tenantCodec` layers must have unit tests covering: key injection, header framing/unframing, decode round-trip, tenant isolation (reads from project A cannot observe project B's objects), and `continue` token round-trip (the scoped key prefix must survive encode/decode through the pagination path).
3. **Scale benchmark (staging).** Run `SCALE_PROJECTS=500 task test:scale` in a staging environment and confirm per-project etcd watcher count stays at 0 and heap growth stays within the per-project budget established in the benchmark.
4. **Isolation correctness.** Assert that a list or watch request scoped to project A returns no objects owned by project B, across all registered resource types.
5. **Watch event delivery.** Assert that watch events for project A are delivered to watchers for project A and not to watchers for project B.
6. **Migration rehearsal.** Run the migration tooling against a staging etcd snapshot and verify that the migrated keyspace is identical to what a fresh write through the new storage layer would produce.
7. **Production rollout.** Blue/green: stand up a new API server replica with the new storage layer against the migrated keyspace before cutting over traffic. Monitor etcd watch stream count and API server working set for 24h before decommissioning the old replica.

## Open questions and risks

- **Eviction policy.** The Plan section describes eviction as a follow-on step: tearing down and rebuilding per-project infrastructure for idle projects so that memory cost tracks concurrently active projects rather than total project count. This is not in scope for this RFC. The key-layout change here makes no assumptions about eviction; it is a prerequisite (re-engagement cost must be low) but does not constrain the eviction design. No issue is tracked yet.
- **etcd ceiling.** #596 identifies etcd's write throughput and total key count as independent scalability limits, regardless of watch stream count. This RFC addresses watch stream count; the etcd ceiling at high project and object counts is a separate capacity planning question that requires benchmarking at 10k+ projects.
- **Alternative storage backends.** Under approach (a), kplane's existing backends ([`kplane-dev/spanner`](https://github.com/kplane-dev/spanner), [`kplane-dev/kplane-kine`](https://github.com/kplane-dev/kplane-kine)) and the pluggable registry proposed in [KPEP-0001](https://github.com/kplane-dev/enhancements/pull/1) provide a concrete path beyond etcd. Under approach (b), an alternative `storage.Interface` implementation can be written against the upstream interface; the key-injection decorator composes with any `storage.Interface` backend. Either way, this is deferred until the etcd ceiling is characterized (see above).
