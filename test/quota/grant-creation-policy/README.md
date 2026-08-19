# Test: `quota-grant-creation-policy`

Tests GrantCreationPolicy functionality for automatic ResourceGrant generation.

This test verifies:
- GrantCreationPolicies automatically create ResourceGrants when matching resources are created
- CEL template expressions correctly render grant specifications
- CEL name expressions generate dynamic grant names
- Condition evaluation updates grants when trigger resources change
- Owner references ensure grants are cleaned up when triggers are deleted
- Production-style label validation works correctly
- Invalid policy configurations are rejected


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-base-infrastructure](#step-setup-base-infrastructure) | 0 | 3 | 0 | 0 | 0 |
| 2 | [setup-grant-creation-policy](#step-setup-grant-creation-policy) | 0 | 3 | 0 | 0 | 0 |
| 3 | [setup-test-organization](#step-setup-test-organization) | 0 | 6 | 0 | 0 | 0 |
| 4 | [verify-automatic-grant-creation](#step-verify-automatic-grant-creation) | 0 | 2 | 0 | 0 | 0 |
| 5 | [test-condition-evaluation](#step-test-condition-evaluation) | 0 | 3 | 0 | 0 | 0 |
| 6 | [test-template-rendering](#step-test-template-rendering) | 0 | 4 | 0 | 0 | 0 |
| 7 | [test-cel-name-expression](#step-test-cel-name-expression) | 0 | 6 | 0 | 0 | 0 |
| 8 | [test-grant-cleanup](#step-test-grant-cleanup) | 0 | 3 | 0 | 0 | 0 |
| 9 | [test-policy-validation](#step-test-policy-validation) | 0 | 2 | 0 | 0 | 0 |
| 10 | [test-production-style-labels](#step-test-production-style-labels) | 0 | 3 | 0 | 0 | 0 |
| 11 | [test-namespace-template-grant](#step-test-namespace-template-grant) | 0 | 6 | 0 | 0 | 0 |

### Step: `setup-base-infrastructure`

Register the resource type for grant creation testing

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |

### Step: `setup-grant-creation-policy`

Create GrantCreationPolicy that automatically generates ResourceGrants for Organizations.
The policy uses CEL templates to compute grant amounts based on organization attributes.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |

### Step: `setup-test-organization`

Create an Organization that matches the GrantCreationPolicy selector.
This should trigger automatic ResourceGrant creation.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | Remove any leftover test organization from earlier runs |
| 2 | `delete` | 0 | 0 | Ensure previous organization namespace is cleaned up |
| 3 | `wait` | 0 | 0 | Wait for organization namespace to be fully removed |
| 4 | `apply` | 0 | 0 | *No description* |
| 5 | `wait` | 0 | 0 | *No description* |
| 6 | `assert` | 0 | 0 | *No description* |

### Step: `verify-automatic-grant-creation`

Verify that ResourceGrant was automatically created by the GrantCreationController.
The grant should be Active and have the correct spec from the policy template.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `wait` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `test-condition-evaluation`

Update the Organization's attributes to trigger grant recalculation.
The policy should update the grant based on the new CEL template evaluation.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |

### Step: `test-template-rendering`

Create an Organization with different attributes (premium tier).
Verify that the policy generates a grant with different values based on CEL template evaluation.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |
| 4 | `assert` | 0 | 0 | *No description* |

### Step: `test-cel-name-expression`

Test GrantCreationPolicy with CEL expression for dynamic grant naming.
The grant name should be generated from CEL template evaluation.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |
| 5 | `wait` | 0 | 0 | *No description* |
| 6 | `assert` | 0 | 0 | *No description* |

### Step: `test-grant-cleanup`

Verify that ResourceGrants are automatically deleted when their trigger resource is removed.
This validates owner reference-based garbage collection.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |

### Step: `test-policy-validation`

Test that invalid GrantCreationPolicy configurations are rejected by validation.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `test-production-style-labels`

Test that GrantCreationPolicy accepts production-style label selectors with prefixes.
This validates a bug fix for label validation in CEL expressions.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |

### Step: `test-namespace-template-grant`

Test that GrantCreationPolicy can create grants in organization-specific namespaces
using namespace templates like 'organization-{{.trigger.metadata.name}}'.
This validates the fix for namespace template rendering in grant creation.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |
| 5 | `wait` | 0 | 0 | *No description* |
| 6 | `assert` | 0 | 0 | *No description* |

---

