# Test: `groupmembership-duplicate-prevention`

Verifies the GroupMembership validating webhook prevents a user from being
added to the same group more than once, rejects non-existent group and user
references, and prevents the spec of an existing membership from being
changed while still allowing metadata-only updates.

This test verifies:
- Creating the first membership for a (user, group) pair succeeds.
- Creating a second membership for the same (user, group) pair is rejected
  at admission.
- A different user can still join the same group.
- A user can join a different group.
- Creating a membership referencing a non-existent group is rejected at admission.
- Creating a membership referencing a non-existent user is rejected at admission.
- Changing an existing membership's group pointer is rejected because the spec is immutable.
- A metadata-only update to an existing membership is accepted.
- Adding and removing a finalizer is accepted.


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
| 9 | [allow-metadata-only-update](#step-allow-metadata-only-update) | 0 | 2 | 0 | 0 | 0 |
| 10 | [allow-finalizer-add-and-remove](#step-allow-finalizer-add-and-remove) | 0 | 4 | 0 | 0 | 0 |

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

Applying a change to an existing membership's group pointer is rejected because the membership spec is immutable.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

### Step: `allow-metadata-only-update`

A metadata-only update is accepted because only the spec is immutable. This is the path the OpenFGA controller takes when it adds its finalizer to a new membership.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `allow-finalizer-add-and-remove`

Adding and then dropping a finalizer is accepted. This is the path the OpenFGA controller takes when it claims a new membership and releases it on delete; rejecting it leaves the membership without an authorization tuple and stuck in Terminating.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `assert` | 0 | 0 | *No description* |

---

