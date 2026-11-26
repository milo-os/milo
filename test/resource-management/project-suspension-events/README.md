# Test: `project-suspension-events`

Tests that the suspension controller's Suspended and Reinstated events
reach Milo under Milo's real RBAC.

The controller manager connects to Milo as system:masters, but it creates
these events while impersonating its project event user
(system:milo:project-event-emitter) and the project's parent context. An
impersonated request is authorized as the impersonated user alone, so the
event create only succeeds if config/services/resourcemanager/project-events
grants that user events create in Milo.

This test verifies:
- Suspending a project creates a Suspended event regarding the project,
  whose message is the reason category and never the
  ProjectSuspension's name.
- The project's Suspended condition message names the category only.
- Lifting the suspension creates a Reinstated event.
- Milo's RBAC is what allows the create: the default project event user
  can create an event, and any other impersonated user is refused.

Scope annotations are not asserted here. Milo adds them on the events
proxy path (the EventsProxy feature gate), which test-infra does not
enable; internal/apiserver/events covers them in unit tests.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 3 | 0 | 0 | 0 |
| 2 | [create-project](#step-create-project) | 0 | 2 | 0 | 0 | 0 |
| 3 | [suspend-project](#step-suspend-project) | 0 | 2 | 0 | 0 | 0 |
| 4 | [verify-suspended-event](#step-verify-suspended-event) | 0 | 1 | 0 | 0 | 0 |
| 5 | [verify-milo-rbac-decides](#step-verify-milo-rbac-decides) | 0 | 1 | 0 | 0 | 0 |
| 6 | [lift-suspension](#step-lift-suspension) | 0 | 3 | 0 | 0 | 0 |

### Step: `setup-organization`

Create the Organization.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |

### Step: `create-project`

Create the Project in the organization's control plane.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `suspend-project`

Suspend the project and check the condition message is redacted.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `verify-suspended-event`

The controller creates a Suspended event regarding the project. The
create runs as the impersonated project event user, so it only lands
if Milo's RBAC allows it.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `verify-milo-rbac-decides`

Show that the event create is decided by Milo's RBAC and not by the
caller's system:masters group: the default project event user may
create an event, and another impersonated user may not.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `lift-suspension`

Lift the suspension and check the Reinstated event.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `script` | 0 | 0 | *No description* |

---

