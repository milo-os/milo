# RFC: Shard projects across controller-manager replicas

- Status: Draft (RFC, design only — no implementation)
- Author: @evetere
- Follow-up to: #631 (problem report), #632 (managedFields strip, partial mitigation)

## 1. Problem

The Milo controller-manager engages **one full controller-runtime
`cluster.Cluster` per Ready Project**. The Milo multicluster provider creates
this cluster in its `Reconcile` via `cluster.New` at
`pkg/multicluster-runtime/milo/provider.go:251`, starts its cache in a
dedicated goroutine (`provider.go:266`), waits for sync (`provider.go:275`), and
engages it with the multicluster manager (`provider.go:290`). Each engaged
project therefore gets its **own cache, informers, reflectors, and watch
goroutines**.

Because leader election is enabled and the deployment runs `replicas: 1`, a
**single active replica holds every project's cluster engagement
simultaneously**. The goroutine count — and the stack memory backing it — grows
linearly with project count:

| Metric (from #631)                | Value          |
| --------------------------------- | -------------- |
| Ready projects                    | ~395           |
| `go_goroutines`                   | ~438,125       |
| Goroutines per project            | ~1,100         |
| `go_memstats_stack_inuse_bytes`   | 1.51 GiB       |
| Working set                       | 5.51 GiB       |

Each project's reflector set (list/watch loops, cache processors, workqueue
workers, per-informer plumbing) accounts for ~1,100 goroutines. Goroutine
stacks are allocated from a pool that **grows but does not shrink back to the
OS**, so the ~1.51 GiB of stack memory is effectively a floor that rises with
project count and stays high.

### Why #632 does not fix this

#632 applies `cache.TransformStripManagedFields()` (see
`cmd/milo/controller-manager/controllermanager.go:641`) to drop `managedFields`
from cached objects, plus per-project quota cache scoping. That reduces the
**per-object heap** held in each cache. It does **not** reduce the number of
reflector sets: a single replica still runs one cache/informer/reflector stack
per project, so the **goroutine-stack floor is unchanged**. #631 explicitly
calls this out. Sharding is the architectural fix for the goroutine floor.

## 2. Goal

Distribute the per-project cluster engagements across **N replicas** so each
replica owns a **disjoint** subset of roughly `projects / N` projects. With the
per-project cost roughly fixed at ~1,100 goroutines + stack, spreading 395
projects over (say) 4 replicas brings each pod to ~99 projects → roughly **1/N
of the goroutine-stack floor per pod** (~0.38 GiB at N=4), while the cluster
total stays the same but is no longer concentrated in one process.

Non-goals: reducing the per-project goroutine count itself (a separate effort —
e.g. shared informers / metadata-only caches), and changing webhook or API
server topology.

## 3. Sharding approaches

The provider already exposes the natural sharding lever:
`Options.LabelSelector *metav1.LabelSelector`
(`pkg/multicluster-runtime/milo/provider.go:57`). When set, `New` wraps the
project watch in a label predicate so only matching Project objects are
reconciled (and thus engaged) — `provider.go:84-96`. Each approach below
decides **which replica owns which project**.

### (a) Static label-selector shards

Give each replica a distinct `LabelSelector` (e.g.
`milo.miloapis.com/shard=0`). Projects must carry a `shard` label, and
**something must assign that label** (an admission webhook or a small assigner
controller) at creation, keeping the assignment balanced.

- Pros: zero provider code change; uses the existing `LabelSelector` path
  verbatim; ownership is explicit and inspectable via `kubectl get projects -l`.
- Cons: requires a new label-assignment component and a backfill for existing
  projects; rebalancing means relabeling objects (mutating user/owned data);
  selectors are static per replica, so changing N means re-templating each
  replica's selector and relabeling. Operationally heavy.

### (b) Hash-based sharding (recommended)

Each replica is configured with `shardIndex` and `shardCount` (injected via env,
typically a StatefulSet ordinal). The provider's `Reconcile` computes
`hash(project.Name) % shardCount` and **skips** projects that do not map to its
`shardIndex` (early return before `cluster.New` at `provider.go:251`).

- Pros: no per-project labeling, no assigner component, no mutation of Project
  objects; ownership is a pure function of the name; trivially inspectable
  (`hash(name) % N`); composes with `LabelSelector` (selector filters the set,
  hash partitions the survivors).
