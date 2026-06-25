# Test: `quota-manual-claim-preservation`

Verifies that manually created ResourceClaims are not garbage-collected by the
ResourceClaimOwnershipController (regression test for issue #642).

Before the fix, the controller incorrectly processed any ResourceClaim with
Granted=True and no owner references, including service-managed claims whose
owning resource lives in a different control plane. It would enter a continuous
delete-recreate loop once the orphan grace period elapsed.

This test creates a ResourceClaim without the admission-plugin auto-created
markers (quota.miloapis.com/auto-created=true and
quota.miloapis.com/created-by=claim-creation-plugin) and a resourceRef that
points to a non-existent Project. Before the fix, the controller would delete
this claim after the orphan max-age (default 30s). After the fix, the controller
skips it entirely because the auto-created markers are absent.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-resource-registration](#step-setup-resource-registration) | 0 | 2 | 0 | 0 | 0 |
| 2 | [setup-organization](#step-setup-organization) | 0 | 2 | 0 | 0 | 0 |
| 3 | [setup-resource-grant](#step-setup-resource-grant) | 0 | 2 | 0 | 0 | 0 |
| 4 | [create-manual-claim-and-wait-for-grant](#step-create-manual-claim-and-wait-for-grant) | 0 | 2 | 0 | 0 | 0 |
| 5 | [verify-claim-not-garbage-collected](#step-verify-claim-not-garbage-collected) | 0 | 2 | 0 | 0 | 0 |

### Step: `setup-resource-registration`

Register the resource type used by this test. The registration must be active
before the quota system can evaluate claims for this resource type.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | Create ResourceRegistration for the test quota type |
| 2 | `wait` | 0 | 0 | Wait for ResourceRegistration to become active |

### Step: `setup-organization`

Create the Organization that acts as the quota consumer. The ownership
controller runs across all control planes, so this step uses the main cluster.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | Create test Organization |
| 2 | `wait` | 0 | 0 | Wait for the Organization namespace to become active |

### Step: `setup-resource-grant`

Grant quota to the Organization so the manually created claim can be
evaluated and granted. Without available quota the claim would be denied
rather than granted, and the ownership controller would not process it.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | Create ResourceGrant for the test Organization |
| 2 | `wait` | 0 | 0 | Wait for ResourceGrant to become active |

### Step: `create-manual-claim-and-wait-for-grant`

Create a ResourceClaim that is missing the auto-created label and annotation
normally set by the admission plugin. The resourceRef points to a Project that
does not exist — without the fix this is what triggers the ownership controller
to eventually delete the claim after the orphan grace period elapses.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | Create manual ResourceClaim without auto-created markers |
| 2 | `wait` | 0 | 0 | Wait for the claim to reach Granted=True |

### Step: `verify-claim-not-garbage-collected`

Sleep past the ownership controller's default orphan max-age threshold (30s)
then assert the claim still exists with Granted=True. Before issue #642 was
fixed, the controller would have deleted this claim because it had no owner
references and its resourceRef pointed to a non-existent Project.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `sleep` | 0 | 0 | Wait past the default orphan max-age (30s) |
| 2 | `wait` | 0 | 0 | Assert the manually created claim was not garbage-collected |

---

