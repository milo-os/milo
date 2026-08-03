# Test: `project-suspension-lifecycle`

Tests the full lifecycle of project suspension propagation.

This test verifies:
- Adding multiple suspensions updates the project status with all references and reasons.
- Setting active suspensions sets the project condition 'Suspended' to True.
- Removing suspensions updates the status list accordingly.
- When all suspensions are removed, the 'Suspended' condition is set to False (Active).
- While a project is suspended, Create requests against its virtual control
  plane are denied with a 403 Forbidden carrying a ProjectSuspended status
  cause (ProjectSuspensionEnforcement admission plugin).
- While a project is suspended, Delete requests against its virtual
  control plane are still allowed.
- Once the last suspension is lifted, Create requests succeed again
  (verifies the enforcement plugin's short-TTL cache does not leave the
  project stuck in a denied state after reinstatement).


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 3 | 0 | 0 | 0 |
| 2 | [create-project](#step-create-project) | 0 | 2 | 0 | 0 | 0 |
| 3 | [seed-namespace-before-suspension](#step-seed-namespace-before-suspension) | 0 | 2 | 0 | 0 | 0 |
| 4 | [add-first-suspension](#step-add-first-suspension) | 0 | 2 | 0 | 0 | 0 |
| 5 | [verify-create-denied-while-suspended](#step-verify-create-denied-while-suspended) | 0 | 1 | 0 | 0 | 0 |
| 6 | [verify-delete-allowed-while-suspended](#step-verify-delete-allowed-while-suspended) | 0 | 1 | 0 | 0 | 0 |
| 7 | [add-second-suspension](#step-add-second-suspension) | 0 | 2 | 0 | 0 | 0 |
| 8 | [add-third-suspension](#step-add-third-suspension) | 0 | 2 | 0 | 0 | 0 |
| 9 | [remove-first-suspension](#step-remove-first-suspension) | 0 | 2 | 0 | 0 | 0 |
| 10 | [remove-second-suspension](#step-remove-second-suspension) | 0 | 2 | 0 | 0 | 0 |
| 11 | [remove-third-suspension](#step-remove-third-suspension) | 0 | 2 | 0 | 0 | 0 |
| 12 | [verify-create-allowed-after-reinstatement](#step-verify-create-allowed-after-reinstatement) | 0 | 1 | 0 | 0 | 0 |
| 13 | [cleanup-write-check-namespace](#step-cleanup-write-check-namespace) | 0 | 1 | 0 | 0 | 0 |

### Step: `setup-organization`

Create Organization

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |

### Step: `create-project`

Create Project in organization context

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `seed-namespace-before-suspension`

Create a Namespace in the project's control plane while the project
is still Active. This resource is deleted later, while the project
is suspended, to lock in the FR3 decision that Delete requests are
allowed during suspension.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `add-first-suspension`

Add first suspension and verify project is suspended

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `verify-create-denied-while-suspended`

With the project suspended, a Create request against its virtual
control plane must be denied by the ProjectSuspensionEnforcement
admission plugin with a 403 Forbidden carrying a ProjectSuspended
status cause (FR1/FR4). kubectl does not surface the structured
Details.Causes of a Forbidden response on write failures (it only
prints the human-readable message), so this step talks to the
project's control-plane REST endpoint directly with curl to inspect
the raw Status object returned by the API server.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `verify-delete-allowed-while-suspended`

Delete requests must still succeed while the project is suspended
(FR3 regression test) — the ProjectSuspensionEnforcement plugin is
not registered for the Delete operation.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |

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

### Step: `verify-create-allowed-after-reinstatement`

Once the last suspension is lifted (project Active again), the same
Create that was denied earlier in verify-create-denied-while-suspended
must now succeed. This validates NFR2: the plugin's 2-second
suspension-state cache does not leave the project stuck in a
denied state after reinstatement. Reuses the exact same object name
that was previously denied, which also proves it was never actually
created while suspended.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `cleanup-write-check-namespace`

Remove the Namespace created by verify-create-allowed-after-reinstatement.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |

---

