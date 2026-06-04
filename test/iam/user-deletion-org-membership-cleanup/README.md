# Test: `user-deletion-org-membership-cleanup`

Regression test for GitHub issue #536: OrganizationMembership resources are
not cleaned up when a User is deleted.

The UserController adds a finalizer (iam.miloapis.com/user-membership-cleanup)
to every active User. When a User is deleted the finalizer runs
cleanupOrganizationMemberships, which lists and deletes all OrganizationMembership
resources that reference the user before the finalizer is removed and the User
object is garbage-collected.

The OrganizationMembership validation webhook is extended to bypass the
last-owner guard when the referenced user has a non-zero DeletionTimestamp or
no longer exists in the API server, allowing the controller to proceed.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 2 | 0 | 0 | 0 |
| 2 | [setup-user](#step-setup-user) | 0 | 3 | 0 | 0 | 0 |
| 3 | [create-membership](#step-create-membership) | 0 | 3 | 0 | 0 | 0 |
| 4 | [delete-user-and-verify-membership-cleaned-up](#step-delete-user-and-verify-membership-cleaned-up) | 0 | 3 | 0 | 0 | 0 |

### Step: `setup-organization`

Create the Organization and wait for its namespace.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `setup-user`

Create a User and wait for it to become Ready. The UserController will add
the membership-cleanup finalizer on the first reconcile.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | Verify the membership-cleanup finalizer was added |

### Step: `create-membership`

Create the owner role fixture and an OrganizationMembership linking the
user to the organization. The membership controller reconciles it to Ready.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | Create the owner role so the membership webhook validates it |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |

### Step: `delete-user-and-verify-membership-cleaned-up`

Delete the User and verify the OrganizationMembership is removed by the
membership-cleanup finalizer.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `error` | 0 | 0 | Wait for the User to be fully removed |
| 3 | `error` | 0 | 0 | Assert the OrganizationMembership was deleted by the finalizer |

---

