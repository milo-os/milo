# Test: `projectsuspension-admission`

Tests the ProjectSuspensionEnforcement admission plugin.

This test verifies:
- We can create and update resources in an active project.
- When a project gets suspended, read and delete operations are still allowed.
- When a project is suspended, create and update operations are rejected with a 403.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 3 | 0 | 0 | 0 |
| 2 | [create-project](#step-create-project) | 0 | 2 | 0 | 0 | 0 |
| 3 | [create-resources-before-suspension](#step-create-resources-before-suspension) | 0 | 4 | 0 | 0 | 0 |
| 4 | [update-resource-before-suspension](#step-update-resource-before-suspension) | 0 | 1 | 0 | 0 | 0 |
| 5 | [suspend-project](#step-suspend-project) | 0 | 3 | 0 | 0 | 0 |
| 6 | [verify-read-allowed](#step-verify-read-allowed) | 0 | 1 | 0 | 0 | 0 |
| 7 | [verify-delete-allowed](#step-verify-delete-allowed) | 0 | 1 | 0 | 0 | 0 |
| 8 | [verify-create-denied](#step-verify-create-denied) | 0 | 1 | 0 | 0 | 0 |
| 9 | [verify-update-denied](#step-verify-update-denied) | 0 | 1 | 0 | 0 | 0 |
| 10 | [verify-finalizer-removal-allowed](#step-verify-finalizer-removal-allowed) | 0 | 5 | 0 | 0 | 0 |

### Step: `setup-organization`

Create Organization

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |

### Step: `create-project`

Create Project in organization context

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `create-resources-before-suspension`

Verify that we can create resources in the project

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |
| 4 | `assert` | 0 | 0 | *No description* |

### Step: `update-resource-before-suspension`

Verify that we can update resources in the project

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `patch` | 0 | 0 | *No description* |

### Step: `suspend-project`

Add suspension to the project

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `sleep` | 0 | 0 | *No description* |

### Step: `verify-read-allowed`

Verify that we can read resources while suspended

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `assert` | 0 | 0 | *No description* |

### Step: `verify-delete-allowed`

Verify that we can delete resources while suspended

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |

### Step: `verify-create-denied`

Verify that creating a resource is denied with 403 ProjectSuspended

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `verify-update-denied`

Verify that updating a resource is denied with 403 ProjectSuspended

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `verify-finalizer-removal-allowed`

Verify that removing a finalizer (the plain Update that lets a
graceful delete complete) is allowed while the project is suspended.
The finalizer-removal PATCH below runs as the non-privileged
test-user (via curl + test-user-token), not test-admin/system:masters
(kubeconfig-project) — the plugin already exempts system:masters
unconditionally, so doing this step with the admin kubeconfig would
pass regardless of whether the isAlreadyTerminating exemption exists
at all. Using test-user is what actually exercises that code path.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |
| 2 | `sleep` | 0 | 0 | *No description* |
| 3 | `script` | 0 | 0 | *No description* |
| 4 | `script` | 0 | 0 | *No description* |
| 5 | `script` | 0 | 0 | *No description* |

---

