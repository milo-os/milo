# Test: `crm-note-contact-lifecycle`

End-to-end tests for CRM Notes.

This test verifies:
- A Note referencing a Contact and User populates status.createdBy with the User's email
- Deleting a Contact causes a reconciled Note that references it to be deleted
- Deleting a User causes owned Notes to be garbage-collected via ownerReferences


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [create-user-contact-and-notes](#step-create-user-contact-and-notes) | 0 | 5 | 0 | 0 | 0 |
| 2 | [delete-contact-and-verify-contact-note-deletion](#step-delete-contact-and-verify-contact-note-deletion) | 0 | 5 | 0 | 0 | 0 |
| 3 | [delete-user-and-verify-user-note-deletion](#step-delete-user-and-verify-user-note-deletion) | 0 | 3 | 0 | 0 | 0 |
| 4 | [verify-additional-notes-still-exist](#step-verify-additional-notes-still-exist) | 0 | 2 | 0 | 0 | 0 |

### Step: `create-user-contact-and-notes`

Create IAM User, Notification Contact, and CRM Notes, then verify Note status

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `apply` | 0 | 0 | *No description* |
| 5 | `assert` | 0 | 0 | *No description* |

### Step: `delete-contact-and-verify-contact-note-deletion`

Delete Contact, update the contact Note to trigger reconciliation, and verify the contact Note is deleted

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `assert` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `delete` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |
| 5 | `wait` | 0 | 0 | *No description* |

### Step: `delete-user-and-verify-user-note-deletion`

Delete the IAM User and verify the Note that references the User is garbage-collected

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `assert` | 0 | 0 | *No description* |
| 2 | `delete` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |

### Step: `verify-additional-notes-still-exist`

Verify additional Notes are not accidentally deleted by Contact or User deletion

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `assert` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

---

