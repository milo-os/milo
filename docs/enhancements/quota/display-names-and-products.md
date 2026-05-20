# Quota display names and product grouping

## Overview

The cloud-portal quota page (Project → Quotas) currently renders one row per
`AllowanceBucket`, with the raw `spec.resourceType` string in the "Resource
Type" column — for example `gateway.networking.k8s.io/httproutes` or
`networking.datumapis.com/trafficprotectionpolicies`. There are two
shortcomings:

1. **Unfriendly labels.** Customers see API identifiers rather than the
   product-level name they recognize (e.g. "HTTP Routes"). Support and
   sales repeatedly translate these strings for users.
2. **No grouping.** Resources that together compose a Datum product are
   scattered alphabetically. "AI Edge" is composed of HTTP routes, HTTP
   proxies, gateways, traffic protection policies, and more, but the UI
   gives no signal of that bundle.

Today there is no Milo API surface for either. `ResourceRegistration` has
only a free-text `description` and unit-display fields (`displayUnit`,
`unitConversionFactor`) — nothing for resource display name or product
membership. The portal renders `spec.resourceType` straight through with
no transformation
(`cloud-portal/app/features/quotas/quotas-table.tsx`).

This enhancement introduces:

- A new optional `displayName` field on `ResourceRegistrationSpec` for
  the friendly resource label.
- A new optional `taxonomy` block on `ResourceRegistrationSpec` for
  product-grouping metadata. Quota does not introduce its own
  catalog/product resource — the higher-level Milo **service catalog**
  (in flight separately) is the long-term system of record and will
  populate these taxonomy fields. For now they're hand-authored on
  each registration.
- **GraphQL-layer enrichment** of `AllowanceBucket` in the
  graphql-gateway service. A field resolver looks the registration up
  by `spec.resourceType` (batched per request) and exposes the
  registration's `displayName` and `taxonomy` as additional GraphQL
  fields on the `AllowanceBucket` type. No controller writes to
  `AllowanceBucket.status`; no CRD change to the bucket.
- A cloud-portal change to query AllowanceBuckets through the GraphQL
  gateway (cloud-portal already uses GraphQL for other Milo resources),
  group rows by product display name, and render the friendly resource
  label.
- A staff-portal edit surface for the two new fields on
  `ResourceRegistration`, so platform operators can curate display
  metadata without hand-editing YAML before the service catalog lands.

## Goals

- Provide a stable API surface for resource display names and
  product-grouping taxonomy that survives renames of underlying API
  identifiers.
- Keep `ResourceRegistration` the single source of truth; do not
  denormalize display metadata into `AllowanceBucket.status` via
  controllers.
- Let UIs render the quota page from a single GraphQL query that joins
  buckets to their registration.
- Keep changes backward-compatible: rows still render if `displayName`
  or `taxonomy` is absent — falling back to `resourceType` and an
  "Other" group respectively.
- Stay forward-compatible with the upcoming Milo service catalog —
  the taxonomy block is intentionally simple so the catalog can
  populate it or replace authoring without a CRD-version cut.

## Non-Goals

- A separate product/service catalog CRD inside the quota API group.
  Service-layer modelling belongs in the Milo service catalog, not in
  `quota.miloapis.com`.
- Controller-driven propagation of display metadata into
  `AllowanceBucket.status`. The join happens at read time at the
  GraphQL layer.
- Internationalization / localization of display names.
- Pricing, SKUs, or billing metadata — display only.
- Curated ordering. Portal sorts alphabetically within each group, and
  groups alphabetically by product display name. Explicit ordering
  hints are deferred.
- Marketing assets such as icons or logos. Can be added later as
  optional fields; not part of this proposal.
- Per-tier or per-plan grouping ("Enterprise" vs "Free"). Out of scope.

## API

### `ResourceRegistration` additions

Two new optional fields on `ResourceRegistrationSpec`
([`resourceregistration_types.go`](../../../pkg/apis/quota/v1alpha1/resourceregistration_types.go)):

