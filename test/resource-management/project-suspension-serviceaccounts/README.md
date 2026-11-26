# Test: `project-suspension-serviceaccounts`

Tests that suspending a project marks its ServiceAccounts, and that
lifting the suspension removes the mark.

The auth provider reads the ServiceAccount's Suspended condition to stop
it from authenticating. The local environment has no auth provider, so
this test covers only the condition that Milo's controller manages.

This test verifies:
- ServiceAccounts in a project that was never suspended carry no
  Suspended condition.
- Suspending the project sets a Suspended condition with status True and
  reason ProjectSuspended on every ServiceAccount in it, with a message
  that names the project and the suspension reason.
- The suspension leaves each ServiceAccount's spec.state unchanged,
  whether the customer set it Active or Inactive.
- While the project is suspended, an identity outside system:masters can
  still write a ServiceAccount's status, which is how the controller
  manager writes the mark, but cannot update the ServiceAccount itself.
  test-admin is in system:masters and bypasses the suspension admission
  plugin, so this uses test-user.
- Lifting the suspension removes the Suspended condition from every
  ServiceAccount.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 3 | 0 | 0 | 0 |
| 2 | [create-project](#step-create-project) | 0 | 2 | 0 | 0 | 0 |
| 3 | [create-service-accounts](#step-create-service-accounts) | 0 | 3 | 0 | 0 | 0 |
| 4 | [suspend-project](#step-suspend-project) | 0 | 2 | 0 | 0 | 0 |
| 5 | [verify-service-accounts-suspended](#step-verify-service-accounts-suspended) | 0 | 2 | 0 | 0 | 0 |
| 6 | [verify-status-writable-outside-system-masters](#step-verify-status-writable-outside-system-masters) | 0 | 2 | 0 | 0 | 0 |
| 7 | [verify-update-denied-outside-system-masters](#step-verify-update-denied-outside-system-masters) | 0 | 2 | 0 | 0 | 0 |
| 8 | [lift-suspension](#step-lift-suspension) | 0 | 2 | 0 | 0 | 0 |
| 9 | [verify-suspended-condition-removed](#step-verify-suspended-condition-removed) | 0 | 2 | 0 | 0 | 0 |

### Step: `setup-organization`

Create the Organization and authorize test-user

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |

### Step: `create-project`

Create the Project and wait for its control plane to be ready

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `create-service-accounts`

Create one Active and one Inactive ServiceAccount in the project's
control plane while the project is still active. Neither may carry a
Suspended condition.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |

### Step: `suspend-project`

Suspend the project and wait for the propagator to mark it Suspended

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `verify-service-accounts-suspended`

Every ServiceAccount in the suspended project carries a Suspended
condition set to True, and keeps its own spec.state.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `assert` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `verify-status-writable-outside-system-masters`

As test-user, which is outside system:masters, write a ServiceAccount's
status in the suspended project. The suspension admission plugin must
let it through, or a controller manager outside system:masters could
never write the mark. The patch touches only status.email, so the mark
stays.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `patch` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `verify-update-denied-outside-system-masters`

As test-user, updating the ServiceAccount itself in the suspended
project is still denied, so the status exemption does not open other
writes.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `patch` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `lift-suspension`

Delete the ProjectSuspension and wait for the project to be Active again

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `verify-suspended-condition-removed`

Lifting the suspension removes the Suspended condition from every
ServiceAccount, and each keeps its own spec.state.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `assert` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

---

