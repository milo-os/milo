# Test: `organization-fast-deletion`

Regression test: deleting an Organization immediately after creating it
must still garbage-collect its namespace.

The organization-scoped namespace has to be owned by the Organization
from the instant it's created, not only once a later reconcile patches
the reference in. Otherwise a fast create-then-delete -- with no time
for that reconcile to ever run -- permanently orphans the namespace and
everything created inside it (OrganizationMemberships, PolicyBindings).


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup](#step-setup) | 0 | 2 | 0 | 0 | 0 |
| 2 | [create-and-immediately-delete](#step-create-and-immediately-delete) | 0 | 4 | 0 | 0 | 0 |

### Step: `setup`

Remove any leftover state from earlier runs.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | Remove any leftover organization from earlier runs |
| 2 | `delete` | 0 | 0 | Ensure previous namespace is cleaned up |

### Step: `create-and-immediately-delete`

Create the Organization, confirm its namespace is already owned by
it, then delete the Organization with no intervening wait and
confirm the namespace is garbage-collected anyway.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | Create test organization |
| 2 | `assert` | 0 | 0 | Namespace must be owned by the Organization from the moment it's created |
| 3 | `delete` | 0 | 0 | Delete the organization immediately, with no wait for any reconcile |
| 4 | `wait` | 0 | 0 | Namespace must be removed by the owner reference alone, without depending on reconcile timing |

---

