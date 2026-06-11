# Assessment: Disabling the Embedded Kubernetes Garbage Collector at `--control-plane-scope=core`

Status: RFC / assessment (no behavioral change proposed yet)
Author: controller-manager working group
Follow-up to: #631, #632

## 1. Summary and question

The Milo controller-manager embeds the upstream Kubernetes garbage collector
(GC) controller. The GC controller builds a cluster-wide dependency graph by
starting one metadata informer ("monitor") per garbage-collectable GVK, so it
can track `ownerReferences` and perform cascade deletion. On an API surface with
many CRDs this graph builder is a meaningful, scope-independent baseline cost in
goroutines and metadata heap.

Issue #631 reported the core control plane carrying ~395 projects with
`go_goroutines` ~438,125 (~1,100 goroutines/project), `go_memstats_stack_inuse_bytes`
1.51 GiB, and a working set of 5.51 GiB. PR #632 reduced per-project quota cache
memory by stripping `managedFields`. This document assesses a complementary
lever:

> Should the embedded GC controller be disabled when the controller-manager
> runs at `--control-plane-scope=core`, to reduce memory and goroutine usage —
> and if so, is the cascade-deletion behavior the core control plane depends on
> covered by other mechanisms?

The conclusion is unchanged after code verification: disabling is **not safe
today**. Three owner-reference cascade paths in the core control plane rely on
the embedded GC — Organization → namespace (cascading to all namespace
contents), User → owned IAM objects, and OrganizationMembership → owned
PolicyBindings (Section 4b). A fourth (downstream anchors) is likely out of
scope but unconfirmed. The required conversions are listed in Section 7.

## 2. How the embedded GC works here

Registration and wiring live in the controller-manager command:

- The GC controller descriptor is registered alongside the namespace controller
  in `NewControllerDescriptors`:
  `cmd/milo/controller-manager/controllermanager.go:1087-1088`
  (`register(newNamespaceControllerDescriptor())` /
  `register(newGarbageCollectorControllerDescriptor())`).
- The descriptor itself is defined in
  `cmd/milo/controller-manager/core.go:77-83`
  (`newGarbageCollectorControllerDescriptor`), with init func
  `startGarbageCollectorController` (`cmd/milo/controller-manager/core.go:85-144`).
- The dependency graph builder is constructed in `CreateControllerContext`,
  gated by both component config and the generic enable/disable mechanism:
  `cmd/milo/controller-manager/controllermanager.go:1147-1162`:

  ```go
  if controllerContext.ComponentConfig.GarbageCollectorController.EnableGarbageCollector &&
      controllerContext.IsControllerEnabled(NewControllerDescriptors()[names.GarbageCollectorController]) {
      ...
      controllerContext.GraphBuilder = garbagecollector.NewDependencyGraphBuilder(...)
  }
  ```

- `IsControllerEnabled` honors the `--controllers` flag through the upstream
  generic helper: `cmd/milo/controller-manager/controllermanager.go:974-981`.
  This means the GC controller can be disabled **without any code change** by
  passing `--controllers=*,-garbagecollector`. The same flag set also gates the
  graph builder construction above, so disabling the controller also avoids
  building the dependency graph and its monitors — not just the GC workers.

- The control-plane scope flag is defined in
  `internal/control-plane/options.go:5-22` (`--control-plane-scope`, values
  `core` / `project`) and is branched on in the run loop at
  `cmd/milo/controller-manager/controllermanager.go:427`
  (`if opts.ControlPlane.Scope == controlplane.ScopeCore`). There is currently
  **no** scope-conditional gating of the GC controller; it runs identically in
  both scopes.

### Per-project amplification

`startGarbageCollectorController` does not only run the root graph builder. It
also wires a `GCSink` into the project provider
(`cmd/milo/controller-manager/core.go:129-141`), so every dynamically
discovered project gets hooked into GC. This is the same fan-out pattern that
drives the per-project goroutine multiplier observed in #631, and it means the
GC cost scales with project count, not just with the static core API surface.

## 3. Estimated cost

The graph builder starts one metadata informer/monitor per garbage-collectable
GVK. Each monitor is a reflector, which carries:

- a list goroutine and a watch goroutine (plus the watch's HTTP/2 stream),
- an in-memory `ThreadSafeStore` holding object **metadata** for every object of
  that GVK (the metadata informer already trims to `PartialObjectMetadata`, and
  the controller-manager additionally trims `managedFields` via the informer
  transform at `cmd/milo/controller-manager/controllermanager.go:1106-1120`),
- periodic resync work.

Rough order-of-magnitude model:

```
goroutines      ~= (monitors per partition) * (reflector goroutines per monitor)
                 ~= N_gvk * ~2..3
metadata heap   ~= sum over GVKs of (object count * trimmed metadata size)
```

where `N_gvk` is the number of garbage-collectable resource types the API
exposes (core + all CRDs). With the project-provider fan-out
(`core.go:129-141`), multiply the monitor set by the number of partitions
(root + projects). On a surface with dozens of CRDs and hundreds of projects,
the GC monitors are a plausible contributor to the goroutine and stack figures
in #631, alongside the quota caches addressed by #632.

Importantly, this cost is **directly measurable today** — no code change is
needed to quantify it. Deploy two otherwise-identical controller-managers and
compare:

- `go_goroutines`
- `go_memstats_stack_inuse_bytes`
- `go_memstats_heap_inuse_bytes`
- container working set

with the GC controller enabled vs. disabled via
`--controllers=*,-garbagecollector`. The delta is the GC controller's
attributable cost. See Section 6 for the full plan.

## 4. What relies on the embedded GC, and the risk of disabling

Disabling the GC controller removes owner-reference-based cascade deletion. Any
deletion path that depends on the controller observing an `ownerReference` and
deleting the dependent will silently leak orphaned objects. The core control
plane has several such paths. Each must be evaluated before disabling.

### 4a. Covered without the embedded GC

- **Namespace *content* deletion (covered) — but namespace *removal* itself is
  not (see 4b).** The namespace controller is registered in the same command
  (`controllermanager.go:1087`) and uses its own `NamespacedResourcesDeleter`
  (`internal/controllers/namespace/namespace_controller.go:90`,
  `:177`), which issues `DeleteCollection` across discovered namespaced
  resources once a namespace enters Terminating. That deleter does **not**
  depend on the GC graph builder. However, it only runs after the
  `organization-<name>` namespace is actually deleted — and that deletion is
  GC-driven, not controller-driven (resolved open question, see 4b). With GC
  off the namespace never enters Terminating, so the content deleter never
  fires and the entire org-namespace subtree leaks. The Organization cascade is
  therefore **not** covered without GC.

- **Project teardown.** The project controller uses an explicit finalizer
  (`projectFinalizer`,
  `internal/controllers/resourcemanager/project_controller.go:34`,
  `:97-133`) and a `projectpurge.Purger` that runs `DeleteCollection` across
  resources (`project_controller.go:62-63`, `:171`). Project deletion does not
  rely on the embedded GC.

- **Several IAM cleanups are explicit.** The Group controller deletes
  GroupMemberships and PolicyBindings via a finalizer
  (`internal/controllers/iam/group_controller.go:49-153`). The
  OrganizationMembership controller explicitly deletes undesired PolicyBindings
  (`organization_membership_controller.go:323-326`). The User controller
  explicitly deletes OrganizationMemberships on user deletion via a finalizer
  (`internal/controllers/iam/user_controller.go:69-80`, `cleanupOrganizationMemberships`).

### 4b. Relies on the embedded GC (the risk)

- **Organization → namespace removal (HIGHEST severity; resolves the 4a open
  question).** The Organization controller has **no finalizer and no explicit
  namespace delete**. On deletion it returns immediately
  (`internal/controllers/resourcemanager/organization_controller.go:38-39`); its
  only namespace action is *setting* the owner reference (`:67`). Namespace
  removal is thus driven solely by owner-reference GC. With GC off, deleting an
  Organization leaves the `organization-<name>` namespace orphaned, the
  namespace never enters Terminating, and the namespace controller's content
  deleter never runs — so **all namespace-scoped org contents leak**, not just
  the namespace object. This is the most damaging path because it cascades.

- **User-owned PolicyBinding / UserPreference.** The User controller *sets*
  owner references on three objects (`internal/controllers/iam/user_controller.go:198-258`)
  but the deletion finalizer cleans up **only OrganizationMemberships**
  (`:70-77`, `cleanupOrganizationMemberships`). The three owned objects rely on
  GC and would orphan on user deletion:
  1. the self-manage `PolicyBinding` (`:210-224`),
  2. the `UserPreference` (`:229-241`),
  3. the `userpreference-self-manage-<user>` PolicyBinding in `milo-system`
     (`:245-258`).

- **Cross-cluster downstream resources (anchor ConfigMaps).** The downstream
  client strategy explicitly relies on in-cluster owner-reference GC to cascade
  deletion. `MappedNamespaceResourceStrategy.SetControllerReference` creates an
  anchor ConfigMap and points the controlled object's owner reference at it
  (`pkg/downstreamclient/mappednamespace.go:105-171`); `DeleteAnchorForObject`
  deletes the anchor and *relies on GC to cascade* to dependents
  (`:180-199`, and the comment at `:108-113`). If this strategy is in use within
  the core-scope cluster, disabling GC breaks that cascade.

  Partial resolution: a grep of `internal/controllers/` finds **no** core
  controller using `MappedNamespaceResourceStrategy` — the only
  `SetControllerReference` calls in core controllers are native owner refs
  (organization namespace, membership PolicyBindings), not downstream anchors.
  This path is therefore *likely* project/infra-scope only and out of scope for
  core. Still confirm before disabling that no core-cluster object uses the
  anchor strategy.

- **OrganizationMembership-owned PolicyBindings.** The membership controller
  (`internal/controllers/resourcemanager/organization_membership_controller.go`)
  sets controller owner references on PolicyBindings — both the role bindings
  (`:444`) and the self-delete PolicyBinding (`:544`). It has **no deletion
  finalizer**; it only deletes *no-longer-desired* bindings during steady-state
  reconcile (`:323-326`). Full cleanup on membership deletion therefore relies
  on owner-reference GC. Disabling GC would orphan the owned PolicyBindings.

### Net risk statement

Verification against the current code surface is worse than the initial draft
assumed. **Project teardown is covered** (explicit finalizer + purger). But
**three owner-reference cascade paths rely on the embedded GC today:**

1. **Organization → namespace** (highest severity — cascades to all namespace
   contents; the org controller has no finalizer/explicit delete),
2. **User → PolicyBinding / UserPreference / userpreference PolicyBinding**
   (three orphaned objects),
3. **OrganizationMembership → owned PolicyBindings** (no deletion finalizer).

A fourth path (downstream anchor ConfigMaps) is *likely* out of scope for the
core cluster but unconfirmed. Disabling the GC controller at core scope today
would leak objects on all three confirmed paths. **Disabling is not safe until
each path is converted to explicit cleanup (Section 7).**

## 5. Options

### Option (a): Leave GC enabled (status quo)

No change. Correct cascade behavior preserved. Pays the full monitor cost in
both scopes. This is the safe default until coverage is proven.

### Option (b): Disable at core scope via the existing `--controllers` flag

Operationally disable with `--controllers=*,-garbagecollector` on the
core-scope deployment manifest only. No code change; reversible by editing the
flag. The gate at `controllermanager.go:1147-1148` ensures this also skips
construction of the dependency graph builder, so the monitors and their
goroutines/heap are never created.

Prerequisite: all Section 4b paths must first be confirmed either out of scope
for the core cluster or converted to explicit cleanup (finalizers / owner
controllers). Otherwise this option leaks orphaned objects.

### Option (c): Default-disable in code at core scope

Add scope-conditional logic so that when `opts.ControlPlane.Scope ==
controlplane.ScopeCore` the GC controller is excluded from the descriptor set
(or the graph builder construction is skipped). The scope branch already exists
at `controllermanager.go:427`, so the hook point is available.

This bakes the behavior into the binary and removes the need for per-deployment
flags, but it is the highest-commitment option and requires the same coverage
proof as Option (b), plus tests. Recommend only after Option (b) has been
validated in staging.

## 6. Recommendation and validation plan

Recommendation: **do not disable yet.** First land the Section 7 cleanup changes
(Changes 1–3 are required; 4–5 are verifications), then measure the cost and
prove cascade coverage using Option (b) as the low-risk experimental lever.
Treat Option (c) as a later step contingent on results.

### Step 1 — Measure the cost (no code change)

In staging, run two core-scope controller-managers with identical config except
one carries `--controllers=*,-garbagecollector`. With a representative project
count, record and compare:

- `go_goroutines`
- `go_memstats_stack_inuse_bytes`
- `go_memstats_heap_inuse_bytes`
- container working-set bytes

This quantifies the GC controller's attributable share of the #631 figures and
confirms whether the saving justifies the effort.

### Step 2 — Prove cascade coverage

With GC disabled in a staging core cluster, exercise each deletion path and
verify no orphans remain:

1. Delete an Organization; confirm its `organization-<name>` namespace is
   deleted and namespace-scoped contents are purged by the namespace controller.
   (Expected to FAIL with GC off until Change 1 in Section 7 lands — namespace
   removal is GC-driven, confirmed in 4b.)
2. Delete a Project; confirm finalizer + purger remove project resources and the
   ProjectControlPlane in the infra cluster.
3. Delete a User; confirm owned PolicyBinding and UserPreference objects are
   removed. (Expected to FAIL with GC off given 4b — this is the gap to close
   before disabling.)
4. Delete an OrganizationMembership; confirm all owned PolicyBindings are gone.
5. If downstream anchors exist in the core cluster, delete an upstream owner and
   confirm anchor + dependents are removed.

### Step 3 — Close gaps, then choose option

For each path that fails in Step 2, add explicit cleanup (finalizer or
owner-controller delete) so it no longer depends on the embedded GC. Once all
paths pass with GC disabled:

- Adopt Option (b) on the core-scope deployment, or
- Promote to Option (c) with scope-conditional gating and unit/e2e tests.

### Open questions to resolve during validation

- ~~Is namespace deletion (parent Organization → namespace) GC-driven or
  controller-driven?~~ **Resolved: GC-driven** (4b, Change 1).
- ~~Does OrganizationMembership deletion explicitly remove all owned
  PolicyBindings, or rely on GC?~~ **Resolved: relies on GC** (4b, Change 3).
- Do `downstreamclient` anchors exist in the core cluster, or only in
  project/infra clusters at a different scope? (Likely out of scope — no core
  controller uses the anchor strategy — but confirm.)
- Are there other resources in the core API surface that set owner references
  without a corresponding explicit-delete controller? (Change 5.)

## 7. Changes required before disabling

Each confirmed GC-reliant path must convert from owner-reference GC to explicit
finalizer-driven cleanup. Ordered by severity:

### Change 1 — Organization finalizer + explicit namespace delete (required)

- Add a finalizer (e.g. `resourcemanager.miloapis.com/organization-namespace-cleanup`)
  to Organization in
  `internal/controllers/resourcemanager/organization_controller.go`.
- On `DeletionTimestamp != 0`, explicitly `Delete` the `organization-<name>`
  namespace (idempotent; ignore `NotFound`), then remove the finalizer. The
  existing namespace controller content-deleter chain then runs unchanged.
- Extend RBAC: the controller currently has only `get;list;watch;update;patch`
  on `namespaces` (`:24`); add `delete`.

### Change 2 — User finalizer deletes owned IAM objects (required)

- In the User deletion branch (`user_controller.go:70-77`), explicitly `Delete`
  the three owned objects before removing the finalizer (ignore `NotFound`):
  1. self-manage PolicyBinding,
  2. UserPreference,
  3. `userpreference-self-manage-<user>` PolicyBinding in `milo-system`.
- Reuse the name builders already present in `ensureOwnerReferences`
  (`:210-247`).

### Change 3 — OrganizationMembership deletion finalizer (required)

- Add a deletion finalizer to the membership controller that lists and deletes
  **all** PolicyBindings owned by the membership (owner-ref/label selector), not
  just the no-longer-desired ones (`:323-326`). Covers both the role bindings
  (`:444`) and the self-delete binding (`:544`).

### Change 4 — Confirm downstream anchors out of core scope (verify)

- Confirm no core-cluster object uses `MappedNamespaceResourceStrategy`. If
  confirmed, no code change. If any core object uses anchors, add explicit
  anchor+dependent deletion or keep GC enabled.

### Change 5 — Owner-reference sweep (verify)

- Audit the remaining core API surface for `SetControllerReference` /
  `SetOwnerReference` calls lacking a matching explicit-delete controller. Each
  is a latent leak under GC-off; convert or document as covered.

After Changes 1–3 land and 4–5 are confirmed, re-run the Section 6 Step 2
validation with GC disabled; all paths must pass before adopting Option (b) or
(c).