- Cons: **changing `shardCount` reshuffles ownership** — with plain modulo,
  almost every project moves when N changes, causing a burst of
  tear-down/re-engage churn. Mitigate by:
  - using **consistent hashing / rendezvous (HRW) hashing** so a change from N
    to N+1 moves only ~`1/(N+1)` of projects, not nearly all of them; and
  - treating `shardCount` changes as deliberate, infrequent operations
    (scale events), accepting a bounded reconcile storm.

### (c) Lease-per-shard / partitioned leader election

Replace the single global lease with **one lease per shard**
(`datum-controller-manager-shard-<i>`). A replica acquires the lease for its
shard index and only then engages that shard's projects; on holder death another
replica acquires the orphaned shard lease and takes over. This is orthogonal to
(a)/(b): it answers *who is allowed to own shard i right now*, while (a)/(b)
answer *which projects belong to shard i*.

- Pros: active/active with HA per shard; failover is automatic.
- Cons: more leases and election machinery; needs a mapping from replica
  identity to shard index (or a claim protocol). Heavier than fixed
  ordinal→shard binding.

### Recommendation

Adopt **(b) hash-based sharding keyed by StatefulSet ordinal**, using
**rendezvous (HRW) hashing** rather than plain modulo to bound rebalancing
churn. A StatefulSet gives each pod a stable ordinal (`...-0`, `...-1`, …) that
maps directly to `shardIndex`, and the replica count maps to `shardCount`. This
needs no new label-assignment component, no mutation of Project objects, and
composes cleanly with the existing `LabelSelector` option. Per-shard leader
election (c) is the recommended HA layer on top (see §4); for an initial
rollout, the StatefulSet's own pod-identity guarantees (at most one pod per
ordinal) provide single-owner-per-shard without extra leases.

## 4. Leader-election interaction

Today a single global lease (`datum-controller-manager`,
`controllermanager.go:219`) ensures exactly one active replica, gated for all
controllers via `leaderElectAndRun` (`controllermanager.go:884`). That is
fundamentally incompatible with sharding: the standby replicas idle, so scaling
`replicas` does nothing. Sharding needs **active/active** — every replica runs
and owns its shard concurrently.

Two ways to get there:

1. **StatefulSet pod identity as the ownership guarantee (initial step).**
   Disable the single global lease for the shardable workload and rely on the
   StatefulSet invariant that ordinal `i` is held by at most one pod. Each pod
   owns shard `i`. Failover: when pod `i` dies, the StatefulSet recreates pod
   `i`, which re-engages shard `i`'s projects on startup. Gap window = pod
   restart time; no other replica covers shard `i` during that window.

2. **Per-shard leases (target state, approach (c)).** Run `shardCount` leases.
   Each replica attempts to acquire the lease for its own ordinal; a healthy
   replica renews it. If a holder dies and does not renew, a designated standby
   (or any replica configured to fail over) acquires the orphaned lease and
   adopts that shard — closing the gap window at the cost of running a hot/warm
   standby. This requires a replica→eligible-shard mapping or a claim protocol.

Recommended path: ship (1) first (simplest, leverages existing StatefulSet
semantics), then layer (2) for tighter failover if the restart-window gap proves
unacceptable.

## 5. Concrete change sketch (no implementation)

**Provider gating.** Add shard awareness to `Options` and gate `Reconcile`:

```go
// Options (pkg/multicluster-runtime/milo/provider.go:41)
type Options struct {
    // ... existing fields ...
    LabelSelector *metav1.LabelSelector // provider.go:57

    // ShardCount is the total number of shards (replicas). 0 or 1 disables sharding.
    ShardCount int
    // ShardIndex is this replica's shard, in [0, ShardCount).
    ShardIndex int
}
```

In `Reconcile` (`provider.go:154`), after fetching the project but **before**
`cluster.New` (`provider.go:251`), add an early ownership gate:

```go
if p.opts.ShardCount > 1 && !p.ownsProject(project.GetName()) {
    log.V(1).Info("Project not owned by this shard, skipping", "key", key, "shardIndex", p.opts.ShardIndex)
    return ctrl.Result{}, nil
}
```

where `ownsProject(name)` returns `hrw(name, shardCount) == shardIndex` (HRW /
rendezvous hashing). This sits alongside the existing `LabelSelector` predicate
(`provider.go:84-96`): the selector filters which objects the controller watches
at all; the shard gate partitions the survivors. They compose without
interfering. The existing "already engaged?" / NotFound teardown logic
(`provider.go:175-206`) is unchanged — projects that move shards are torn down
on the losing replica and engaged on the winning one.