```go
// DisplayName is the human-readable resource label shown in UIs
// (e.g. "HTTP Routes", "Traffic Protection Policies"). When unset, UIs
// fall back to spec.resourceType.
//
// +kubebuilder:validation:Optional
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=100
DisplayName string `json:"displayName,omitempty"`

// Taxonomy carries display-only categorization metadata used by UIs to
// group resources (e.g. by product). The fields are populated either
// manually on each registration today, or by the Milo service catalog
// once that lands. Quota does not interpret these fields — they are
// pure metadata for catalog-style UIs.
//
// +kubebuilder:validation:Optional
Taxonomy *ResourceTaxonomy `json:"taxonomy,omitempty"`
```

```go
// ResourceTaxonomy describes how a resource should be grouped in
// catalog-style UIs. All fields are display-only.
type ResourceTaxonomy struct {
    // Product is the machine identifier of the product this resource
    // belongs to (e.g. "ai-edge"). Stable across renames of the
    // human-readable label; safe to use as a grouping key.
    //
    // +kubebuilder:validation:Optional
    // +kubebuilder:validation:MinLength=1
    // +kubebuilder:validation:MaxLength=63
    // +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
    Product string `json:"product,omitempty"`

    // ProductDisplayName is the human-readable product name shown in
    // UIs (e.g. "AI Edge"). When unset but Product is set, UIs may
    // title-case Product as a fallback.
    //
    // +kubebuilder:validation:Optional
    // +kubebuilder:validation:MinLength=1
    // +kubebuilder:validation:MaxLength=100
    ProductDisplayName string `json:"productDisplayName,omitempty"`

    // Category is an optional finer-grained grouping within a product
    // (e.g. "Routing", "Protection"). Reserved for future use; UIs may
    // ignore it until a clear use case emerges.
    //
    // +kubebuilder:validation:Optional
    // +kubebuilder:validation:MaxLength=100
    Category string `json:"category,omitempty"`
}
```

Both fields are mutable (unlike `resourceType` and `type`). Edits take
effect on the next GraphQL query.

Example updated registration:

```yaml
apiVersion: quota.miloapis.com/v1alpha1
kind: ResourceRegistration
metadata:
  name: httproutes-per-project
spec:
  consumerType:
    apiGroup: resourcemanager.miloapis.com
    kind: Project
  type: Entity
  resourceType: gateway.networking.k8s.io/httproutes
  displayName: HTTP Routes
  taxonomy:
    product: ai-edge
    productDisplayName: AI Edge
  description: Maximum number of HTTP routes that can be created within a project
  baseUnit: route
  displayUnit: routes
  unitConversionFactor: 1
  claimingResources:
    - apiGroup: gateway.networking.k8s.io
      kind: HTTPRoute
```

### `AllowanceBucket` CRD changes

**None.** No new fields on `AllowanceBucketStatus`; no controller
changes to copy registration metadata into the bucket. The bucket
controller continues to do exactly what it does today.

### Relationship to the Milo service catalog

The Milo service catalog (in flight as a separate enhancement) is the
long-term system of record for service- and product-level metadata.
Once it lands, the expected migration path is:

- Service-catalog entries become the authoring surface for product
  identifiers and display names.
- A small reconciler (or generator) populates `spec.taxonomy` on
  matching `ResourceRegistration` objects from the catalog.
- The CRD field shape stays the same; what changes is who writes it.

Encoding the taxonomy directly on `ResourceRegistration` (rather than
introducing a quota-local `Product` resource) avoids a second catalog
inside the quota API group and avoids a forced migration when the
service catalog lands.

## GraphQL Enrichment

