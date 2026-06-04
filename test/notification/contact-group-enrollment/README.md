# Test: `contact-group-enrollment`

End-to-end tests for the ContactGroupEnrollmentController.

This test verifies the following scenarios:
1. A Contact with a User SubjectRef is automatically enrolled into a ContactGroup
   when a matching ContactGroupEnrollmentPolicy exists.
2. A Contact without a User SubjectRef is NOT enrolled (selector filtering).
3. A Contact with a pre-existing ContactGroupMembershipRemoval is NOT enrolled
   (opt-out is honored).
4. Enrollment is idempotent — a second reconciliation does not create duplicate
   ContactGroupMembership records.
5. An existing Contact is enrolled retroactively when a new
   ContactGroupEnrollmentPolicy is applied (the policy watcher triggers re-evaluation).
6. Full pipeline: creating a User triggers UserContactController to create a Contact,
   which in turn triggers enrollment via ContactGroupEnrollmentPolicy.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-contact-group](#step-setup-contact-group) | 0 | 2 | 0 | 0 | 0 |
| 2 | [setup-basic-policy](#step-setup-basic-policy) | 0 | 2 | 0 | 0 | 0 |
| 3 | [setup-test-users](#step-setup-test-users) | 0 | 5 | 0 | 0 | 0 |
| 4 | [enroll-user-contact](#step-enroll-user-contact) | 0 | 2 | 1 | 0 | 0 |
| 5 | [verify-enrollment-annotation](#step-verify-enrollment-annotation) | 0 | 1 | 0 | 0 | 0 |
| 6 | [no-enrollment-for-bare-contact](#step-no-enrollment-for-bare-contact) | 0 | 4 | 0 | 0 | 0 |
| 7 | [setup-opt-out-removal](#step-setup-opt-out-removal) | 0 | 6 | 0 | 0 | 0 |
| 8 | [no-enrollment-when-opted-out](#step-no-enrollment-when-opted-out) | 0 | 4 | 0 | 0 | 0 |
| 9 | [enrollment-is-idempotent](#step-enrollment-is-idempotent) | 0 | 2 | 0 | 0 | 0 |
| 10 | [create-contact-before-policy](#step-create-contact-before-policy) | 0 | 3 | 0 | 0 | 0 |
| 11 | [apply-retroactive-policy](#step-apply-retroactive-policy) | 0 | 4 | 1 | 0 | 0 |
| 12 | [full-pipeline-user-signup](#step-full-pipeline-user-signup) | 0 | 4 | 1 | 0 | 0 |
| 13 | [cleanup](#step-cleanup) | 0 | 1 | 0 | 0 | 0 |

### Step: `setup-contact-group`

Create the ContactGroup used across all scenarios

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `setup-basic-policy`

Create the basic ContactGroupEnrollmentPolicy targeting User contacts

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `setup-test-users`

Create Users whose auto-created Contacts are used in subsequent scenarios

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |
| 5 | `wait` | 0 | 0 | *No description* |

### Step: `enroll-user-contact`

Verify the auto-created Contact for enrollment-test-user is enrolled into the ContactGroup

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `wait` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

#### Catch

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `verify-enrollment-annotation`

Verify the enrollment annotation is written to the Contact after evaluation

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `no-enrollment-for-bare-contact`

Create a Contact without a SubjectRef and verify no membership is created

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `script` | 0 | 0 | *No description* |
| 4 | `error` | 0 | 0 | *No description* |

### Step: `setup-opt-out-removal`

Create the opted-out User, wait for its Contact, then create the removal.
Uses a dedicated ContactGroup (enrollment-opt-out-test-group) so the opt-out
test policy can create memberships without conflicting with enrollment-test-group
memberships from the basic policy.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |
| 5 | `apply` | 0 | 0 | *No description* |
| 6 | `assert` | 0 | 0 | *No description* |

### Step: `no-enrollment-when-opted-out`

Apply a new enrollment policy AFTER the removal is in place. The controller
evaluates user-enrollment-opted-out-user against this policy for the first time
with the removal already present, so enrollment is skipped.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `script` | 0 | 0 | *No description* |
| 4 | `error` | 0 | 0 | *No description* |

### Step: `enrollment-is-idempotent`

Re-triggering reconciliation on an already-enrolled Contact does not create duplicates

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |
| 2 | `script` | 0 | 0 | *No description* |

### Step: `create-contact-before-policy`

Assert the Contact for enrollment-existing-user exists before the retroactive
policy is applied, and create the dedicated group the retroactive policy targets.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |

### Step: `apply-retroactive-policy`

Apply a new policy and verify existing contacts are enrolled retroactively

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `script` | 0 | 0 | *No description* |
| 4 | `assert` | 0 | 0 | *No description* |

#### Catch

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `full-pipeline-user-signup`

Create a User and verify the full enrollment chain:
  User → (UserContactController) → Contact → (ContactGroupEnrollmentController) → ContactGroupMembership


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `script` | 0 | 0 | *No description* |
| 4 | `assert` | 0 | 0 | *No description* |

#### Catch

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `cleanup`

Remove all resources created by this test

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

---

