# API Reference

Packages:

- [quota.miloapis.com/v1alpha1](#quotamiloapiscomv1alpha1)

# quota.miloapis.com/v1alpha1

Resource Types:

- [AllowanceBucket](#allowancebucket)

- [ClaimCreationPolicy](#claimcreationpolicy)

- [GrantCreationPolicy](#grantcreationpolicy)

- [ResourceClaim](#resourceclaim)

- [ResourceGrant](#resourcegrant)

- [ResourceRegistration](#resourceregistration)




## AllowanceBucket
<sup><sup>[↩ Parent](#quotamiloapiscomv1alpha1 )</sup></sup>

[... existing AllowanceBucket section unchanged ...]

## ResourceRegistration
<sup><sup>[↩ Parent](#quotamiloapiscomv1alpha1 )</sup></sup>







ResourceRegistration enables quota tracking for a specific resource type.
Administrators create registrations to define measurement units, consumer relationships,
and claiming permissions.

### How It Works
- Administrators create registrations to enable quota tracking for specific resource types, including projects, resources with allocatable capacity, and now org-level feature entitlements.
- The system validates the registration and sets the "Active" condition when ready
- ResourceGrants can then allocate capacity for the registered resource type (if applicable)
- ResourceClaims can consume capacity when allowed resources are created (for Entity and Allocation registrations)
- For Feature registrations, there is no admission enforcement or claim processing: registration signals that the feature is available for the organization.

### Core Relationships
- **ResourceGrant.spec.allowances[].resourceType** must match this registration's **spec.resourceType**
- **ResourceClaim.spec.requests[].resourceType** must match this registration's **spec.resourceType** (for Entity and Allocation types; not applicable for Feature type)
- **ResourceClaim.spec.consumerRef** must match this registration's **spec.consumerType** type
- **ResourceClaim.spec.resourceRef** kind must be listed in this registration's **spec.claimingResources** (not applicable for Feature type)

### Registration Lifecycle
1. **Creation**: Administrator creates **ResourceRegistration** with resource type and consumer type and selects a registration type: Entity, Allocation, or Feature
2. **Validation**: System validates that referenced resource types exist and are accessible
3. **Activation**: System sets `Active=True` condition when validation passes
4. **Operation**:
    - For Entity and Allocation types, **ResourceGrants** and **ResourceClaims** can reference the active registration
    - For Feature type, grants simply register entitlement; claims and admission enforcement do not apply
5. **Updates**: Only mutable fields (`description`, `claimingResources`) can be changed

### Status Conditions
- **Active=True**: Registration is validated and operational; grants and claims can use it (or entitlement is signaled for Feature)
- **Active=False, reason=ValidationFailed**: Configuration errors prevent activation (check message)
- **Active=False, reason=RegistrationPending**: Quota system is processing the registration

### Measurement Types
- **Entity registrations** (`spec.type=Entity`): Count discrete resource instances (**Projects**, **Users**). Claims and grants reference this resource type for tracking allowed entities.
- **Allocation registrations** (`spec.type=Allocation`): Measure capacity amounts (CPU, memory, storage). Claims may request specific capacities, and grants allocate numeric capacity.
- **Feature registrations** (`spec.type=Feature`): Org-level boolean entitlement grants for feature flags. No resource amount or numeric capacity is tracked. There is no claim processing or admission enforcement for features; the registration simply signals a feature is granted or not. Grants record the availability of a feature for an organization. Use this for enabling/disabling feature flags on a per-org basis.

### Field Constraints and Limits
- Maximum 20 entries in **spec.claimingResources**
- **spec.resourceType**, **spec.consumerType**, and **spec.type** are immutable after creation
- **spec.description** maximum 500 characters
- **spec.baseUnit** and **spec.displayUnit** maximum 50 characters each
- **spec.unitConversionFactor** minimum value is 1

### Selectors and Filtering
- **Field selectors**: spec.consumerType.kind, spec.consumerType.apiGroup, spec.resourceType
- **Recommended labels** (add manually):
  - quota.miloapis.com/resource-kind: Project
  - quota.miloapis.com/resource-apigroup: resourcemanager.miloapis.com
  - quota.miloapis.com/consumer-kind: Organization

### Security Considerations
- Only include trusted resource types in **spec.claimingResources**
- Registrations are cluster-scoped and affect quota system-wide
- Consumer types must have appropriate RBAC permissions to create claims

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>apiVersion</b></td>
        <td>string</td>
        <td>quota.miloapis.com/v1alpha1</td>
        <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>ResourceRegistration</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#resourceregistrationspec">spec</a></b></td>
        <td>object</td>
        <td>
          ResourceRegistrationSpec defines the desired state of ResourceRegistration.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#resourceregistrationstatus">status</a></b></td>
        <td>object</td>
        <td>
          ResourceRegistrationStatus reports the registration's operational state and processing status.
The system updates status conditions to indicate whether the registration is active and
usable for quota operations.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceRegistration.spec
<sup><sup>[↩ Parent](#resourceregistration)</sup></sup>



ResourceRegistrationSpec defines the desired state of ResourceRegistration.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>baseUnit</b></td>
        <td>string</td>
        <td>
          BaseUnit defines the internal measurement unit for all quota calculations.
The system stores and processes all quota amounts using this unit.
Use singular form with lowercase letters. Maximum 50 characters.

Examples:
- "project" (for Entity type tracking Projects)
- "millicore" (for CPU allocation)
- "byte" (for storage or memory)
- "user" (for Entity type tracking Users)
- "feature" (for Feature type tracking feature entitlements)
<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#resourceregistrationspecclaimingresourcesindex">claimingResources</a></b></td>
        <td>[]object</td>
        <td>
          ClaimingResources specifies which resource types can create ResourceClaims for this registration.
Only resources listed here can trigger quota consumption for this resource type.
At least one claiming resource must be specified.
Maximum 20 entries.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#resourceregistrationspecconsumertype">consumerType</a></b></td>
        <td>object</td>
        <td>
          ConsumerType specifies which resource type receives grants and creates claims for this registration.
The consumer type must exist in the cluster before creating the registration.

Example: When registering "Projects per Organization", set `ConsumerType` to **Organization**
(apiGroup: `resourcemanager.miloapis.com`, kind: `Organization`). **Organizations** then
receive **ResourceGrants** allocating **Project** quota and create **ResourceClaims** when **Projects** are created.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>displayUnit</b></td>
        <td>string</td>
        <td>
          DisplayUnit defines the unit shown in user interfaces and API responses.
Should be more human-readable than BaseUnit. Use singular form. Maximum 50 characters.

Examples:
- "project" (same as BaseUnit when no conversion needed)
- "core" (for displaying CPU instead of millicores)
- "GiB" (for displaying memory/storage instead of bytes)
- "TB" (for large storage volumes)
- "features" (for Feature registrations)
<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>resourceType</b></td>
        <td>string</td>
        <td>
          ResourceType identifies the resource to track with quota.
Platform administrators define resource type identifiers that make sense for their
quota system usage. This field is immutable after creation.

The identifier format is flexible to accommodate various naming conventions
and organizational needs. Service providers can use any meaningful identifier.

Examples:
- "resourcemanager.miloapis.com/projects"
- "iam.miloapis.com/users"
- "compute_cpu"
- "storage.volumes"
- "custom-service-quota"
- "test.validation.miloapis.com/feature-resources" (for Feature)
<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>enum</td>
        <td>
          Type specifies the measurement method for quota tracking.
This field is immutable after creation.

Valid values:
- `Entity`: Counts discrete resource instances. Use for resources where each instance
  consumes exactly 1 quota unit (for example, **Projects**, **Users**, **Databases**).
  Claims always request integer quantities.
- `Allocation`: Measures numeric capacity or resource amounts. Use for resources
  with variable consumption (for example, CPU millicores, memory bytes, storage capacity).
  Claims can request fractional amounts based on resource specifications.
- `Feature`: A boolean entitlement grant used for organization-level feature flags. No numeric
  capacity is tracked, and no claim or admission enforcement is performed. The registration simply signals that a feature is available to an organization. Grants using this registration represent an on/off entitlement rather than allocatable quota.
<br/>
          <br/>
            <i>Enum</i>: Entity, Allocation, Feature<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>unitConversionFactor</b></td>
        <td>integer</td>
        <td>
          UnitConversionFactor converts BaseUnit values to DisplayUnit values for presentation.
Must be a positive integer. Minimum value is 1 (no conversion).

Formula: displayValue = baseValue / unitConversionFactor

Examples:
- 1 (no conversion: "project" to "project")
- 1000 (millicores to cores: 2000 millicores displays as 2 cores)
- 1073741824 (bytes to GiB: 2147483648 bytes displays as 2 GiB)
- 1000000000000 (bytes to TB: 2000000000000 bytes displays as 2 TB)
- 1 (for features: no conversion needed)
<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 1<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>description</b></td>
        <td>string</td>
        <td>
          Description provides human-readable context about what this registration tracks.
Use clear, specific language that explains the resource type and measurement approach.
Maximum 500 characters.

Examples:
- "Projects created within Organizations"
- "CPU millicores allocated to workloads"
- "Storage bytes claimed by volume requests"
- "Feature flag X enabled for organizations" (for Feature registrations)
<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>

[... remainder of the ResourceRegistration and API documentation unchanged ...]