Display metadata is joined to buckets at the GraphQL layer rather than
via controller-driven status propagation. The graphql-gateway already
composes a Milo supergraph dynamically from OpenAPI specs (see
[`graphql-gateway/README.md`](https://github.com/datum-cloud/graphql-gateway))
and exposes `AllowanceBucket` and `ResourceRegistration` as types in
that supergraph.

### Schema extension

Extend the `AllowanceBucket` type with two computed fields that
delegate to the matching `ResourceRegistration`:

```graphql
extend type AllowanceBucket {
  # Resolved from the matching ResourceRegistration's spec.displayName.
  # Falls back to spec.resourceType when unset.
  displayName: String

  # Resolved from the matching ResourceRegistration's spec.taxonomy.
  # Null when the registration has no taxonomy block.
  taxonomy: ResourceTaxonomy
}

type ResourceTaxonomy {
  product: String
  productDisplayName: String
  category: String
}
```

A custom resolver runs against the existing supergraph: when the
`AllowanceBucket.displayName` or `AllowanceBucket.taxonomy` fields are
selected, it issues a `ResourceRegistration` lookup keyed by
`(spec.consumerRef.kind, spec.resourceType)`. Lookups are batched per
request with a DataLoader so a page that renders dozens of buckets
makes one (cached) registration fetch.

### Caching

`ResourceRegistration` objects change rarely. A short in-memory TTL
cache (e.g. 30s) on the resolver is sufficient; consistent across the
duration of a typical portal page render.

### No HTTP / REST changes

Cloud-portal and staff-portal already consume Milo through the GraphQL
gateway for several resource types
(`cloud-portal/app/resources/organizations/organization.gql-*.ts`,
similar for users). This change uses the same path. Direct
Kubernetes-API readers of `AllowanceBucket` continue to see exactly
the same JSON they see today; they simply don't get the enriched
fields.

## Portal Changes

The cloud-portal quota page switches from a direct AllowanceBucket
fetch to a GraphQL query that selects the enriched fields:

- New GraphQL query under `app/resources/allowance-buckets/` modelled
  after the existing `organization.gql-queries.ts` pattern.
- [`app/features/quotas/quotas-table.tsx`](https://github.com/datum-cloud/cloud-portal/blob/main/app/features/quotas/quotas-table.tsx):
  group rows by `taxonomy.productDisplayName` (rendering a section
  header per group), render `displayName` in the Resource Type column,
  falling back to `resourceType` when empty. Buckets with no
  `taxonomy.product` group under "Other", sorted last. When
  `productDisplayName` is empty but `product` is set, title-case
  `product` as the header.
- The existing direct-fetch adapter
  (`app/resources/allowance-buckets/allowance-bucket.adapter.ts`) is
  retired or kept only for paths that don't need the enriched fields.

## Staff Portal Changes

Until the Milo service catalog lands, platform operators need a place
to edit the new display fields without hand-editing YAML. The staff
portal gains a small edit surface on top of `ResourceRegistration`,
using the existing Milo-via-Kubernetes-API pattern (no new HTTP
surface). No "products" admin page is added — there is no `Product`
resource to manage, and the service catalog will eventually own the
authoring experience.

New pages under the staff portal's existing admin section:

- **Resource registrations list.** Lists all `ResourceRegistration`
  objects with their `resourceType`, `displayName`,
  `taxonomy.product`, and `taxonomy.productDisplayName`.
- **Resource registration edit.** Edits only the new mutable display
  fields: `spec.displayName`, `spec.taxonomy.product`,
  `spec.taxonomy.productDisplayName`, and `spec.taxonomy.category`.
  Immutable fields (`resourceType`, `type`, `consumerType`, unit
  fields, `claimingResources`) are shown read-only.

Authorization piggybacks on the existing staff-portal admin role
binding. Server-side validation lives on the CRD; the staff portal
mirrors length / pattern constraints client-side for fast feedback but
does not become the source of truth.

When the service catalog lands and starts populating `spec.taxonomy`
from a separate authoring surface, this staff-portal page becomes a
read-only viewer for those fields, or is removed entirely.

## Verification

End-to-end on staging:

1. Apply an updated `ResourceRegistration` with `displayName` and
   `taxonomy` set. `kubectl get resourceregistration <name> -o yaml`
   — confirm the new fields are accepted.
2. Run a GraphQL query against the gateway selecting
   `AllowanceBucket.displayName` and `AllowanceBucket.taxonomy` for a
   consumer that has the affected resource type. Confirm the response
   mirrors the registration.
3. Clear `spec.taxonomy` on the registration; rerun the query and
   confirm `taxonomy` is `null` (TTL cache aside).
4. Load the portal quota page for a project that has the affected
   buckets. Confirm:
   - Rows are grouped by product display name with a header per group.
   - Each row shows the friendly display name instead of the API
     string.
   - Buckets with no `taxonomy.product` appear in an "Other" group
     sorted last.
   - Buckets with no `displayName` fall back to `resourceType`.
5. Integration test in graphql-gateway covering:
   - Registration → bucket enrichment for both `displayName` and
     `taxonomy`.
   - Missing registration (orphan bucket) yields fallback fields and
     null `taxonomy`, no error.
   - DataLoader batches multiple buckets into a single registration
     lookup per request.
6. From the staff portal admin section: edit a `ResourceRegistration`
   to add `displayName` and `taxonomy`, refresh the consumer-side
   quota page in cloud-portal, and confirm the new group + display
   name appear within one TTL window.

## Rollout

- Land Milo CRD additions (two new optional fields on
  `ResourceRegistrationSpec`) in one PR. No bucket controller changes;
  no `AllowanceBucket` CRD changes.
- Ship the graphql-gateway resolver extension. Once deployed,
  `displayName` and `taxonomy` become queryable on `AllowanceBucket`.
- Update `network-services-operator` registrations to populate
  `displayName` and `taxonomy` in a follow-up PR.
- Staff-portal edit surface ships once the CRD changes are in
  staging.
- Cloud-portal switches the quota page to the new GraphQL query.
  Forward-compatible with registrations that have empty
  `displayName` / `taxonomy` (rows still render via the existing
  fallbacks).
- When the service catalog lands, the authoring source for
  `spec.taxonomy` migrates from hand-edited YAML / staff-portal forms
  to catalog-driven reconciliation. No CRD or GraphQL changes
  required.

## Open Questions

- TTL for the registration cache in the resolver: 30s feels right
  given how rarely registrations change, but worth confirming once
  numbers exist. (Cache miss should be tiny: a single list/get
  against Milo.)
- Should `taxonomy.product` carry any uniqueness guarantee across
  registrations? (Lean: no — it's a grouping key, not an identifier.
  Many registrations sharing the same `product` value is the whole
  point.)
- Should the quota CRDs gain a label like
  `quota.miloapis.com/product=ai-edge` on `ResourceRegistration` for
  selector queries? (Lean: spec field only for now; add a label later
  if a real query path appears.)
- Once the service catalog is live, do we deprecate manual editing of
  `spec.taxonomy` outright, or leave it as a manual override path?
  (Defer to the service catalog enhancement.)

## References

- Existing types:
  [`pkg/apis/quota/v1alpha1/resourceregistration_types.go`](../../../pkg/apis/quota/v1alpha1/resourceregistration_types.go),
  [`pkg/apis/quota/v1alpha1/allowancebucket_types.go`](../../../pkg/apis/quota/v1alpha1/allowancebucket_types.go).
- Example registrations:
  `network-services-operator/config/quota/registrations/*.yaml`.
- GraphQL gateway: `datum-cloud/graphql-gateway` — Hive Gateway with
  dynamic supergraph composition from Milo OpenAPI specs.
- Existing portal GraphQL pattern:
  `cloud-portal/app/resources/organizations/organization.gql-*.ts`.
- Portal table:
  `cloud-portal/app/features/quotas/quotas-table.tsx`.
- Staff portal repo: `datum-cloud/staff-portal` — admin UIs follow the
  same Milo-via-Kubernetes-API pattern used for existing quota admin
  pages.
- Milo service catalog enhancement (in flight) — long-term system of
  record for `spec.taxonomy` authoring.
