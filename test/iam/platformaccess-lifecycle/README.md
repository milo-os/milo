# Test: `platformaccess-lifecycle`

Validates the full lifecycle of the PlatformAccess resource.
Specifically:
- Creation validation and defaulting (OwnerReference to User)
- Rejection of invalid state transitions (CEL Validation)
- Rejection of modifying the immutable userRef field (CEL Validation)
- Prevention of duplicate PlatformAccess resources for the same user (Validating Webhook)
- Prevention of PlatformAccess deletion (Validating Webhook)
- Garbage collection of PlatformAccess upon User deletion (OwnerReference)


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [create-user-and-pa](#step-create-user-and-pa) | 0 | 4 | 0 | 0 | 0 |
| 2 | [test-invalid-state-value](#step-test-invalid-state-value) | 0 | 1 | 0 | 0 | 0 |
| 3 | [test-immutable-userref](#step-test-immutable-userref) | 0 | 1 | 0 | 0 | 0 |
| 4 | [test-nonexistent-user](#step-test-nonexistent-user) | 0 | 1 | 0 | 0 | 0 |
| 5 | [test-duplicate-platformaccess](#step-test-duplicate-platformaccess) | 0 | 1 | 0 | 0 | 0 |
| 6 | [test-prevent-deletion](#step-test-prevent-deletion) | 0 | 1 | 0 | 0 | 0 |
| 7 | [test-garbage-collection-on-user-delete](#step-test-garbage-collection-on-user-delete) | 0 | 3 | 0 | 0 | 0 |

### Step: `create-user-and-pa`

Create User, then PlatformAccess, and verify it gets created with correct OwnerReference.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |

### Step: `test-invalid-state-value`

Attempt to transition to a wrong state value not allowed by the enum.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `patch` | 0 | 0 | *No description* |

### Step: `test-immutable-userref`

Attempt to modify the immutable userRef field, which is not allowed.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `patch` | 0 | 0 | *No description* |

### Step: `test-nonexistent-user`

Attempt to create PlatformAccess referencing a non-existent user, which is not allowed.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `test-duplicate-platformaccess`

Attempt to create another PlatformAccess for the same user, which is not allowed.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `test-prevent-deletion`

Attempt to delete the PlatformAccess resource, which must be blocked.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |

### Step: `test-garbage-collection-on-user-delete`

Delete the User and verify the PlatformAccess is garbage collected.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `error` | 0 | 0 | *No description* |
| 3 | `error` | 0 | 0 | *No description* |

---

