# Test: `organization-fast-deletion`

Regression test: deleting an Organization immediately after creating it
must still garbage-collect its namespace.

The namespace's owner reference to the Organization is set by
OrganizationController.Reconcile once the Organization is committed and
watchable -- not synchronously at namespace-creation time, since a
namespace created with an owner reference to an Organization that hasn't
been persisted yet (as happens if this is done from the Organization's
own validating admission webhook) can be seen as orphaned by the garbage
collector and deleted before the Organization creation call even
returns. An organizationNamespaceFinalizer on the Organization closes the
fast create-then-delete window instead: deletion can't complete until
Reconcile has confirmed the namespace owns a valid reference back to it,
so cascading cleanup (OrganizationMemberships, PolicyBindings, and the
namespace itself) always happens once the Organization is actually
removed.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup](#step-setup) | 0 | 2 | 0 | 0 | 0 |
| 2 | [create-and-immediately-delete](#step-create-and-immediately-delete) | 0 | 3 | 0 | 0 | 0 |

### Step: `setup`

Remove any leftover state from earlier runs.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | Remove any leftover organization from earlier runs |
| 2 | `delete` | 0 | 0 | Ensure previous namespace is cleaned up |

### Step: `create-and-immediately-delete`

Create the Organization, then delete it immediately with no wait for
any reconcile, and confirm the namespace is garbage-collected anyway
once the finalizer releases.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | Create test organization |
| 2 | `delete` | 0 | 0 | Delete the organization immediately, with no wait for any reconcile |
| 3 | `wait` | 0 | 0 | Namespace must still be removed, via the namespace finalizer gating deletion until its owner reference is confirmed |

---

