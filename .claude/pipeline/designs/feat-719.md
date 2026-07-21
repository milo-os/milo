---
id: feat-719
title: Block New Work in a Suspended Project (Admission Enforcement)
status: draft
created: 2026-07-20
author: architect
---

# Block New Work in a Suspended Project (Admission Enforcement)

## Overview

When a `Project` is suspended (`Project.Status.Conditions[type=Suspended].status
== "True"`, propagated by `ProjectSuspensionPropagatorController`), the
project's virtual control plane must reject writes with a clear, typed reason.
Reads must continue to work so operators, billing systems, and the affected
customer can still inspect state.

This design adds a new in-process admission plugin,
`ProjectSuspensionEnforcement`, modeled directly on the two admission plugins
that already solve this exact "per-request, resolve project, check
authoritative state" problem in this codebase:

- `internal/apiserver/admission/plugin/namespace/lifecycle/admission.go`
  (`ProjectNamespaceLifecycle`)
- `pkg/quota/admission/plugin.go` (`ResourceQuotaEnforcement`)

No marker projection into project control planes, no webhook, and no
`ValidatingAdmissionPolicy` are used. `ValidatingAdmissionPolicy` was
evaluated and rejected: `paramRef` binds a single Project (or a static
selector) per binding, and there is no way to make a global VAP binding
resolve "which Project does this specific request belong to" dynamically
against the cluster-scoped `Project` object. This decision is final for this
design; do not re-evaluate VAP during implementation.

## Requirements

### Functional Requirements
- FR1: Any Create or Update request routed through a project's virtual
  control plane (`.../projects/<id>/control-plane/...`) must be denied with
  HTTP 403 when that project's `Suspended` condition is `True`.
- FR2: Read requests (Get, List, Watch) are unaffected. (Admission plugins
  never see read verbs, so this is satisfied by construction — see
  Security Considerations.)
- FR3: Delete requests are allowed even while suspended, so customers can
  clean up or offboard during a suspension (see Decisions Made).
- FR4: The 403 response carries a typed, machine-readable cause
  (`ProjectSuspendedCause`) distinct from other denial reasons (quota,
  RBAC, namespace terminating) so API clients can render a specific
  "this project is suspended" message.
- FR5: The plugin resolves suspension state from the authoritative source —
  the core `Project.Status` object — with no new projected/mirrored
  resource.
- FR6: A small, fixed set of exemptions keep the platform itself functional
  during suspension (access reviews, superuser/loopback identities).

### Non-Functional Requirements
- NFR1: Added latency per write request should be a single cached or live
  GET against the root apiserver — no watch establishment, no cross-cluster
  hop, consistent with the quota plugin's existing per-request overhead
  budget.
- NFR2: Suspension state must be re-checked with low staleness (target: a
  few seconds) so reinstatement (suspension lifted) unblocks writes quickly
  without requiring an apiserver restart.
- NFR3: The plugin must not require project-scoped credentials or a new
  cross-control-plane client type; it reuses the standard root
  `dynamic.Interface` injected by `initializer.WantsDynamicClient`
  (the same interface the quota plugin implements for its root client).
- NFR4: Always-on in all environments (dev, test, prod) — suspension
  enforcement is a correctness/safety property, not an experimental feature.

## Design

### Resource Types

No new resource types or CRDs. This feature adds:
1. One new admission plugin (Go package, no API surface).
2. One new exported constant (`ProjectSuspendedCause`) in the existing
   `resourcemanager.miloapis.com/v1alpha1` Go package so API clients can
   import it without depending on internal admission plugin code.

