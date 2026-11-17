# Test: `access-rules`

End-to-end test for owner and hidden-field access rules
(docs/architecture/access-rules.md), through the real handler chain.

Two synthetic CRDs: notes carry iam.miloapis.com/owner-field, bulletins
carry iam.miloapis.com/hidden-field. test-user (token test-user-token,
UID test-user) may do anything to both through RBAC, but has no get on
bulletins/hidden until a later step makes it staff.

Covers:
  - lists, watches as tables, and collection deletes narrowed to the
    caller's notes in the user and organization contexts, even when the
    client asks for another owner;
  - named requests for another user's note, or a hidden bulletin,
    answering NotFound from the filter. The filter reads the stored object
    through the loopback client, so a hidden bulletin answering 404 rather
    than 200 also shows the loopback read isn't narrowed itself;
  - the AccessRuleOwnerEnforcement admission plugin refusing a create, or
    a server-side apply that creates, for another owner, and a change of
    owner;
  - the platform context passing owner rules through but still hiding
    bulletins, and staff seeing hidden bulletins;
  - the milo-access-rules readiness check.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [install-crds](#step-install-crds) | 0 | 6 | 0 | 0 | 0 |
| 2 | [readiness-check](#step-readiness-check) | 0 | 1 | 0 | 0 | 0 |
| 3 | [user-context](#step-user-context) | 0 | 2 | 0 | 0 | 0 |
| 4 | [organization-context](#step-organization-context) | 0 | 2 | 0 | 0 | 0 |
| 5 | [platform-context](#step-platform-context) | 0 | 1 | 0 | 0 | 0 |
| 6 | [staff](#step-staff) | 0 | 2 | 0 | 0 | 0 |

### Step: `install-crds`

*No description*

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |
| 4 | `apply` | 0 | 0 | *No description* |
| 5 | `apply` | 0 | 0 | *No description* |
| 6 | `sleep` | 0 | 0 | *No description* |

### Step: `readiness-check`

*No description*

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `user-context`

test-user, in its own user context, only sees and changes its own notes.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |
| 2 | `script` | 0 | 0 | *No description* |

### Step: `organization-context`

In an organization context, test-user still only sees its own notes.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `script` | 0 | 0 | *No description* |

### Step: `platform-context`

The platform context passes owner rules through but still hides bulletins from non-staff.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `staff`

With get on bulletins/hidden, test-user sees hidden bulletins.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `script` | 0 | 0 | *No description* |

---

