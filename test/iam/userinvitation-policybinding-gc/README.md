# Test: `userinvitation-policybinding-gc`

Regression test for GitHub issue #535: Orphaned PolicyBindings for deleted
UserInvitations are not garbage collected.

The UserInvitationController creates PolicyBindings in milo-system granting
the invitee user `getinvitation` and `acceptinvitation` permissions. When a
UserInvitation is deleted (accepted, declined, expired, or manually removed),
the associated PolicyBindings must be cleaned up by the finalizer.

The fix wires r.finalizer.Finalize(ctx, ui) into Reconcile so the finalizer
string is added on first reconcile and the cleanup handler runs on deletion,
deleting both PolicyBindings before the object is removed.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-prerequisites](#step-setup-prerequisites) | 0 | 5 | 0 | 0 | 0 |
| 2 | [create-invitation-and-verify-policybindings](#step-create-invitation-and-verify-policybindings) | 0 | 3 | 0 | 0 | 0 |
| 3 | [delete-invitation-and-verify-policybindings-cleaned-up](#step-delete-invitation-and-verify-policybindings-cleaned-up) | 0 | 3 | 0 | 0 | 0 |

### Step: `setup-prerequisites`

Create the Role and EmailTemplate that the UserInvitation webhook and
controller require, then create the Organization and invitee User.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | Create role and email template fixtures in milo-system |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |
| 4 | `apply` | 0 | 0 | *No description* |
| 5 | `wait` | 0 | 0 | *No description* |

### Step: `create-invitation-and-verify-policybindings`

Create the UserInvitation and wait for the Pending condition, which
indicates the controller created the PolicyBindings.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `script` | 0 | 0 | Verify both invitation PolicyBindings were created in milo-system |

### Step: `delete-invitation-and-verify-policybindings-cleaned-up`

Delete the UserInvitation and verify the finalizer cleans up both
PolicyBindings before the object is removed.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `error` | 0 | 0 | Wait for the UserInvitation to be fully removed |
| 3 | `script` | 0 | 0 | Assert both PolicyBindings were deleted by the finalizer |

---

