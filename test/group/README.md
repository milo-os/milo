# Test: `group`

Tests IAM Group functionality including creation, membership, and garbage collection.

This test verifies:
- Groups can be created and become Ready
- PolicyBindings can reference groups
- GroupMemberships link users to groups
- Deleting a group cascades to delete GroupMemberships
- GroupMemberships are annotated with the member's email at admission
- PolicyBindings are updated when groups are deleted
- PolicyBindings are deleted when they have no remaining subjects


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [create-groups-and-policy-binding](#step-create-groups-and-policy-binding) | 0 | 11 | 0 | 0 | 0 |
| 2 | [create-memberships](#step-create-memberships) | 0 | 2 | 0 | 0 | 0 |
| 3 | [stamp-member-email](#step-stamp-member-email) | 0 | 3 | 0 | 0 | 0 |
| 4 | [delete-groups](#step-delete-groups) | 0 | 12 | 0 | 0 | 0 |

### Step: `create-groups-and-policy-binding`

Create Groups, Role, Organization, and PolicyBinding with group references

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 1 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |
| 4 | `apply` | 0 | 1 | *No description* |
| 5 | `wait` | 0 | 0 | *No description* |
| 6 | `apply` | 0 | 0 | *No description* |
| 7 | `wait` | 0 | 0 | *No description* |
| 8 | `apply` | 0 | 1 | *No description* |
| 9 | `apply` | 0 | 0 | *No description* |
| 10 | `assert` | 0 | 0 | *No description* |
| 11 | `wait` | 0 | 0 | *No description* |

### Step: `create-memberships`

Create GroupMemberships linking users to the test groups

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `stamp-member-email`

A GroupMembership referencing an existing User is annotated with the member's email at admission

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |

### Step: `delete-groups`

Delete groups and verify cascade deletion of memberships and policy binding updates

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |
| 5 | `error` | 0 | 0 | *No description* |
| 6 | `error` | 0 | 0 | *No description* |
| 7 | `sleep` | 0 | 0 | *No description* |
| 8 | `assert` | 0 | 0 | *No description* |
| 9 | `delete` | 0 | 0 | *No description* |
| 10 | `wait` | 0 | 0 | *No description* |
| 11 | `wait` | 0 | 0 | *No description* |
| 12 | `error` | 0 | 0 | *No description* |

---

