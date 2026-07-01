# Test: `policybinding-subject-uid-resolution`

Verifies the PolicyBinding mutating webhook resolves a subject's uid from
its name.

This test verifies:
- A PolicyBinding referencing a Group by name (no uid) has the Group's
  metadata.uid stamped into the stored subject.
- A PolicyBinding referencing a non-existent Group is rejected at admission.
- A system group subject (name starting with "system:") is accepted and
  left without a uid.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [resolve-group-uid](#step-resolve-group-uid) | 0 | 3 | 0 | 0 | 0 |
| 2 | [reject-missing-group](#step-reject-missing-group) | 0 | 1 | 0 | 0 | 0 |
| 3 | [allow-system-group-without-uid](#step-allow-system-group-without-uid) | 0 | 2 | 0 | 0 | 0 |

### Step: `resolve-group-uid`

A name-only Group subject gets its uid resolved by the webhook

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 1 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |

### Step: `reject-missing-group`

A subject naming a non-existent Group is rejected at admission

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

### Step: `allow-system-group-without-uid`

A system group subject is accepted without a uid

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

---

