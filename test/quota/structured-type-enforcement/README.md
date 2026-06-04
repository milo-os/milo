# Test: `structured-type-enforcement`

Tests quota enforcement for EndpointSlice (a structured/native k8s type)
in a project control plane.

EndpointSlice is a native Kubernetes type that arrives at the admission
plugin as a Go struct (*discoveryv1.EndpointSlice), not as
*unstructured.Unstructured. The admission plugin must convert it to
unstructured with correct JSON field names (metadata, not objectMeta)
for CEL template expressions like trigger.metadata.name to work.

This test uses a deterministic claim name template
"endpointslice-{{ trigger.metadata.name }}" to verify the CEL
evaluation works correctly for structured types.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-resource-registration](#step-setup-resource-registration) | 0 | 2 | 0 | 0 | 0 |
| 2 | [setup-grant-creation-policy](#step-setup-grant-creation-policy) | 0 | 2 | 0 | 0 | 0 |
| 3 | [setup-test-organization](#step-setup-test-organization) | 0 | 2 | 0 | 0 | 0 |
| 4 | [create-project-in-org](#step-create-project-in-org) | 0 | 2 | 0 | 0 | 0 |
| 5 | [verify-grant-for-project](#step-verify-grant-for-project) | 0 | 1 | 0 | 0 | 0 |
| 6 | [verify-bucket-pre-created](#step-verify-bucket-pre-created) | 0 | 1 | 0 | 0 | 0 |
| 7 | [setup-claim-creation-policy](#step-setup-claim-creation-policy) | 0 | 2 | 0 | 0 | 0 |
| 8 | [create-endpointslice-1](#step-create-endpointslice-1) | 0 | 2 | 2 | 0 | 0 |
| 9 | [verify-claim-for-endpointslice-1](#step-verify-claim-for-endpointslice-1) | 0 | 1 | 0 | 0 | 0 |
| 10 | [verify-bucket-usage-1-of-5](#step-verify-bucket-usage-1-of-5) | 0 | 1 | 0 | 0 | 0 |
| 11 | [create-endpointslice-2](#step-create-endpointslice-2) | 0 | 1 | 0 | 0 | 0 |
| 12 | [verify-claim-for-endpointslice-2](#step-verify-claim-for-endpointslice-2) | 0 | 1 | 0 | 0 | 0 |
| 13 | [verify-bucket-usage-2-of-5](#step-verify-bucket-usage-2-of-5) | 0 | 1 | 0 | 0 | 0 |
| 14 | [delete-endpointslice-1](#step-delete-endpointslice-1) | 0 | 1 | 0 | 0 | 0 |
| 15 | [verify-bucket-after-deletion](#step-verify-bucket-after-deletion) | 0 | 1 | 0 | 0 | 0 |

### Step: `setup-resource-registration`

Register EndpointSlice resource type for quota tracking

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `setup-grant-creation-policy`

Create GrantCreationPolicy to grant EndpointSlice quota to projects

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `setup-test-organization`

Create test organization

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `create-project-in-org`

Create project in org control plane

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `verify-grant-for-project`

Confirm grant is created in project control plane

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `wait` | 0 | 0 | *No description* |

### Step: `verify-bucket-pre-created`

Verify AllowanceBucket shows 5 available EndpointSlices

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `assert` | 0 | 0 | *No description* |

### Step: `setup-claim-creation-policy`

Register ClaimCreationPolicy for EndpointSlices with deterministic name

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `create-endpointslice-1`

Create EndpointSlice in project control plane.
This is the critical test: EndpointSlice is a native k8s type that
arrives as a structured Go type in admission. The CEL template
trigger.metadata.name must resolve correctly.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `sleep` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |

#### Catch

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |
| 2 | `script` | 0 | 0 | *No description* |

### Step: `verify-claim-for-endpointslice-1`

Confirm ResourceClaim with deterministic name is created and granted

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `wait` | 0 | 0 | *No description* |

### Step: `verify-bucket-usage-1-of-5`

Verify bucket shows 1 EndpointSlice allocated

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `assert` | 0 | 0 | *No description* |

### Step: `create-endpointslice-2`

Create second EndpointSlice

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

### Step: `verify-claim-for-endpointslice-2`

Confirm second claim with deterministic name is created and granted

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `wait` | 0 | 0 | *No description* |

### Step: `verify-bucket-usage-2-of-5`

Verify bucket shows 2 EndpointSlices allocated

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `assert` | 0 | 0 | *No description* |

### Step: `delete-endpointslice-1`

Delete first EndpointSlice to free quota

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |

### Step: `verify-bucket-after-deletion`

Verify bucket shows quota freed after deletion

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `assert` | 0 | 0 | *No description* |

---

