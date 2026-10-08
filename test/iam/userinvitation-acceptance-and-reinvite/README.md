# Test: `userinvitation-acceptance-and-reinvite`

End-to-end regression tests for GitHub issue #802, which had two defects:

1. Accepting an invitation after its expiration silently did nothing:
   spec.state was recorded as Accepted, but no membership was granted and
   no error was raised, leaving a dead "Accepted" + Expired record.
2. Any existing invitation (expired, declined, or accepted-too-late) counted
   as a duplicate, so re-inviting a person after the first invitation went
   terminal failed with `spec.organizationRef: Duplicate value`.

The fix makes the validating webhook reject an acceptance (Pending ->
Accepted) once the invitation has expired, and relaxes the create-time
duplicate check so only a live, still-pending invitation blocks a re-invite.

This suite verifies the test plan for #802:
- Scenario A: accepting an expired invitation is rejected; the object is
  left untouched as Pending.
- Scenario B: after an invitation expires, re-inviting the same email and
  organization succeeds (the expired record no longer counts as a duplicate).
- Scenario C: after an invitation is declined, re-inviting the same email
  and organization succeeds.
- Scenario D: while an invitation is live and pending, a duplicate
  invitation for the same email and organization is still rejected.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-prerequisites](#step-setup-prerequisites) | 0 | 13 | 0 | 0 | 0 |
| 2 | [accept-after-expiry-is-rejected](#step-accept-after-expiry-is-rejected) | 0 | 5 | 0 | 0 | 0 |
| 3 | [expired-invitation-does-not-block-reinvite](#step-expired-invitation-does-not-block-reinvite) | 0 | 3 | 0 | 0 | 0 |
| 4 | [declined-invitation-does-not-block-reinvite](#step-declined-invitation-does-not-block-reinvite) | 0 | 7 | 0 | 0 | 0 |
| 5 | [live-pending-invitation-blocks-reinvite](#step-live-pending-invitation-blocks-reinvite) | 0 | 5 | 0 | 0 | 0 |
| 6 | [accepted-invitation-grants-membership-and-is-removed](#step-accepted-invitation-grants-membership-and-is-removed) | 0 | 10 | 0 | 0 | 0 |

### Step: `setup-prerequisites`

Create the shared Role and EmailTemplate fixtures in milo-system that the
UserInvitation webhook and controller require, then create each test
Organization (and its namespace) and invitee User.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | Create role and email template fixtures in milo-system |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `apply` | 0 | 0 | *No description* |
| 5 | `wait` | 0 | 0 | *No description* |
| 6 | `wait` | 0 | 0 | *No description* |
| 7 | `wait` | 0 | 0 | *No description* |
| 8 | `apply` | 0 | 0 | *No description* |
| 9 | `apply` | 0 | 0 | *No description* |
| 10 | `apply` | 0 | 0 | *No description* |
| 11 | `wait` | 0 | 0 | *No description* |
| 12 | `wait` | 0 | 0 | *No description* |
| 13 | `wait` | 0 | 0 | *No description* |

### Step: `accept-after-expiry-is-rejected`

Issue #802 defect 1. After a UserInvitation lapses, an acceptance
(Pending -> Accepted) must be rejected at admission, the message must
mention the expiration, and the object must remain Pending.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | Create a UserInvitation that expires a few seconds in the future |
| 2 | `wait` | 0 | 0 | Wait for the invitation to be processed while live |
| 3 | `script` | 0 | 0 | Wait for the invitation to lapse past its expiration |
| 4 | `script` | 0 | 0 | Accepting the expired invitation is rejected at admission |
| 5 | `script` | 0 | 0 | The originally-sent invitation email still exists for the invitee |

### Step: `expired-invitation-does-not-block-reinvite`

Issue #802 defect 2. The expired UserInvitation from Scenario A is retained
but must not count as a duplicate. Creating a fresh invitation for the same
email and organization succeeds.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `script` | 0 | 0 | The re-invite produced an invitation email to the invitee |

### Step: `declined-invitation-does-not-block-reinvite`

Issue #802 defect 2. A declined UserInvitation is terminal and must not
count as a duplicate. Creating a fresh invitation for the same email and
organization after a decline succeeds.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `assert` | 0 | 0 | *No description* |
| 5 | `apply` | 0 | 0 | *No description* |
| 6 | `wait` | 0 | 0 | *No description* |
| 7 | `script` | 0 | 0 | The post-decline re-invite produced an invitation email to the invitee |

### Step: `live-pending-invitation-blocks-reinvite`

Issue #802 constraint. Only a live (not expired), still-pending invitation
may block a re-invite. Creating a second invitation for the same email and
organization while the first is live and pending must be rejected, and only
the original invitation may remain.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `script` | 0 | 0 | The live pending invitation produced an invitation email to the invitee |
| 4 | `apply` | 0 | 0 | *No description* |
| 5 | `script` | 0 | 0 | Only the original live invitation exists |

### Step: `accepted-invitation-grants-membership-and-is-removed`

Issue #802 defect 1. Accepting a live invitation grants the requested
OrganizationMembership (defect 1: acceptance must not silently do nothing)
and removes the accepted invitation.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |
| 5 | `apply` | 0 | 0 | *No description* |
| 6 | `wait` | 0 | 0 | *No description* |
| 7 | `script` | 0 | 0 | The invitation produced an email to the invitee |
| 8 | `script` | 0 | 0 | Accept the live invitation |
| 9 | `wait` | 0 | 0 | The accepted invitation is removed after membership is granted |
| 10 | `assert` | 0 | 0 | *No description* |

---

