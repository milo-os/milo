# Test: `groupmembership-user-email`

Verifies the member email annotation on a GroupMembership tracks the
referenced User's email address.

This test verifies:
- A GroupMembership created for a User that has an email address is
  annotated with iam.miloapis.com/user-email at admission, so the create
  audit event carries the member's email.
- After the referenced User changes their email address, the GroupMembership
  controller refreshes the annotation, so the value never goes stale.

Two components maintain the annotation because the create audit event carries
no status -- the API server drops status on create and an admission webhook
cannot populate a status subresource -- and the activity line for "added X to
group Y" is built from that event. The mutating webhook therefore stamps the
annotation at admission, and the GroupMembership controller owns it from then
on by watching Users.

This suite carries no `requires` label, so the default end-to-end selector
(`requires!=authorization-provider`) runs it. It touches no
authorization-provider backed resources.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [stamp-email-at-admission](#step-stamp-email-at-admission) | 0 | 4 | 0 | 0 | 0 |
| 2 | [refresh-email-on-user-change](#step-refresh-email-on-user-change) | 0 | 2 | 0 | 0 | 0 |

### Step: `stamp-email-at-admission`

A GroupMembership for a User with an email is annotated at admission

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `assert` | 0 | 0 | *No description* |

### Step: `refresh-email-on-user-change`

Changing the User's email refreshes the annotation on the membership

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

---

