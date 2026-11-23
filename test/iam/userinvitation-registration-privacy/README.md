# Test: `userinvitation-registration-privacy`

Tests that an organization can invite any email address without learning
whether that address belongs to a registered user, while the invited user
still finds and answers the invitation.

The invitation an organization reads looks the same for a registered and
an unregistered address: both reach Pending, neither carries the invitee's
User name or a registered/not-registered condition, and selecting by a
User name finds nothing. Only the invited user can accept or decline, so
an organization cannot probe by moving the state itself. The invited user
lists their invitations in their own user context, where the API server
matches them by the hash of the user's email.

This test verifies:
- Invitations to a registered and an unregistered address both become
  Pending, with the same conditions and no invitee User.
- An organization-scope field selector on status.inviteeUser.name returns
  nothing.
- An organization-scope caller who is not the invitee cannot accept
  either invitation, and the refusal reads the same for both.
- In the invitee's user context, listing invitations returns only the one
  addressed to their email, even when the caller asks for another
  address's hash.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup](#step-setup) | 0 | 5 | 0 | 0 | 0 |
| 2 | [invite-registered-and-unregistered](#step-invite-registered-and-unregistered) | 0 | 4 | 0 | 0 | 0 |
| 3 | [org-scope-status-is-uniform](#step-org-scope-status-is-uniform) | 0 | 2 | 0 | 0 | 0 |
| 4 | [org-scope-cannot-answer-invitations](#step-org-scope-cannot-answer-invitations) | 0 | 1 | 0 | 0 | 0 |
| 5 | [invitee-lists-own-invitation](#step-invitee-lists-own-invitation) | 0 | 1 | 0 | 0 | 0 |

### Step: `setup`

Create the organization, the registered invitee, and RBAC for the organization-scope caller

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |
| 5 | `apply` | 0 | 0 | *No description* |

### Step: `invite-registered-and-unregistered`

Invite a registered and an unregistered address; both become Pending

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |

### Step: `org-scope-status-is-uniform`

The organization reads the same status for both invitations

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |
| 2 | `script` | 0 | 0 | *No description* |

### Step: `org-scope-cannot-answer-invitations`

A caller who is not the invitee cannot accept, with the same refusal for both

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `invitee-lists-own-invitation`

The invitee's user context lists only the invitation addressed to them

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

---

