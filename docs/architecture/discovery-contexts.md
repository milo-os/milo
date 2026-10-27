# Parent-context-aware API discovery

Milo serves the same API surface to many kinds of callers — humans hitting the
cluster root to manage their organizations, controllers operating inside a
specific organization, project-scoped clients running inside a project's
control plane, and end users reading their own profile. Most of those callers
only care about a small slice of the resources Milo exposes.

This document describes how a resource declares which **parent contexts** it
is visible in, and how Milo filters the standard Kubernetes API discovery
responses (`/apis`, `/apis/{group}/{version}`) per request.

> **Status: prototype.** The filter is implemented; the companion admission
> check that turns this into a hard boundary is not. See
> [Tradeoffs](#tradeoffs).

## Parent contexts

A request enters Milo in one of four parent contexts. The context is
determined by the URL prefix the client used; the existing handlers in
`pkg/server/filters/` extract the parent type and stash it on the request
context.

| Context        | URL pattern                                                                              |
| -------------- | ---------------------------------------------------------------------------------------- |
| `Platform`         | `/apis/...` (no parent prefix)                                                           |
| `Organization` | `/apis/resourcemanager.miloapis.com/v1alpha1/organizations/{id}/control-plane/apis/...`  |
| `Project`      | `.../projects/{id}/control-plane/apis/...`                                               |
| `User`         | `/apis/iam.miloapis.com/v1alpha1/users/{id}/control-plane/apis/...`                      |

The filter consults a registry built from CRD annotations and decides, for
each `(group, resource)` pair, whether to include it in the discovery
response.

## Tagging a resource

### Bundled CRDs (Go-defined types)

Add the `+kubebuilder:metadata:annotations` marker on the type. `task
generate` writes it onto the generated CRD manifest:

```go
// +kubebuilder:resource:path=projects,scope=Cluster
// +kubebuilder:metadata:annotations=discovery.miloapis.com/parent-contexts=Organization
type Project struct { ... }
```

For multiple contexts, quote the marker value. controller-gen's marker
parser treats bare tokens as the legacy slice form (split on `;`) and splits
the annotations list on `,`, so unquoted multi-context values fail code
generation. A quoted value goes through `strconv.Unquote` and is preserved
verbatim:

```go
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization,User"
type OrganizationMembership struct { ... }
```

The runtime parser accepts `,` and `;` interchangeably, so external CRDs
written directly as YAML (no controller-gen in the loop) can use either.

References:
- `sigs.k8s.io/controller-tools/pkg/crd/markers/crd.go:360` — `Metadata.Annotations []string`
- `sigs.k8s.io/controller-tools/pkg/markers/parse.go:376` — `parseString` accepts `"..."` and `` `...` `` via `strconv.Unquote`

### External CRDs (services built on Milo)

External services install their CRDs through the standard apiextensions
endpoint. Tag the CRD object directly — Milo's CRD informer picks up the
annotation within seconds and the discovery filter starts honoring it on the
next request.

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
  annotations:
    discovery.miloapis.com/parent-contexts: "Organization,Project"
spec:
  group: example.com
  names: { kind: Widget, plural: widgets, ... }
```

Comma OR semicolon work in raw CRD YAML — there's no controller-gen in the
loop. The wildcard `*` (or omitting the annotation entirely) means "visible
in all contexts," which is the backwards-compatible default.

### Non-CRD types (built-in, aggregated)

Resources served by aggregated APIs (e.g. `identity.miloapis.com/sessions`,
core/v1) aren't backed by a CRD object, so there is no annotation surface.
Milo's own are listed in `builtinContexts` in `pkg/server/discovery`, and
`discovery.NewRegistry()` registers them, so every process that builds the
registry starts from the same declarations. A new one goes in that list:

```go
{Group: "identity.miloapis.com", Resource: "sessions"}: {ContextUser},
```

A static registration is the lowest-precedence input: a
`DiscoveryContextPolicy` rule or a CRD annotation for the same
`GroupResource` overrides it.

## What the client sees

```console
# Platform context — projects are hidden because they're tagged Organization-only.
$ kubectl api-resources --api-group=resourcemanager.miloapis.com
NAME             SHORTNAMES   APIVERSION                              NAMESPACED   KIND
organizations                 resourcemanager.miloapis.com/v1alpha1   false        Organization

# Organization context — projects show up; organizations themselves are hidden
# because the user already knows which org they're in.
$ kubectl api-resources \
    --server=https://milo/apis/.../organizations/acme/control-plane \
    --api-group=resourcemanager.miloapis.com
NAME                       APIVERSION                              NAMESPACED   KIND
organizationmemberships    resourcemanager.miloapis.com/v1alpha1   true         OrganizationMembership
projects                   resourcemanager.miloapis.com/v1alpha1   false        Project
```

## Tradeoffs

**This is a discovery hint, not enforcement.** A client that already knows
the GVR of a hidden resource can still issue a `GET`/`POST`/`LIST` and the
apiserver will serve it (subject to RBAC). To make the boundary hard, pair
the filter with a validating admission plugin that rejects writes to a
resource whose annotation excludes the current parent context. The two share
the same `Registry` so the rules can never drift.

**Until then, a request for a hidden type is counted.** The API server counts
each resource request in Project context for a type the registry does not
serve in Project, in `projectstorage_unserved_requests_total{verb,group,resource}`,
and serves it as before. A type that only a wildcard rule hides counts under
the rule's patterns, such as `search.miloapis.com` and `*`, so a name a
request chose never becomes a label. It logs each new (user, verb, group,
resource) with the project, at most once an hour, so a caller can be found by
name. The counter runs whatever the `DiscoveryContextFilter` gate says: the API
server builds the registry with the gate off too. It counts nothing until the
registry has synced.

**Backwards compatibility.** Resources without the annotation are visible
everywhere. Existing CRDs (Milo's and third-parties') keep working unchanged
until they opt in.

**Startup window.** During apiserver startup, before the CRD informer has
synced, the filter falls open (everything visible). This avoids hiding
resources during the brief window where the registry is empty.

## The garbage collector reads the same registry

The controller manager builds its own `Registry` from the same CRDs and
`DiscoveryContextPolicy` objects, whatever the `DiscoveryContextFilter` gate
says, and gives it to the garbage collector as the types each project serves.
The feature gate `GarbageCollectorServedTypes` selects what the collector does
with it:

- **Off (the default):** every project watches the root's deletable types, as
  before, and the collector counts, by group and resource, what narrowing would
  drop from the project graphs: `garbagecollector_unserved_objects`,
  `garbagecollector_unserved_owned_objects` and
  `garbagecollector_unserved_owners_with_dependents`. It logs each counted
  object once. A series is absent, not 0, while the collector cannot see.
- **On:** each project watches only the root's deletable types it serves, and
  a dependent whose owner's kind its project does not serve is held, never
  collected as orphaned. Turn it on only when all three gauges read 0.

Unlike the filter, the collector does not fall open: until its registry has
synced, a project keeps the set it has.

## File map

- `pkg/server/discovery/contexts.go` — annotation contract, ParentContext type, request-context detection
- `pkg/server/discovery/registry.go` — CRD-watching registry + static registration
- `pkg/server/discovery/filter.go` — HTTP middleware that captures and filters discovery responses
- `pkg/server/discovery/unserved.go` — the counter of requests for hidden types, and its caller log
- `cmd/milo/apiserver/config.go` — wires the filter into the three handler chains (kube-API, apiextensions, aggregator)
- `cmd/milo/apiserver/server.go` — starts the registry's CRD informer in a post-start hook
- `cmd/milo/controller-manager/servedtypes.go` — builds the garbage collector's registry
- `internal/controllers/garbagecollector/served.go` — each project's monitor set and the owner hold
- `internal/controllers/garbagecollector/unserved.go` — the three gauges and the log line
