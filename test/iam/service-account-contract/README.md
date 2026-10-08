# Test: `service-account-contract`

Tests the iam.miloapis.com ServiceAccount API contract in a Project
control plane served by the local Milo apiserver.

This test verifies:
- A ServiceAccount can be created in a Project control plane
- spec.state defaults to Active when spec is present and spec.state is
  omitted
- The ServiceAccount is not visible at the platform root, so it is
  scoped to the Project control plane
- A ServiceAccount can be deleted and is fully removed

Key issuance, authentication, and revocation for service accounts need
the auth service, which the local environment lacks, so they are not covered
here.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 5 | 0 | 0 | 0 |
| 2 | [create-project-and-wait-for-ready](#step-create-project-and-wait-for-ready) | 0 | 2 | 0 | 0 | 0 |
| 3 | [create-service-account](#step-create-service-account) | 0 | 2 | 0 | 0 | 0 |
| 4 | [verify-service-account-absent-at-root](#step-verify-service-account-absent-at-root) | 0 | 1 | 0 | 0 | 0 |
| 5 | [delete-service-account](#step-delete-service-account) | 0 | 2 | 0 | 0 | 0 |

### Step: `setup-organization`

Create Organization, User, and OrganizationMembership for the project

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |
| 5 | `apply` | 0 | 0 | *No description* |

### Step: `create-project-and-wait-for-ready`

Create the Project that hosts the ServiceAccount control plane

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `create-service-account`

Create a ServiceAccount in the Project control plane and verify spec.state defaults to Active

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `verify-service-account-absent-at-root`

Verify the ServiceAccount is not stored at the platform root

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `error` | 0 | 0 | *No description* |

### Step: `delete-service-account`

Delete the ServiceAccount and verify it is removed

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `error` | 0 | 0 | *No description* |

---

