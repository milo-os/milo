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

- A new cluster-scoped `Product` CRD under
  `quota.miloapis.com/v1alpha1` that carries product-level display
  metadata.
- New fields on `ResourceRegistrationSpec`: `displayName` (per-resource
  human label) and `productRef` (reference to a `Product`).
- Propagation of `displayName`, `productRef`, and the product display
  name into `AllowanceBucket.status` so the portal renders the quota
  page from a single resource list, without an additional join.
- A portal change to group bucket rows by product display name and
  render the friendly resource label.
- A staff-portal admin surface for managing `Product` objects and
  editing `displayName` / `productRef` on `ResourceRegistration`, so
  GTM and platform operators can curate the quota catalog without
  hand-editing YAML.

## Goals

- Provide a stable API surface for resource display names and product
  grouping that survives renames of underlying API identifiers.
- Let the portal render the quota page from `AllowanceBucket` alone,
  with no extra fetch of `ResourceRegistration` or `Product` from the
  consumer's project control plane.
- Keep changes backward-compatible: rows still render if `displayName`
  or `productRef` is absent — falling back to `resourceType` and an
  "Other" group respectively.
- Allow a resource to belong to at most one product
  (one-product-to-many-resources). Multi-product membership is
  out of scope.

## Non-Goals

- Internationalization / localization of display names.
- Pricing, SKUs, or billing metadata on `Product` — display only.
- Curated ordering. Portal sorts alphabetically within each group, and
  groups alphabetically by product display name. Explicit ordering
  hints are deferred.
- Marketing assets such as icons or logos. Can be added later as
  optional fields; not part of this proposal.
- Per-tier or per-plan grouping ("Enterprise" vs "Free"). Out of scope.

## API

### New CRD: `Product`

Cluster-scoped, lives in `quota.miloapis.com/v1alpha1` alongside the
existing quota types under
[`pkg/apis/quota/v1alpha1/`](../../../pkg/apis/quota/v1alpha1).

```go
// quota/v1alpha1/product_types.go

type ProductSpec struct {
    // DisplayName is the human-readable product name shown in UIs.
    //
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinLength=1
    // +kubebuilder:validation:MaxLength=100
    DisplayName string `json:"displayName"`

    // Description is short explanatory copy shown alongside the product
    // header in the quota UI. Maximum 500 characters.
    //
    // +kubebuilder:validation:Optional
    // +kubebuilder:validation:MaxLength=500
    Description string `json:"description,omitempty"`
}

type ProductStatus struct {
    // ObservedGeneration tracks the spec generation last reconciled.
    ObservedGeneration int64 `json:"observedGeneration,omitempty"`

    // Conditions tracks validation and readiness state.
    Conditions []metav1.Condition `json:"conditions,omitempty"`
}
```

`metadata.name` is the stable identifier referenced from
`ResourceRegistration.spec.productRef.name`. Conventional form is
kebab-case: `ai-edge`, `core-networking`, `compute`.

Example:

```yaml
apiVersion: quota.miloapis.com/v1alpha1
kind: Product
metadata:
  name: ai-edge
spec:
  displayName: AI Edge
  description: HTTP-layer routing and traffic protection for AI workloads.
```

### `ResourceRegistration` additions

Two new optional fields on `ResourceRegistrationSpec`
([`resourceregistration_types.go`](../../../pkg/apis/quota/v1alpha1/resourceregistration_types.go)):

```go
// DisplayName is the human-readable resource label shown in UIs
// (e.g. "HTTP Routes", "Traffic Protection Policies"). When unset, the
// portal falls back to spec.resourceType.
//
// +kubebuilder:validation:Optional
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=100
DisplayName string `json:"displayName,omitempty"`

// ProductRef groups this registration under a Product for display.
// References a Product by name in the same cluster. When unset, the
// resource renders in an "Other" group in the portal.
//
// +kubebuilder:validation:Optional
ProductRef *ProductReference `json:"productRef,omitempty"`
```

```go
type ProductReference struct {
    // Name of the Product resource.
    //
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinLength=1
    Name string `json:"name"`
}
```

Both new fields are mutable (unlike `resourceType` and `type`). Updates
propagate to downstream buckets on the next reconciliation.

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
  productRef:
    name: ai-edge
  description: Maximum number of HTTP routes that can be created within a project
  baseUnit: route
  displayUnit: routes
  unitConversionFactor: 1
  claimingResources:
    - apiGroup: gateway.networking.k8s.io
      kind: HTTPRoute
