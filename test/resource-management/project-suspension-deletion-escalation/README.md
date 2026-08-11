# Test: `project-suspension-deletion-escalation`

Tests that a suspended project schedules an auditable escalation to
deletion and notifies the project's organization, and that reinstating
the project cancels the pending escalation.

This test verifies:
- Suspending a project sets status.suspensionEscalation.deletionAt and a
  PendingDeletion condition (the retention-window clock).
- A countdown warning e-mail is sent to the organization's contact
  address as soon as the project becomes suspended, stored in the
  milo-system namespace as a centralized audit trail.
- Reinstating the project (lifting its only ProjectSuspension) clears
  status.suspensionEscalation and flips PendingDeletion back to False.

Waiting out the full retention window and asserting the eventual Delete
is covered by unit tests (projectsuspension_escalation_controller_test.go),
which can control the clock deterministically; doing so here would
require the suite to block for the configured retention window.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-prerequisites](#step-setup-prerequisites) | 0 | 1 | 0 | 0 | 0 |
| 2 | [setup-organization](#step-setup-organization) | 0 | 2 | 0 | 0 | 0 |
| 3 | [create-project](#step-create-project) | 0 | 2 | 0 | 0 | 0 |
| 4 | [suspend-project](#step-suspend-project) | 0 | 2 | 0 | 0 | 0 |
| 5 | [verify-deletion-warning-email-sent](#step-verify-deletion-warning-email-sent) | 0 | 1 | 0 | 0 | 0 |
| 6 | [reinstate-project](#step-reinstate-project) | 0 | 2 | 0 | 0 | 0 |

### Step: `setup-prerequisites`

Create the EmailTemplate the escalation controller's Email webhook
requires to exist before it will admit a warning Email (the
controller-manager's default --project-suspension-deletion-warning-
email-template value).


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `setup-organization`

Create Organization with a contact e-mail for notifications

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

### Step: `suspend-project`

Suspend the project and verify the retention window is scheduled

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `verify-deletion-warning-email-sent`

As soon as the project is suspended, a countdown warning e-mail must
be created for the organization's contact address. Warning e-mails
are stored in the milo-system namespace (not the organization's own
namespace) so they remain a stable, centralized audit trail.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `reinstate-project`

Lift the suspension and verify the escalation is cancelled

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

---

