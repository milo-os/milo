# Test: `project-suspension`

Tests ProjectSuspension validation webhooks and CEL validation rules.

This test verifies:
- Rejection of ProjectSuspension creation when referencing a non-existent project
- Immutable spec fields are protected by CEL validation rules on updates


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 2 | 0 | 0 | 0 |
| 2 | [create-project](#step-create-project) | 0 | 2 | 0 | 0 | 0 |
| 3 | [test-invalid-project-reference](#step-test-invalid-project-reference) | 0 | 1 | 0 | 0 | 0 |
| 4 | [test-immutability-validation](#step-test-immutability-validation) | 0 | 5 | 0 | 0 | 0 |
| 5 | [test-garbage-collection](#step-test-garbage-collection) | 0 | 3 | 0 | 0 | 0 |

### Step: `setup-organization`

Create Organization

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `create-project`

Create Project in organization context

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `test-invalid-project-reference`

Verify that referencing a non-existent project is rejected by validating webhook

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `test-immutability-validation`

Verify that immutable spec fields cannot be modified after creation

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `script` | 0 | 0 | *No description* |
| 3 | `script` | 0 | 0 | *No description* |
| 4 | `script` | 0 | 0 | *No description* |
| 5 | `script` | 0 | 0 | *No description* |

### Step: `test-garbage-collection`

Delete the project and verify that the ProjectSuspension is garbage collected

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `script` | 0 | 0 | *No description* |

---

