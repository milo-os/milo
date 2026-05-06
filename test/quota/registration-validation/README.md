# Test: `resource-registration-validation`

End-to-end tests for ResourceRegistration validation including:
- OpenAPI schema validation (required fields, patterns, constraints)
- CEL immutability validation
- Admission plugin duplicate detection in claimingResources
- Cross-resource duplicate detection via admission plugin


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [create-valid-registration](#step-create-valid-registration) | 0 | 3 | 0 | 0 | 0 |
| 1a | [create-valid-feature-registration](#step-create-valid-feature-registration) | 0 | 2 | 0 | 0 | 0 |
| 2 | [test-missing-required-fields](#step-test-missing-required-fields) | 0 | 1 | 0 | 0 | 0 |
| 3 | [test-invalid-type-enum](#step-test-invalid-type-enum) | 0 | 1 | 0 | 0 | 0 |
| 4 | [test-invalid-conversion-factor](#step-test-invalid-conversion-factor) | 0 | 1 | 0 | 0 | 0 |
| 5 | [test-duplicate-claiming-resources](#step-test-duplicate-claiming-resources) | 0 | 1 | 0 | 0 | 0 |
| 6 | [test-cross-resource-duplicate](#step-test-cross-resource-duplicate) | 0 | 3 | 0 | 0 | 0 |
| 7 | [test-immutable-resource-type](#step-test-immutable-resource-type) | 0 | 3 | 0 | 0 | 0 |
| 8 | [test-immutable-consumer-type-ref](#step-test-immutable-consumer-type-ref) | 0 | 1 | 0 | 0 | 0 |
| 9 | [test-immutable-type](#step-test-immutable-type) | 0 | 1 | 0 | 0 | 0 |
| 10 | [test-valid-update](#step-test-valid-update) | 0 | 2 | 0 | 0 | 0 |
| 11 | [test-max-claiming-resources](#step-test-max-claiming-resources) | 0 | 1 | 0 | 0 | 0 |

### Step: `create-valid-registration`

Create a valid ResourceRegistration and verify it becomes Active

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |

### Step: `create-valid-feature-registration`

Verify that a ResourceRegistration with type=`Feature` is accepted and reaches Active=True. This test covers the new 'Feature' type available on resource registrations, ensuring proper validation and signaling for feature flag entitlements.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `test-missing-required-fields`

Verify that ResourceRegistrations without required fields are rejected

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `test-invalid-type-enum`

Verify that invalid enum values for type field are rejected

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `test-invalid-conversion-factor`

Verify that unitConversionFactor below minimum value is rejected

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `test-duplicate-claiming-resources`

Verify that duplicate entries in claimingResources array are rejected

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `test-cross-resource-duplicate`

Verify that registering the same resourceType twice across different objects is rejected

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `create` | 0 | 0 | *No description* |

### Step: `test-immutable-resource-type`

Verify that resourceType field cannot be modified after creation (CEL validation)

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `patch` | 0 | 0 | *No description* |

### Step: `test-immutable-consumer-type-ref`

Verify that consumerType field cannot be modified after creation (CEL validation)

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `patch` | 0 | 0 | *No description* |

### Step: `test-immutable-type`

Verify that type field cannot be modified after creation (CEL validation)

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `patch` | 0 | 0 | *No description* |

### Step: `test-valid-update`

Verify that mutable fields can be successfully updated

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `patch` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `test-max-claiming-resources`

Verify that exceeding maximum claimingResources array size is rejected

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

---

 
