# Test: `platformaccess-migration`

Validates that PlatformAccess resources are automatically created and synced
based on User state and registration approval status by using the appropriate
supporting resources (PlatformAccessApproval, PlatformAccessRejection, UserDeactivation).


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [create-user-and-verify-pa](#step-create-user-and-verify-pa) | 0 | 3 | 0 | 0 | 0 |
| 2 | [test-sync-to-approved](#step-test-sync-to-approved) | 0 | 3 | 0 | 0 | 0 |
| 3 | [test-sync-to-rejected](#step-test-sync-to-rejected) | 0 | 4 | 0 | 0 | 0 |
| 4 | [test-sync-to-suspended](#step-test-sync-to-suspended) | 0 | 4 | 0 | 0 | 0 |
| 5 | [test-sync-back-to-approved](#step-test-sync-back-to-approved) | 0 | 6 | 0 | 0 | 0 |
| 6 | [cleanup-and-verify-deletion](#step-cleanup-and-verify-deletion) | 0 | 4 | 0 | 0 | 0 |

### Step: `create-user-and-verify-pa`

Create a User and verify that a corresponding PlatformAccess is created in Pending state.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |

### Step: `test-sync-to-approved`

Create PlatformAccessApproval and verify User status and PlatformAccess state update to Approved.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |

### Step: `test-sync-to-rejected`

Remove approval, create PlatformAccessRejection, and verify status sync to Rejected.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |

### Step: `test-sync-to-suspended`

Create UserDeactivation, set Ready status, and verify status sync to Suspended (Inactive).

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `script` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |

### Step: `test-sync-back-to-approved`

Remove deactivation and rejection, re-apply approval, and verify sync back to Approved.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `delete` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |
| 5 | `wait` | 0 | 0 | *No description* |
| 6 | `wait` | 0 | 0 | *No description* |

### Step: `cleanup-and-verify-deletion`

Delete the User and verify everything is cleaned up and garbage collected.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `error` | 0 | 0 | *No description* |
| 3 | `error` | 0 | 0 | *No description* |
| 4 | `delete` | 0 | 0 | *No description* |

---

