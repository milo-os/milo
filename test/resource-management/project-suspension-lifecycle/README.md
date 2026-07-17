# Test: `project-suspension-lifecycle`

Tests the full lifecycle of project suspension propagation.

This test verifies:
- Adding multiple suspensions updates the project status with all references and reasons.
- Setting active suspensions sets the project condition 'Suspended' to True.
- Removing suspensions updates the status list accordingly.
- When all suspensions are removed, the 'Suspended' condition is set to False (Active).


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 2 | 0 | 0 | 0 |
| 2 | [create-project](#step-create-project) | 0 | 2 | 0 | 0 | 0 |
| 3 | [add-first-suspension](#step-add-first-suspension) | 0 | 2 | 0 | 0 | 0 |
| 4 | [add-second-suspension](#step-add-second-suspension) | 0 | 2 | 0 | 0 | 0 |
| 5 | [add-third-suspension](#step-add-third-suspension) | 0 | 2 | 0 | 0 | 0 |
| 6 | [remove-first-suspension](#step-remove-first-suspension) | 0 | 2 | 0 | 0 | 0 |
| 7 | [remove-second-suspension](#step-remove-second-suspension) | 0 | 2 | 0 | 0 | 0 |
| 8 | [remove-third-suspension](#step-remove-third-suspension) | 0 | 2 | 0 | 0 | 0 |

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

### Step: `add-first-suspension`

Add first suspension and verify project is suspended

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `add-second-suspension`

Add second suspension and verify project status lists both

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `add-third-suspension`

Add third suspension and verify project status lists all three

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `remove-first-suspension`

Delete first suspension and verify it is removed from project status

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `remove-second-suspension`

Delete second suspension and verify it is removed from project status

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `remove-third-suspension`

Delete third suspension and verify project returns to Active status

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

---