| Resource | Group | Storage | Description |
|----------|-------|---------|--------------|
| Project (existing, read-only from this plugin's perspective) | resourcemanager.miloapis.com | etcd (native, root apiserver) | Authoritative suspension state read by the plugin |

### API Definitions

New constant, added to `pkg/apis/resourcemanager/v1alpha1/project_types.go`
alongside the existing `ProjectSuspended*` constants (around line 60):

```go
// ProjectSuspendedCause is the metav1.StatusCause Type set on the
// Details.Causes of the 403 Forbidden response returned by the
// ProjectSuspensionEnforcement admission plugin when a write is denied
// because the request's target project is suspended. API clients match on
// this value (via apierrors.StatusError.ErrStatus.Details.Causes) to
// distinguish suspension-caused denials from other admission failures
// (quota, RBAC, NamespaceLifecycle's NamespaceTerminatingCause).
const ProjectSuspendedCause metav1.CauseType = "ProjectSuspended"
```

New package `internal/apiserver/admission/plugin/projectsuspension`:

`internal/apiserver/admission/plugin/projectsuspension/register.go`
```go
package projectsuspension

const PluginName = "ProjectSuspensionEnforcement"

func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(_ io.Reader) (admission.Interface, error) {
		return NewPlugin()
	})
}
```

`internal/apiserver/admission/plugin/projectsuspension/admission.go`
```go
package projectsuspension

// Plugin denies Create/Update requests targeting a suspended project's
// virtual control plane. It resolves the project from request context
// (set by pkg/server/filters/projects.go's ProjectRouterWithRequestInfo)
// and checks the authoritative core Project object's Suspended condition.
type Plugin struct {
	*admission.Handler

	dynamicClient dynamic.Interface // root-scope client, injected via WantsDynamicClient
	cache         *utilcache.LRUExpireCache // key: projectID -> *suspensionState
	cacheTTL      time.Duration             // default 2s
	logger        logr.Logger
}

var (
	projectGVR = schema.GroupVersionResource{
		Group:    "resourcemanager.miloapis.com",
		Version:  "v1alpha1",
		Resource: "projects",
	}
	_ initializer.WantsDynamicClient      = &Plugin{}
	_ admission.ValidationInterface       = &Plugin{}
	_ admission.InitializationValidator   = &Plugin{}
)

func NewPlugin() (*Plugin, error)
func (p *Plugin) SetDynamicClient(c dynamic.Interface)
func (p *Plugin) ValidateInitialization() error
func (p *Plugin) Validate(ctx context.Context, attrs admission.Attributes, _ admission.ObjectInterfaces) error
func (p *Plugin) getSuspensionState(ctx context.Context, projectID string) (*suspensionState, error)

type suspensionState struct {
	Suspended   bool
	Suspensions []resourcemanagerv1alpha1.ProjectSuspensionInfo
}

func isExempt(ctx context.Context, attrs admission.Attributes) bool
func newSuspendedForbiddenError(attrs admission.Attributes, projectID string, s *suspensionState) error
```

### Storage Design

No new storage. The plugin resolves suspension state as follows, matching
the "live Get against root, short-TTL in-memory cache" pattern already used
by `NamespaceLifecycle`'s `forceLiveLookupCache`:

1. `milorequest.ProjectID(ctx)` — if absent or empty, this request is not
   scoped to a project control plane (it's a root-cluster request, e.g. CRUD
   on `Project`/`ProjectSuspension` themselves, or an org-scoped request).
   Return `nil` (allow) immediately. This mirrors how the quota plugin and
   `NamespaceLifecycle` both no-op outside project context for the
   resources they don't care about.
2. Check `p.cache` (a `utilcache.NewLRUExpireCache(...)`, same type
   `NamespaceLifecycle` already imports) for a fresh entry keyed by
   `projectID`. TTL default `2 * time.Second` — long enough to absorb
   request bursts, short enough that reinstatement (Suspended flips to
   False) is picked up well within an interactive retry window.
3. On cache miss, `Get` the `Project` object by name via
   `p.dynamicClient.Resource(projectGVR).Get(ctx, projectID,
   metav1.GetOptions{})` (cluster-scoped — no `.Namespace()` call).
   `p.dynamicClient` is the standard root dynamic client injected by the
   built-in `admission.PluginInitializers` `WantsDynamicClient` initializer
   — the exact same injection point the quota plugin
   (`pkg/quota/admission/plugin.go:131` `SetDynamicClient`) already uses,
   so no new wiring is required in `cmd/milo/apiserver/config.go`.
4. Extract `status.conditions[type=Suspended]` via
   `unstructured.NestedSlice` (same idiom as
   `pkg/quota/admission/plugin.go`'s `classifyGrantedCondition`) and
   `status.suspensions` via `runtime.DefaultUnstructuredConverter`. Cache
   and return the result.
5. `Project.NotFound` → allow (fail open on a missing project is
   consistent with `NamespaceLifecycle`, which also allows through on a
   404; a project that doesn't exist can't be "suspended", and some other
   layer — RBAC, routing — will reject the request anyway).
   Any other error → fail closed? See Decisions Made — recommend fail
   *open* with a warning + metric, matching the quota plugin's philosophy
   of not letting infrastructure errors in an unrelated subsystem block
   all traffic; suspension enforcement is a policy safety net, not a
   security boundary.

This plugin deliberately does **not** implement `SetLoopbackConfig` /
`WantsLoopbackConfig` and does **not** build a project-scoped client the way
`NamespaceLifecycle` and the quota plugin do. Those two plugins need a
project-scoped client because they inspect resources that live *inside* the
project's control plane (Namespaces, ResourceClaims). This plugin inspects
the *root*-scoped `Project` object, so the plain root `dynamicClient` is
sufficient regardless of which project's control plane the inbound request
is for.

### Event Processing

Not applicable — this is a synchronous admission check, not an
event-driven flow. No change to `ProjectSuspensionPropagatorController`.

### Which Operations Are Allowed vs Denied

| Operation | Suspended project | Rationale |
|---|---|---|
| Get / List / Watch | Allowed | Admission plugins are never invoked for read verbs; nothing to implement. |
| Create | Denied | Canonical "new work". |
| Update (including subresources, e.g. `/status`) | Denied | A spec update or a controller-driven status update both represent the project continuing to do work while frozen. See Open Questions for the one caveat (internal reconciler status writes). |
| Delete | **Allowed** (recommended default — see Decisions Made) | Lets customers/operators clean up or offboard without waiting for reinstatement; matches `NamespaceLifecycle`'s existing "always allow delete of other resources" rule. |
| Connect (e.g. exec/attach/proxy subresources) | Not handled by this plugin | Out of scope; register the `admission.Handler` for `Create, Update` only, matching the "writes" framing in the acceptance criteria. If a future need to block `exec`/`attach` into suspended-project workloads arises, add `admission.Connect` to the handler in a follow-up — flagged as a non-blocking open question below. |

`Plugin` is constructed with
`admission.NewHandler(admission.Create, admission.Update)` — no `Delete`
operation, so Delete requests never reach `Validate` at all (the generic
admission chain skips plugins not registered for the observed operation).
This is simpler and cheaper than accepting Delete and explicitly allowing
it, and it self-documents the decision at the type declaration.

### Exemptions

Checked by `isExempt` before any Project lookup, in this order (cheapest
checks first):

1. **Access review passthrough.** Reuse the same
   `accessReviewResources` allow-list pattern from
   `internal/apiserver/admission/plugin/namespace/lifecycle/admission.go`
   (`SubjectAccessReview`/`LocalSubjectAccessReview`) so authorization
   checks never leak or depend on project suspension state.
2. **Superuser / loopback identities.** Requests whose
   `attrs.GetUserInfo().GetGroups()` contains `system:masters` are exempt.
   This is the identity used by the apiserver's own loopback client — the
   same client `internal/apiserver/storage/project/mux.go`'s
   `bootstrapMiloSystemNamespace` uses to create the `milo-system`
   namespace on first access to a project's control plane storage. Without
   this exemption, that bootstrap path (and any other privileged
   in-process automation, e.g. future migration jobs) could be denied by
   this plugin. This mirrors the general Kubernetes convention that
   `system:masters` bypasses admission-time policy checks (it already
   bypasses RBAC).
3. **Requests without a project in context** (`ProjectID(ctx)` unset) are
   handled earlier as a fast no-op, not as an "exemption" per se — they're
   simply out of scope for this plugin (root-cluster Organization/Project/
   ProjectSuspension CRUD, e.g. the suspension lifecycle machinery itself
   writing `ProjectSuspension` objects or the propagator controller
   patching `Project.Status`, both happen at root scope and are therefore
   never subject to this plugin).

Everything else inside a suspended project's control plane — including
writes to the project's own `milo-system` namespace by a non-superuser
identity — is denied. This is intentionally narrow; broadening exemptions
(e.g. exempting `milo-system` by namespace name regardless of identity) is
called out as an open, non-blocking question below rather than designed in
preemptively.

### Typed Deny Reason

Denial uses the standard `admission.NewForbidden` (403 Forbidden,
`metav1.StatusReasonForbidden`) — consistent with `NamespaceLifecycle` and
the quota plugin, so existing client-side "is this a Forbidden" handling
keeps working. The distinguishing signal is a `metav1.StatusCause` appended
to `Details.Causes`, exactly like `NamespaceLifecycle` does with
`v1.NamespaceTerminatingCause`:

```go
func newSuspendedForbiddenError(attrs admission.Attributes, projectID string, s *suspensionState) error {
	reasons := uniqueSortedReasons(s.Suspensions) // []string, e.g. ["Billing", "Fraud"]
	msg := fmt.Sprintf(
		"project %q is suspended (%s) and cannot accept new writes while suspended",
		projectID, strings.Join(reasons, ", "),
	)
	err := admission.NewForbidden(attrs, errors.New(msg))
	if apiErr, ok := err.(*apierrors.StatusError); ok {
		apiErr.ErrStatus.Details.Causes = append(apiErr.ErrStatus.Details.Causes, metav1.StatusCause{
			Type:    resourcemanagerv1alpha1.ProjectSuspendedCause, // "ProjectSuspended"
			Message: msg,
			Field:   "",
		})
	}
	return err
}
```

Recommendation on cause granularity (per the open design question): **one
generic cause type (`ProjectSuspendedCause`) plus the underlying
`ProjectSuspensionReason` enum value(s) embedded in the message**, not one
cause type per reason. Rationale: clients only need to answer "was this
denied because the project is suspended?" (one boolean check on
`Details.Causes[].Type`); the *why* (Fraud, Billing, Abuse, Compliance,
Administrative) is presentation detail that belongs in the message, and a
project can have multiple simultaneous active suspensions with different
reasons (see `ProjectSuspensionPropagatorController`'s aggregation logic),
so a single cause naturally accommodates one-or-many reasons without an
N-cause-type explosion.

### Platform Capability Integrations

| Capability | Integration Point | Details |
|------------|-------------------|---------|
| IAM | None required | Enforcement is based on Project status, not a permission model; `system:masters` exemption reuses an existing IAM/RBAC convention, not a new one. |
| Quota | Ordering only | This plugin must run **before** `ResourceQuotaEnforcement` in the admission chain so a denied-for-suspension request never triggers `ResourceClaim` creation. See Rollout/Config. |
| Activity | Not integrated in this design (future consideration) | Emitting an Activity event on every denied write is not required by the acceptance criteria and would add per-denial write load; if product wants an audit trail of "blocked write attempts," add it later as a metric-driven decision, not by default. |

### Security Considerations

- This is a **defense-in-depth / product-correctness** control, not a
  primary authorization boundary — RBAC/IAM still gates *who* can act; this
  plugin gates *whether the project accepts writes at all* regardless of
  who is asking (other than the narrow `system:masters` exemption).
- Fail-open on transient lookup errors (see Storage Design step 5) is a
  deliberate choice to avoid an outage in one subsystem (root apiserver
  reachability for this plugin's Get) cascading into "no project can be
  written to." This is recorded as a Decision below and should be revisited
  if audit/compliance requirements demand fail-closed instead.
- Reads are never subject to admission review in Kubernetes' admission
  chain (`admission.Interface` is only invoked for
  Create/Update/Delete/Connect), so FR2 ("reads always allowed") requires
  no code — it is a structural guarantee, not a behavior this plugin
  implements. State this explicitly in code comments so a future reader
  doesn't "fix" a perceived gap.
- The `system:masters` exemption is intentionally broad (matches upstream
  Kubernetes convention) rather than a narrower "just the loopback client"
  check, because there is no existing narrower identity marker in this
  codebase for "trusted in-process automation." If a narrower marker is
  introduced later (e.g. a dedicated service account or extra key), this
  plugin's `isExempt` is the single place to update.

## Decisions Made (recorded here and in Handoff)

- **DELETE is allowed during suspension.** The acceptance criteria says
  "block new work," not "freeze the project." Blocking deletes would
  prevent legitimate cleanup/offboarding and would contradict the
  `ReinstateAuthority: Consumer` suspension category, where the affected
  customer may need to reduce footprint to get reinstated. Implemented by
  simply not registering the plugin for `admission.Delete`.
- **Fail-open on Project-lookup errors other than NotFound.** Treat
  suspension enforcement as best-effort policy, not a hard security gate;
  see Security Considerations.
- **Single generic `ProjectSuspendedCause`, not one per
  `ProjectSuspensionReason`.** See Typed Deny Reason above.
- **No project-scoped client / `SetLoopbackConfig` needed.** The plugin
  only ever reads the root-scoped `Project` object, so the plain injected
  `dynamic.Interface` (root) suffices — unlike `NamespaceLifecycle` and the
  quota plugin, which both need project-scoped clients because they inspect
  in-project resources.
- **Always-on, no feature gate.** Matches `NamespaceLifecycle`'s posture
  (unconditionally in `defaultOnPlugins` in
  `cmd/milo/apiserver/admission.go`), not a flag-gated rollout. Suspension
  enforcement has no meaningful "off" state that isn't just "don't suspend
  projects," which is already controlled by whether a `ProjectSuspension`
  exists.

## Open Questions

- **Non-blocking:** Should status-subresource updates from *internal*
  reconcilers running against a suspended project's own control plane
  (e.g. a workload controller updating its own object's `.status` in
  reaction to something that happened before suspension) be exempted? This
  design blocks them by default (simplest, matches "no new work" most
  literally). If chainsaw e2e testing (see Test Plan) surfaces reconcilers
  that get stuck retrying status patches against a suspended project in a
  way that's operationally noisy, add a narrower exemption then (e.g. by
  extra key or a dedicated identity) rather than designing it in
  speculatively now.
- **Non-blocking:** Should `admission.Connect` (`exec`/`attach`/`proxy`
  subresources) also be blocked for suspended projects? Not handled by
  this plugin; flagged for a follow-up if a concrete need arises.
- **Non-blocking:** Should a denied-write metric (counter, labeled by
  project and suspension reason) be added for operability, mirroring the
  quota plugin's `admissionResultTotal`? Recommended but not required for
  FR completion; api-dev should add `milo_projectsuspension_admission_denied_total`
  if time allows, following the exact `metrics.NewCounterVec` +
  `legacyregistry.MustRegister` pattern in `pkg/quota/admission/plugin.go`
  lines 45-74.

## Implementation Plan

1. Add `ProjectSuspendedCause` constant to
   `pkg/apis/resourcemanager/v1alpha1/project_types.go` (near the existing
   `ProjectSuspended*` constants, ~line 60). Run `task generate` if any
   generator touches constants (likely not needed for a plain const, but
   confirm no drift in generated docs via `task generate:docs`).
2. Create `internal/apiserver/admission/plugin/projectsuspension/register.go`
   with `PluginName = "ProjectSuspensionEnforcement"` and `Register(plugins
   *admission.Plugins)`, following
   `pkg/quota/admission/register.go`'s structure (no readiness check needed
   here — this plugin has no async cache to sync, unlike the quota
   plugin's `resourceTypeValidator`).
3. Create
   `internal/apiserver/admission/plugin/projectsuspension/admission.go`
   implementing `Plugin` per the API Definitions section: `NewPlugin`,
   `SetDynamicClient` (satisfies `initializer.WantsDynamicClient`, the
   built-in k8s admission initializer interface — no new initializer
   plumbing needed in `cmd/milo/apiserver/config.go`), `ValidateInitialization`,
   `Validate`, `getSuspensionState`, `isExempt`,
   `newSuspendedForbiddenError`, `uniqueSortedReasons`.
   - Use `utilcache.NewLRUExpireCache(...)` (same import as
     `internal/apiserver/admission/plugin/namespace/lifecycle/admission.go:23`)
     for the suspension-state cache, TTL 2s, capacity e.g. 1024 (generous
     relative to expected concurrently-active project count).
   - Reach `status.conditions` via `unstructured.NestedSlice` +
     manual field extraction (same idiom as
     `pkg/quota/admission/plugin.go`'s `classifyGrantedCondition`, lines
     1033-1061) rather than a full typed conversion, to avoid coupling to
     `Project`'s exact JSON shape beyond `type`/`status`/`reason`. For
     `status.suspensions`, a full typed conversion via
     `runtime.DefaultUnstructuredConverter.FromUnstructured` into
     `[]resourcemanagerv1alpha1.ProjectSuspensionInfo` is fine and simpler
     since that field's use is just to render reasons in the message.
4. Wire registration:
   - `cmd/milo/apiserver/server.go`: add
     `projectsuspension.Register(s.Admission.GenericAdmission.Plugins)`
     next to the existing `admissionquota.Register(...)` call (line 219).
   - `cmd/milo/apiserver/admission.go`:
     - In `GetMiloOrderedPlugins()`, insert
       `plugins = append(plugins, projectsuspension.PluginName)` **before**
       the existing `plugins = append(plugins, admissionquota.PluginName)`
       line (currently line 26), so suspension enforcement runs before
       quota's `ResourceClaim` creation in the same
       `ValidatingAdmissionPolicy`-adjacent insertion point.
     - Add `projectsuspension.PluginName` to the `MiloAdmissionPlugins`
       set (line 35-38) so it's default-on via the existing
       `DefaultOffAdmissionPlugins()` computation — no separate
       `RecommendedPluginOrder`/`EnablePlugins` append needed (that
       ad hoc pattern is only used for `NamespaceLifecycle`; prefer the
       cleaner quota-style wiring for this new plugin).
5. Unit tests: `internal/apiserver/admission/plugin/projectsuspension/admission_test.go`.
6. Extend the existing chainsaw suite
   `test/resource-management/project-suspension-lifecycle/` with an
   admission-enforcement step (see Test Plan) rather than creating a new
   top-level test directory.
7. Run `task test:unit` and `task test:end-to-end` (scoped to the
   `project-suspension-lifecycle` chainsaw test) before handoff to
   code-reviewer.

## Test Plan

### Unit tests (`admission_test.go`)

Follow `pkg/quota/admission/plugin_test.go`'s style (table-driven,
`fake` dynamic client via `k8s.io/client-go/dynamic/fake`) rather than
`lifecycle`'s narrower `pathPrefixRT`-focused tests, since this plugin's
core logic (project lookup + condition parsing + exemptions) is what needs
coverage, not HTTP transport rewriting (this plugin has none).

Cases to cover:
- Project not suspended → `Validate` returns `nil` for Create and Update.
- Project suspended, single active suspension (`Fraud`) → Create denied
  with `apierrors.IsForbidden(err) == true` and a `ProjectSuspendedCause`
  present in `Details.Causes`, message contains `"Fraud"`.
- Project suspended, multiple active suspensions → message lists all
  reasons, sorted/deterministic (matches
  `ProjectSuspensionPropagatorController`'s existing sort behavior so
  tests aren't flaky).
- Project suspended → Delete request is never routed to `Validate` at all
  (verify via the `admission.Handler`'s `Handles(admission.Delete) ==
  false`, not by asserting `Validate` returns nil for a Delete attrs,
  since the plugin never registers for Delete).
- No project in context (`ProjectID` unset) → `nil` immediately, no client
  call (assert via a dynamic fake client with a `ReactionFunc` that fails
  the test if invoked).
- Project not found (404 from fake client) → `nil` (fail open).
- Project lookup returns a non-404 error → `nil` (fail open) — assert this
  explicitly since it's a deliberate, easily-regressed choice.
- `system:masters` group in `UserInfo` → `nil` even when project is
  suspended.
- `SubjectAccessReview`/`LocalSubjectAccessReview` resource → `nil` even
  when project is suspended.
- Cache behavior: two Validate calls within the TTL window for the same
  suspended project hit the fake client's Get exactly once (assert call
  count), and a call after TTL expiry hits it again.

### Chainsaw e2e

Yes, extend the existing suite — do not create a parallel one. Add steps
to `test/resource-management/project-suspension-lifecycle/chainsaw-test.yaml`
(which already provisions a `Project`, applies `ProjectSuspension`
resources, and asserts `Project.Status` propagation per commit
`d77edbbd`/`7bf28745`) with:
- After a suspension is active and `Project.Status.Conditions[Suspended]`
  is `True` (already asserted by the existing suite), attempt a Create
  against the project's control plane (e.g. a `Namespace` or any
  lightweight namespaced resource already used elsewhere in this suite)
  and assert the response is `403` with reason `Forbidden` — chainsaw's
  `(error != null)` + `kubectl get --raw` or a `script` step checking
  `.status.details.causes[].reason == "ProjectSuspended"` on the returned
  `Status` object (chainsaw can capture command output/exit code from a
  `kubectl` invocation piped through `jq`).
- After the suspension is lifted (existing suite already exercises
  lifting), assert the same Create now succeeds — this is the
  reinstatement-latency check (NFR2) validated end-to-end rather than only
  via the unit-test TTL assertion.
- Assert a Delete of an existing resource in the project's control plane
  succeeds while still suspended, to lock in the FR3 decision as a
  regression test.

## Rollout/Config

Always-on, unconditional — no feature gate. Justification: matches
`NamespaceLifecycle`'s posture (also unconditional, `defaultOnPlugins` in
`cmd/milo/apiserver/admission.go` line 44-49), and unlike, say, a new
storage backend or experimental API, there's no meaningful reason an
operator would want project suspension enforcement *disabled* while still
using `ProjectSuspension` resources — the two are the same feature from a
product perspective. The existing `DefaultOffAdmissionPlugins()` mechanism
still lets an operator explicitly disable it via
`--disable-admission-plugins=ProjectSuspensionEnforcement` if ever needed
for debugging, same escape hatch every other plugin here already has.

## Handoff

### Decisions Made
- DELETE allowed during suspension (not registering the plugin for the
  Delete operation) — see Decisions Made section above for full rationale.
- Fail-open on any Project-lookup error other than a clean 404.
- Single `ProjectSuspendedCause` cause type; reasons embedded in message
  text, not exploded into per-reason cause types.
- No project-scoped client needed; plain root `dynamic.Interface` via
  `initializer.WantsDynamicClient` is sufficient since this plugin only
  ever reads the root-scoped `Project` object.
- Always-on, no feature gate; wired via the quota-style
  `MiloAdmissionPlugins` set + `GetMiloOrderedPlugins()` insertion, not the
  more ad hoc `NamespaceLifecycle`-style manual `RecommendedPluginOrder`/
  `EnablePlugins` append.
- Plugin ordered before `ResourceQuotaEnforcement` in the admission chain.

### Open Questions
- (Non-blocking) Should internal reconciler status-subresource writes be
  exempted? Default: no, block them; revisit if chainsaw/e2e testing shows
  operational noise.
- (Non-blocking) Should `admission.Connect` (exec/attach/proxy) be blocked
  too? Default: out of scope for this iteration.
- (Non-blocking) Add a `milo_projectsuspension_admission_denied_total`
  metric? Recommended, not required.

### Implementation Notes
- For api-dev: the exact injection point for the root dynamic client is
  the standard Kubernetes `admission.PluginInitializers` built-in
  `WantsDynamicClient` interface (`k8s.io/apiserver/pkg/admission/
  initializer`) — this is **not** the same as this codebase's custom
  `WantsLoopbackConfig` duck-typed initializer
  (`internal/apiserver/admission/initializer/loopback.go`). Confirm by
  checking how `pkg/quota/admission/plugin.go`'s `dynamicClient` field
  (distinct from its separate `loopbackConfig` field) gets set — it's
  through the upstream `controlplaneapiserver.CreateConfig(...,
  pluginInitializers)` machinery already wired in
  `cmd/milo/apiserver/config.go`, no new code needed there.
- For api-dev: double check whether `Project` is served as a CRD or a
  native (built-in Go type) resource in this apiserver before writing the
  unstructured-parsing code — if native, admission still receives
  `*unstructured.Unstructured` for objects served through the *dynamic*
  client Get call used here (this plugin does its own `Get` via
  `dynamicClient`, it does not rely on `attrs.GetObject()`), so this should
  be uniform regardless; the CRD-vs-native distinction in the quota
  plugin's comments is about `attrs.GetObject()` on the *admitted* object,
  which this plugin does not need to inspect at all (it never mutates or
  validates the object being written, only the target project's state).
- For test-engineer: the chainsaw suite directory to extend is
  `test/resource-management/project-suspension-lifecycle/`, not the
  sibling `test/resource-management/project-suspension/` directory (that
  one tests `ProjectSuspension` CRD-level webhook validation, e.g.
  `04-invalid-project-ref.yaml`, and is a different concern).
- For test-engineer: reuse `resourcemanagerv1alpha1.ReasonFraud`,
  `ReasonBilling`, etc. (`pkg/apis/resourcemanager/v1alpha1/
  projectsuspension_types.go` lines 13-17) in test fixtures for consistency
  with the existing suspension-lifecycle chainsaw steps
  (`03-suspension1.yaml`, `04-suspension2.yaml`, `05-suspension3.yaml`).
