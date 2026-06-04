# Test: `project-deletion`

Tests Project deletion and resource cleanup.

This test verifies:
- A project can be deleted after reaching Ready status
- The ResourceCleanup condition progresses through the expected states
- The project is fully removed from both organization and main cluster contexts


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 5 | 0 | 0 | 0 |
| 2 | [create-project-and-wait-for-ready](#step-create-project-and-wait-for-ready) | 0 | 3 | 0 | 0 | 0 |
| 3 | [delete-project](#step-delete-project) | 0 | 2 | 0 | 0 | 0 |
| 4 | [verify-project-gone-from-main-cluster](#step-verify-project-gone-from-main-cluster) | 0 | 1 | 0 | 0 | 0 |

### Step: `setup-organization`

Create Organization, User, and OrganizationMembership for project testing

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |
| 5 | `apply` | 0 | 0 | *No description* |

### Step: `create-project-and-wait-for-ready`

Create Project in organization context and verify it reaches Ready status

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |

### Step: `delete-project`

Delete the project and verify cleanup completes

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `verify-project-gone-from-main-cluster`

Verify the project no longer exists in the main cluster

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

---

