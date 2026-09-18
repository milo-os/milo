# Test: `groupmembership-duplicate-prevention`

Verifies the GroupMembership validating webhook prevents a user from being
added to the same group more than once, rejects non-existent group and user
references, and prevents an existing membership from being updated.

This test verifies:
- Creating the first membership for a (user, group) pair succeeds.
- Creating a second membership for the same (user, group) pair is rejected
  at admission.
- A different user can still join the same group.
- A user can join a different group.
- Creating a membership referencing a non-existent group is rejected at admission.
- Creating a membership referencing a non-existent user is rejected at admission.
- Updating an existing membership is rejected because group memberships are immutable.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-users-and-groups](#step-setup-users-and-groups) | 0 | 4 | 0 | 0 | 0 |
| 2 | [create-first-membership](#step-create-first-membership) | 0 | 2 | 0 | 0 | 0 |
| 3 | [reject-duplicate-membership](#step-reject-duplicate-membership) | 0 | 1 | 0 | 0 | 0 |
| 4 | [allow-different-user-same-group](#step-allow-different-user-same-group) | 0 | 2 | 0 | 0 | 0 |
| 5 | [allow-same-user-different-group](#step-allow-same-user-different-group) | 0 | 2 | 0 | 0 | 0 |
| 6 | [reject-nonexistent-group](#step-reject-nonexistent-group) | 0 | 1 | 0 | 0 | 0 |
| 7 | [reject-nonexistent-user](#step-reject-nonexistent-user) | 0 | 1 | 0 | 0 | 0 |
| 8 | [reject-reference-update](#step-reject-reference-update) | 0 | 1 | 0 | 0 | 0 |
| 9 | [reject-any-update](#step-reject-any-update) | 0 | 1 | 0 | 0 | 0 |

### Step: `setup-users-and-groups`

Create required Users and Groups for testing group memberships.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |
| 4 | `assert` | 0 | 0 | *No description* |

### Step: `create-first-membership`

The first membership linking a user to a group is accepted.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `reject-duplicate-membership`

A second membership for the same (user, group) pair is rejected by the webhook.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

### Step: `allow-different-user-same-group`

A different user may still join the same group.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `allow-same-user-different-group`

The same user may join a different group.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `reject-nonexistent-group`

Creating a membership referencing a non-existent group is rejected.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

### Step: `reject-nonexistent-user`

Creating a membership referencing a non-existent user is rejected.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

### Step: `reject-reference-update`

Applying a change to an existing membership's group pointer is rejected because group memberships are immutable.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

### Step: `reject-any-update`

Re-applying an existing membership is rejected because group memberships are immutable.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

---