**Injection.** `shardIndex` from the StatefulSet pod ordinal (parse
`$(POD_NAME)` via the downward API, or read a `SHARD_INDEX` env), `shardCount`
from a `SHARD_COUNT` env wired to the StatefulSet's replica count. Wire these
into the `miloprovider.New(ctrl, miloprovider.Options{...})` call at
`controllermanager.go:631`.

**Deployment shape.** Convert the controller-manager (or at least the
project-engagement workload) from a `replicas: 1` Deployment to a
**StatefulSet** with `N` replicas so each pod has a stable ordinal →
`shardIndex`. `SHARD_COUNT=N`. Disable the single global lease for this
workload (or scope it — see §7).

**Composition with `LabelSelector`.** Unchanged and additive: operators can
still restrict a whole deployment to a subset of projects via `LabelSelector`,
and sharding partitions within that subset. Approach (a) remains available for
operators who prefer explicit label-based ownership.

## 6. Rollout / validation plan

1. **Baseline.** Record `go_goroutines` and `go_memstats_stack_inuse_bytes` on
   the single active replica at current project count (~395), plus working set.
2. **Deploy sharded (canary N=2).** Roll out the StatefulSet with `SHARD_COUNT=2`
   in a staging environment with representative project counts.
3. **Per-pod metrics.** Confirm each pod's `go_goroutines` and
   `go_memstats_stack_inuse_bytes` are ~1/N of baseline and that the sum across
   pods is comparable to the old single-replica total.
4. **Exactly-once ownership.** Verify every project is engaged by **exactly
   one** replica — no gaps, no overlaps. Expose a provider metric
   (`milo_provider_engaged_projects{shard="i"}`) and assert the union equals the
   full Ready-project set and the pairwise intersection is empty. Cross-check
   against `hrw(name) == i` for each project.
5. **Failover.** Kill pod `i`; confirm shard `i`'s projects are re-engaged after
   restart (StatefulSet path) or adopted by a standby (lease path), and measure
   the gap window.
6. **Rebalancing.** Scale `SHARD_COUNT` from N to N+1 and confirm only ~`1/(N+1)`
   of projects move (HRW), bounding the reconcile storm; verify steady state
   re-converges to exactly-once ownership.
7. **Production rollout.** Increase N gradually, watching the per-pod stack
   floor and the rebalancing churn at each step.

## 7. Open questions / risks

- **Rebalancing churn.** Any `shardCount` change tears down and re-engages
  caches. HRW hashing bounds the fraction that moves, but a large N change still
  causes a reconcile storm (mass `cluster.New` / cache resync). Needs a
  rate-limit / staged-rebalance story and quantification at scale.
- **Lease scoping vs. singleton controllers.** `controllermanager.go` gates
  *all* controllers under one global lease (`leaderElectAndRun`,
  `controllermanager.go:884`). Sharding must apply **only** to the project
  engagement / quota multicluster workload; singleton controllers
  (organization, namespace, IAM, invitation controllers wired earlier in the
  same `Run`) must keep a single global leader. This likely means running the
  sharded multicluster workload as a separate process/StatefulSet from the
  singleton controllers, or introducing a second lease scope. **Needs design
  before implementation.**
- **Webhook traffic.** Validation/mutation webhooks are not sharded and will
  still hit all replicas (or whichever the Service routes to). Sharding the
  engagement workload does not reduce webhook load; if webhooks are co-located
  with the engagement workload, splitting them out should be considered.
- **Quota cross-cluster coordination correctness.** The quota system relies on
  the multicluster manager seeing engaged project clusters for cross-cluster
  coordination (`controllermanager.go:616-668`, QPS 500 / Burst 1000 for
  coordination). If projects are split across replicas, **each replica only sees
  its own shard's clusters**. Any quota logic that assumes a single process can
  observe *all* projects (e.g. organization-level aggregate quota that spans
  projects on different shards) would break. This must be analyzed: either quota
  aggregation is already per-project (safe to shard) or it needs a
  shard-aware/aggregating coordination layer. **Flagged as the highest-risk
  item; requires analysis before rollout.**
- **Hash function stability.** `shardIndex`/`shardCount` and the hash must be
  identical across replicas and stable across restarts/versions; a hash change
  reshuffles everything. Pin the algorithm and document it.