```

### `AllowanceBucket.status` propagation

The bucket controller already aggregates state from `ResourceRegistration`
(see [`allowancebucket_types.go`](../../../pkg/apis/quota/v1alpha1/allowancebucket_types.go)).
Extend it to copy display metadata into status so the portal can render
the quota page from a single list call:

```go
type AllowanceBucketStatus struct {
    // ... existing fields ...

    // DisplayName mirrors the matching ResourceRegistration.spec.displayName
    // at last reconciliation. Empty if the registration has no display name.
    //
    // +kubebuilder:validation:Optional
    // +kubebuilder:validation:MaxLength=100
    DisplayName string `json:"displayName,omitempty"`

    // Product surfaces product grouping metadata for UI rendering.
    // Nil if the matching ResourceRegistration has no productRef, or if
    // the referenced Product does not exist (a status condition records
    // the dangling reference).
    //
    // +kubebuilder:validation:Optional
    Product *BucketProductInfo `json:"product,omitempty"`
}

type BucketProductInfo struct {
    // Name is the Product.metadata.name reference.
    //
    // +kubebuilder:validation:Required
    Name string `json:"name"`

    // DisplayName mirrors Product.spec.displayName at last reconciliation.
    //
    // +kubebuilder:validation:Required
    DisplayName string `json:"displayName"`
}
```

The bucket controller becomes the single denormalization point: when a
`ResourceRegistration` or `Product` changes, dependent buckets are
requeued and re-reconciled. The portal never needs to GET
`ResourceRegistration` or `Product` directly.

## Controller Behavior

### Reconciliation triggers

The existing bucket reconciler already watches `ResourceRegistration` and
the consumer-side `ResourceGrant` / `ResourceClaim` populations. Two new
watches:

- `Product` change → enqueue all `ResourceRegistration` objects with a
  matching `spec.productRef.name`, which in turn enqueues their
  dependent buckets.
- `ResourceRegistration.spec.displayName` or `spec.productRef` change →
  enqueue dependent buckets (this is already implicit if the reconciler
  reacts to any registration change; otherwise add a predicate).

### Resolution

For each bucket reconciliation:

1. Resolve the `ResourceRegistration` matching `spec.resourceType` and
   `spec.consumerRef.kind` (as today).
2. Copy `registration.spec.displayName` into `status.displayName`.
3. If `registration.spec.productRef` is set, GET the referenced
   `Product`:
   - On success, populate `status.product` with `{name, displayName}`.
   - On NotFound, clear `status.product` and set condition
     `ProductRefResolved=False` with reason `ProductNotFound`. Do not
     fail reconciliation — the bucket should still serve quota.
4. If `registration.spec.productRef` is unset, clear `status.product` and
   set `ProductRefResolved=True` with reason `NoProductRef`.

### Validation

- The new fields on `ResourceRegistration` and `Product` are validated
  via kubebuilder length / required markers — no admission webhook
  changes needed.
- `productRef.name` is **not** validated against existing `Product`
  objects at admission time. Dangling references are tolerated and
  surfaced through bucket status (GTM may create registrations before
  products land).

## Portal Changes

Two files in cloud-portal:

- [`app/resources/allowance-buckets/allowance-bucket.adapter.ts`](https://github.com/datum-cloud/cloud-portal/blob/main/app/resources/allowance-buckets/allowance-bucket.adapter.ts):
  pass through `status.displayName` and `status.product` onto the
  view-model.
- [`app/features/quotas/quotas-table.tsx`](https://github.com/datum-cloud/cloud-portal/blob/main/app/features/quotas/quotas-table.tsx):
  group rows by `status.product.displayName` (rendering a section
  header per group), and render `status.displayName` in the Resource
  Type column, falling back to `spec.resourceType` when empty. Buckets
  with no `status.product` group under "Other", sorted last.

No new API surface in the portal — the existing AllowanceBucket list
endpoint carries everything needed.

## Staff Portal Changes

The catalog of products and per-resource display names is curated by
the GTM and platform teams, so the staff portal gains a small admin
surface that writes directly to the Milo CRDs introduced above. No new
HTTP API is added — the staff portal already speaks to Milo via the
Kubernetes API (see [`feedback_portal_milo_via_k8s_api`] pattern), so
this is a UI on top of existing `quota.miloapis.com/v1alpha1` types.

New pages under the staff portal's existing admin section:

- **Products list.** Lists all `Product` objects with display name,
  description, and count of `ResourceRegistration` objects pointing at
  them. Actions: create, edit, delete.
- **Product detail / edit.** Edits `Product.spec.displayName` and
  `Product.spec.description`. Lists the registrations that reference
  it (read-only summary).
- **Resource registrations list.** Lists all `ResourceRegistration`
  objects with `resourceType`, `displayName`, `productRef`, and the
  bucket count derived from `AllowanceBucket` field selectors.
- **Resource registration edit.** Edits the two mutable display fields:
  `spec.displayName` and `spec.productRef.name` (with a dropdown
  populated from the `Product` list, plus a "None" choice that clears
  the ref). Immutable fields (`resourceType`, `type`, `consumerType`,
  unit fields, `claimingResources`) are shown read-only.

Authorization: gated by the existing staff-portal admin role binding —
the same role that already manages quotas elsewhere in the staff
portal. New `ProtectedResource` / `Role` definitions are not required
because `Product` and `ResourceRegistration` are cluster-scoped quota
types that the platform admin role already grants edit access to.

Validation is server-side via the kubebuilder markers on the CRDs;
the staff portal mirrors the same constraints client-side (length
limits, required fields) for fast feedback but does not become the
source of truth.

[`feedback_portal_milo_via_k8s_api`]: <!-- internal pattern; see staff-portal CLAUDE.md -->

## Verification

End-to-end on staging:

1. Apply a `Product` manifest and one updated `ResourceRegistration`
   referencing it.
2. `kubectl get resourceregistration <name> -o yaml` — confirm
   `displayName` and `productRef` are accepted.
3. Wait for bucket reconciliation. `kubectl get allowancebucket -o yaml`
   for a consumer with that resource type — confirm
   `status.displayName` and `status.product.displayName` are populated.
4. Delete the `Product`; confirm bucket
   `status.conditions[type=ProductRefResolved]=False`, and that the
   `status.product` field is cleared. Bucket continues to serve quota.
5. Load the portal quota page for a project that has the affected
   buckets. Confirm:
   - Rows are grouped by product display name with a header per group.
   - Each row shows the friendly display name instead of the API
     string.
   - Buckets with no `productRef` appear in an "Other" group sorted
     last.
   - Buckets with no `displayName` fall back to `resourceType`.
6. Chainsaw test in milo covering:
   - Registration → product → bucket propagation.
   - Product deletion clears bucket status and sets the condition.
   - Absent `displayName` / `productRef` reconciles to empty fields.
7. From the staff portal admin section: create a `Product`, attach an
   existing `ResourceRegistration` via the edit form, refresh the
   consumer-side quota page in cloud-portal, and confirm the new
   group + display name appear within one reconciliation cycle.

## Rollout

- Land milo CRD changes + bucket controller update in one PR; ship as a
  patch release of the quota CRDs.
- Update `network-services-operator` registrations to add
  `displayName` and `productRef`, and add initial `Product` manifests
  (AI Edge, Core Networking) in a follow-up PR.
- Wire `Product` manifests into the infra deployment via the existing
  quota kustomization. Image-updater handles the cascade.
- Cloud-portal change ships independently and is forward-compatible
  with buckets that have empty `status.displayName` /
  `status.product`.
- Staff-portal admin surface ships once the CRD changes are in
  staging — GTM can curate the catalog before the consumer-facing
  cloud-portal grouping lands, since updates flow through the bucket
  controller regardless of which UI rendered them.

## Open Questions

- Should `productRef` be allowed to point at a `Product` that does not
  yet exist? (Lean: yes, with the `ProductRefResolved=False` condition,
  so GTM can stage registrations before products land.)
- Do we need a `quota.miloapis.com/product` label on
  `ResourceRegistration` for selector queries (e.g. "all registrations
  belonging to AI Edge")? (Lean: spec field only for now; add a label
  later if a real query path appears.)
- Should `Product` carry a `Tier` or similar gating field so that
  free-tier consumers see only free-tier products? (Lean: out of scope
  — this is a display concern; gating lives at the grant level.)

## References

- Existing types:
  [`pkg/apis/quota/v1alpha1/resourceregistration_types.go`](../../../pkg/apis/quota/v1alpha1/resourceregistration_types.go),
  [`pkg/apis/quota/v1alpha1/allowancebucket_types.go`](../../../pkg/apis/quota/v1alpha1/allowancebucket_types.go).
- Example registrations:
  `network-services-operator/config/quota/registrations/*.yaml`.
- Portal table:
  `cloud-portal/app/features/quotas/quotas-table.tsx`.
- Staff portal repo: `datum-cloud/staff-portal` — admin UIs follow the
  same Milo-via-Kubernetes-API pattern used for existing quota admin
  pages.
