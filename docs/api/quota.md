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






**AllowanceBucket** aggregates quota limits and usage for a single (consumer, resourceType) combination.
The system automatically creates buckets to provide real-time quota availability information
for **ResourceClaim** evaluation during admission.

### How It Works
1. **Auto-Creation**: Quota system creates buckets automatically for each unique (consumer, resourceType) pair found in active **ResourceGrants**
2. **Aggregation**: Quota system continuously aggregates capacity from active **ResourceGrants** and consumption from granted **ResourceClaims**
3. **Decision Support**: Quota system uses bucket `status.available` to determine if **ResourceClaims** can be granted
4. **Updates**: Quota system updates bucket status whenever contributing grants or claims change

### Aggregation Logic
**AllowanceBuckets** serve as the central aggregation point where quota capacity meets quota consumption.
The quota system continuously scans for **ResourceGrants** that match both the bucket's consumer
and resource type, but only considers grants with an `Active` status condition. For each qualifying
grant, the quota system examines all allowances targeting the bucket's resource type and sums the
amounts from every bucket within those allowances. This sum becomes the bucket's limit - the total
quota capacity available to the consumer for that specific resource type.

Simultaneously, the quota system tracks quota consumption by finding all **ResourceClaims** with matching
consumer and resource type specifications. However, only claims that have been successfully granted
contribute to the allocated total. The quota system sums the allocated amounts from all granted
requests, creating a running total of consumed quota capacity.

The available quota emerges from this simple relationship: Available = Limit - Allocated. The
system ensures this value never goes negative, treating any calculated negative as zero. This
available amount represents the quota capacity remaining for new **ResourceClaims** and drives
real-time admission decisions throughout the cluster.

### Real-Time Admission Decisions
When a **ResourceClaim** is created:
1. Quota system identifies the relevant bucket (matching consumer and resource type)
2. Compares requested amount with bucket's `status.available`
3. Grants claim if requested amount <= available capacity
4. Denies claim if requested amount > available capacity
5. Updates bucket status to reflect the new allocation (if granted)

### Bucket Lifecycle
1. **Auto-Created**: When first ResourceGrant creates allowance for (consumer, resourceType)
2. **Active**: Continuously aggregated while ResourceGrants or ResourceClaims exist
3. **Updated**: Status refreshed whenever contributing resources change
4. **Persistent**: Buckets remain even when limit drops to 0 (for monitoring)

### Consistency and Performance
**Eventual Consistency:**
- Status may lag briefly after ResourceGrant or ResourceClaim changes
- Controller processes updates asynchronously for performance
- LastReconciliation timestamp indicates data freshness

**Scale Optimization:**
- Stores aggregates (limit, allocated, available) rather than individual entries
- ContributingGrantRefs tracks grants (few) but not claims (many)
- Single bucket per (consumer, resourceType) regardless of claim count

### Status Information
- **Limit**: Total quota capacity from all contributing ResourceGrants
- **Allocated**: Total quota consumed by all granted ResourceClaims
- **Available**: Remaining quota capacity (Limit - Allocated)
- **ClaimCount**: Number of granted claims consuming from this bucket
- **GrantCount**: Number of active grants contributing to this bucket
- **ContributingGrantRefs**: Detailed information about contributing grants

### Monitoring and Troubleshooting
**Quota Monitoring:**
- Monitor status.available to track quota usage trends
- Check status.allocated vs status.limit for utilization ratios
- Use status.claimCount to understand resource creation patterns

**Troubleshooting Issues:**
When investigating quota problems, start with the bucket's limit value. A limit of zero typically
indicates that no ResourceGrants are contributing capacity for this consumer and resource type
combination. Verify that ResourceGrants exist with matching consumer and resource type specifications,
and confirm their status conditions show Active=True. Grants with validation failures or pending
states won't contribute to bucket limits.

High allocation values relative to limits suggest quota consumption issues. Review the ResourceClaims
that match this bucket's consumer and resource type to identify which resources are consuming large
amounts of quota. Check the claim allocation details to understand consumption patterns and identify
potential quota leaks where claims aren't being cleaned up properly.

Stale bucket data manifests as allocation or limit values that don't reflect recent changes to
grants or claims. Check the lastReconciliation timestamp to determine data freshness, then examine
quota system logs for aggregation errors or performance issues. The quota system should process
changes within seconds under normal conditions.

### System Architecture
- **Single Writer**: Only the quota system updates bucket status (prevents races)
- **Dedicated Processing**: Separate components focus solely on bucket aggregation
- **Event-Driven**: Responds to ResourceGrant and ResourceClaim changes
- **Efficient Queries**: Uses indexes and field selectors for fast aggregation

### Selectors and Filtering
- **Field selectors**: spec.consumerRef.kind, spec.consumerRef.name, spec.resourceType
- **System labels** (set automatically by quota system):
  - quota.miloapis.com/consumer-kind: Organization
  - quota.miloapis.com/consumer-name: acme-corp

### Common Queries
- All buckets for a consumer: label selector quota.miloapis.com/consumer-kind + quota.miloapis.com/consumer-name
- All buckets for a resource type: field selector spec.resourceType=<value>
- Specific bucket: field selector spec.consumerRef.name + spec.resourceType
- Overutilized buckets: filter by status.available < threshold
- Empty buckets: filter by status.limit = 0

### Performance Considerations
- Bucket status updates are asynchronous and may lag resource changes
- Large numbers of ResourceClaims can impact aggregation performance
- Controller uses efficient aggregation queries to handle scale
- Status updates are batched to reduce API server load

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
      <td>AllowanceBucket</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#allowancebucketspec">spec</a></b></td>
        <td>object</td>
        <td>
          AllowanceBucketSpec defines the desired state of AllowanceBucket.
The system automatically creates buckets for each unique (consumer, resourceType) combination
found in active ResourceGrants.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#allowancebucketstatus">status</a></b></td>
        <td>object</td>
        <td>
          AllowanceBucketStatus contains the quota system-computed quota aggregation for a specific
(consumer, resourceType) combination. The quota system continuously updates this status
by aggregating capacity from active ResourceGrants and consumption from granted ResourceClaims.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### AllowanceBucket.spec
<sup><sup>[↩ Parent](#allowancebucket)</sup></sup>



AllowanceBucketSpec defines the desired state of AllowanceBucket.
The system automatically creates buckets for each unique (consumer, resourceType) combination
found in active ResourceGrants.

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
        <td><b><a href="#allowancebucketspecconsumerref">consumerRef</a></b></td>
        <td>object</td>
        <td>
          ConsumerRef identifies the quota consumer tracked by this bucket.
Must match the ConsumerRef from ResourceGrants that contribute to this bucket.
Only one bucket exists per unique (ConsumerRef, ResourceType) combination.

Examples:
- Organization "acme-corp" consuming Project quota
- Project "web-app" consuming User quota
- Organization "enterprise-corp" consuming storage quota<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>resourceType</b></td>
        <td>string</td>
        <td>
          ResourceType specifies which resource type this bucket aggregates quota for.
Must exactly match a ResourceRegistration.spec.resourceType that is currently active.
The quota system validates this reference and only creates buckets for registered types.

The identifier format is flexible, as defined by platform administrators
in their ResourceRegistrations.

Examples:
- "resourcemanager.miloapis.com/projects"
- "compute_cpu"
- "storage.volumes"
- "custom-service-quota"<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### AllowanceBucket.spec.consumerRef
<sup><sup>[↩ Parent](#allowancebucketspec)</sup></sup>



ConsumerRef identifies the quota consumer tracked by this bucket.
Must match the ConsumerRef from ResourceGrants that contribute to this bucket.
Only one bucket exists per unique (ConsumerRef, ResourceType) combination.

Examples:
- Organization "acme-corp" consuming Project quota
- Project "web-app" consuming User quota
- Organization "enterprise-corp" consuming storage quota

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
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind specifies the type of consumer resource.
Must match an existing Kubernetes resource type that can receive quota grants.

Common consumer types:
- "Organization" (top-level quota consumer)
- "Project" (project-level quota consumer)
- "User" (user-level quota consumer)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name identifies the specific consumer resource instance.
Must match the name of an existing consumer resource in the cluster.

Examples:
- "acme-corp" (Organization name)
- "web-application" (Project name)
- "john.doe" (User name)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup specifies the API group of the consumer resource.
Use full group name for Milo resources.

Examples:
- "resourcemanager.miloapis.com" (Organization/Project resources)
- "iam.miloapis.com" (User/Group resources)
- "infrastructure.miloapis.com" (infrastructure resources)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace identifies the namespace of the consumer resource.
Required for namespaced consumer resources (e.g., Projects).
Leave empty for cluster-scoped consumer resources (e.g., Organizations).

Examples:
- "" (empty for cluster-scoped Organizations)
- "organization-acme-corp" (namespace for Projects within an organization)
- "project-web-app" (namespace for resources within a project)<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### AllowanceBucket.status
<sup><sup>[↩ Parent](#allowancebucket)</sup></sup>



AllowanceBucketStatus contains the quota system-computed quota aggregation for a specific
(consumer, resourceType) combination. The quota system continuously updates this status
by aggregating capacity from active ResourceGrants and consumption from granted ResourceClaims.

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
        <td><b>allocated</b></td>
        <td>integer</td>
        <td>
          Allocated represents the total quota currently consumed by granted ResourceClaims.
Calculated by summing all allocation amounts from ResourceClaims with status.conditions[type=Granted]=True
that match the bucket's spec.consumerRef and have requests for spec.resourceType.

Aggregation logic:
- Only ResourceClaims with Granted=True contribute to allocated amount
- Only requests matching spec.resourceType are included
- All allocated amounts from matching requests are summed<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>available</b></td>
        <td>integer</td>
        <td>
          Available represents the quota capacity remaining for new ResourceClaims.
Always calculated as: Available = Limit - Allocated (never negative).
The system uses this value to determine whether new ResourceClaims can be granted.

Decision logic:
- ResourceClaim is granted if requested amount <= Available
- ResourceClaim is denied if requested amount > Available
- Multiple concurrent claims may race; first to be processed wins<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>claimCount</b></td>
        <td>integer</td>
        <td>
          ClaimCount indicates the total number of granted ResourceClaims consuming quota from this bucket.
Includes all ResourceClaims with status.conditions[type=Granted]=True that have requests
matching spec.resourceType and spec.consumerRef.

Used for monitoring quota usage patterns and identifying potential issues.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>grantCount</b></td>
        <td>integer</td>
        <td>
          GrantCount indicates the total number of active ResourceGrants contributing to this bucket's limit.
Includes all ResourceGrants with status.conditions[type=Active]=True that have allowances
matching spec.resourceType and spec.consumerRef.

Used for understanding quota source distribution and debugging capacity issues.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>limit</b></td>
        <td>integer</td>
        <td>
          Limit represents the total quota capacity available for this (consumer, resourceType) combination.
Calculated by summing all bucket amounts from active ResourceGrants that match the bucket's
spec.consumerRef and spec.resourceType. Measured in BaseUnit from the ResourceRegistration.

Aggregation logic:
- Only ResourceGrants with status.conditions[type=Active]=True contribute to the limit
- All allowances matching spec.resourceType are included from contributing grants
- All bucket amounts within matching allowances are summed<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#allowancebucketstatuscontributinggrantrefsindex">contributingGrantRefs</a></b></td>
        <td>[]object</td>
        <td>
          ContributingGrantRefs provides detailed information about each ResourceGrant that contributes
to this bucket's limit. Includes grant names, amounts, and last observed generations for
tracking and debugging quota sources.

This field provides visibility into:
- Which grants are providing quota capacity
- How much each grant contributes
- Whether grants have been updated since last bucket calculation

Grants are tracked individually because they are typically few in number compared to claims.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>lastReconciliation</b></td>
        <td>string</td>
        <td>
          LastReconciliation records when the quota system last recalculated this status.
Used for monitoring quota system health and understanding how fresh the aggregated data is.

The quota system updates this timestamp every time it processes the bucket, regardless of
whether the aggregated values changed.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          ObservedGeneration indicates the most recent spec generation the quota system has processed.
When ObservedGeneration matches metadata.generation, the status reflects the current spec.
When ObservedGeneration is lower, the quota system is still processing recent changes.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### AllowanceBucket.status.contributingGrantRefs[index]
<sup><sup>[↩ Parent](#allowancebucketstatus)</sup></sup>



ContributingGrantRef tracks a ResourceGrant that contributes capacity to this bucket.
The quota system maintains these references to provide visibility into quota sources
and to detect when grants change.

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
        <td><b>amount</b></td>
        <td>integer</td>
        <td>
          Amount specifies how much quota capacity this grant contributes to the bucket.
Represents the sum of all buckets within all allowances for the matching
resource type in the referenced grant. Measured in BaseUnit.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>lastObservedGeneration</b></td>
        <td>integer</td>
        <td>
          LastObservedGeneration records the ResourceGrant's generation when the bucket
quota system last processed it. Used to detect when grants have been updated
and the bucket needs to recalculate its aggregated limit.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name identifies the ResourceGrant that contributes to this bucket's limit.
Used for tracking quota sources and debugging allocation issues.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>

## ClaimCreationPolicy
<sup><sup>[↩ Parent](#quotamiloapiscomv1alpha1 )</sup></sup>






ClaimCreationPolicy automatically creates ResourceClaims during admission to enforce quota in real-time.
Policies intercept resource creation requests, evaluate trigger conditions, and generate
quota claims that prevent resource creation when quota limits are exceeded.

### How It Works
1. **Trigger Matching**: Admission webhook matches incoming resource creates against spec.trigger.resource
2. **Constraint Evaluation**: All CEL expressions in spec.trigger.constraints must evaluate to true
3. **Template Rendering**: Policy renders spec.target.resourceClaimTemplate using available template variables
4. **Claim Creation**: System creates the rendered ResourceClaim in the specified namespace
5. **Quota Evaluation**: Claim is immediately evaluated against AllowanceBucket capacity
6. **Admission Decision**: Original resource creation succeeds or fails based on claim result

### Policy Processing Flow
**Active Policies** (spec.disabled=false):
1. Admission webhook receives resource creation request
2. Finds all ClaimCreationPolicies matching the resource type
3. Evaluates trigger constraints for each matching policy
4. Creates ResourceClaim for each policy where all constraints are true
5. Evaluates all created claims against quota buckets
6. Allows resource creation only if all claims are granted

**Disabled Policies** (spec.disabled=true):
- Completely ignored during admission processing
- No constraints evaluated, no claims created
- Useful for temporarily disabling quota enforcement

### Template Expressions
Template expressions generate dynamic content for ResourceClaim fields including metadata and specification.
Content inside `{{ }}` delimiters is evaluated as CEL expressions, while content outside is treated as literal text.

**Template Expression Rules:**
- `{{expression}}` - Pure CEL expression, evaluated and substituted
- `literal-text` - Used as-is without any evaluation
- `{{expression}}-literal` - CEL output combined with literal text
- `prefix-{{expression}}-suffix` - Literal text surrounding CEL expression

**Template Expression Examples:**
- `{{trigger.metadata.name + '-claim'}}` - Pure CEL expression (metadata)
- `{{trigger.metadata.name}}-quota-claim` - CEL + literal suffix (metadata)
- `{{trigger.spec.organization}}` - Extract spec field for consumer name (spec)
- `{{trigger.metadata.labels["tier"] + "-tier"}}` - Label-based naming (spec)
- `fixed-claim-name` - Literal string only (no evaluation)

**Use Template Expressions For:** ResourceClaimTemplate fields (metadata and spec)

### Constraint Expressions
Constraint expressions determine whether a policy should trigger by evaluating boolean conditions.
These are pure CEL expressions without delimiters that must return true/false values.

**Constraint Expression Rules:**
- Write pure CEL expressions directly (no wrapping syntax)
- Must return boolean values (true = trigger policy, false = skip)
- All constraints in a policy must return true for the policy to activate

**Constraint Expression Examples:**
- `trigger.spec.tier == "premium"` - Field equality check
- `trigger.metadata.labels["environment"] == "prod"` - Label-based filtering
- `user.groups.exists(g, g == "admin")` - User authorization check
- `has(trigger.spec.quotaProfile)` - Field existence check

**Use Constraint Expressions For:** spec.trigger.constraints fields

### Expression Variables
Both template and constraint expressions have access to the same context variables:

**trigger**: The complete resource that triggered the policy, including all metadata, spec,
and status fields. Navigate using CEL property access: `trigger.metadata.name`, `trigger.spec.replicas`.

**user**: Authentication context providing access to the requester's name, unique identifier,
group memberships, and additional attributes. Enables user-based quota policies.

**requestInfo**: Operational context including the API verb being performed and resource type
being manipulated. Useful for distinguishing between create, update, and delete operations.

**CEL Functions**: Standard CEL functions available for data manipulation including conditional
expressions (`condition ? value1 : value2`), string methods (`lowerAscii()`, `upperAscii()`, `trim()`),
and collection operations (`exists()`, `all()`, `filter()`).

### Consumer Resolution
The system automatically resolves spec.consumerRef for created claims:
- Uses parent context resolution to find the appropriate consumer
- Typically resolves to Organization for Project resources, Project for User resources, etc.
- Consumer must match the ResourceRegistration.spec.consumerType for the requested resource type

### Validation and Dependencies
**Policy Validation:**
- Target resource type must exist and be accessible
- All resource types in claim specification must have active ResourceRegistrations
- Consumer resolution must be resolvable for target resources
- CEL expressions must be syntactically valid

**Runtime Dependencies:**
- ResourceRegistration must be Active for each requested resource type
- Triggering resource kind must be listed in ResourceRegistration.spec.claimingResources
- AllowanceBucket must exist (created automatically when ResourceGrants are active)

### Policy Lifecycle
1. **Creation**: Administrator creates ClaimCreationPolicy
2. **Validation**: System validates target resource and expressions
3. **Activation**: System sets Ready=True when validation passes
4. **Operation**: Admission webhook uses active policies to create claims
5. **Updates**: Changes trigger re-validation; only Ready policies are used

### Status Conditions
- **Ready=True**: Policy is validated and actively creating claims
- **Ready=False, reason=ValidationFailed**: Configuration errors prevent activation (check message)
- **Ready=False, reason=PolicyDisabled**: Policy is disabled (spec.disabled=true)

### Automatic Claim Features
Claims created by ClaimCreationPolicy include:
- **Standard Labels**: quota.miloapis.com/auto-created=true, quota.miloapis.com/policy=<policy-name>
- **Standard Annotations**: quota.miloapis.com/created-by=claim-creation-plugin, timestamps
- **Owner References**: Set to triggering resource when possible for lifecycle management
- **Cleanup**: Automatically cleaned up when denied to prevent accumulation

### Field Constraints and Limits
- Maximum 10 constraints per trigger (spec.trigger.constraints)
- Static amounts only in v1alpha1 (no expression-based quota amounts)
- Template metadata labels are literal strings (no expression processing)
- Template annotation values support CEL expressions

### Selectors and Filtering
- **Field selectors**: spec.trigger.resource.kind, spec.trigger.resource.apiVersion, spec.disabled
- **Recommended labels** (add manually):
  - quota.miloapis.com/target-kind: Project
  - quota.miloapis.com/environment: production
  - quota.miloapis.com/tier: premium

### Common Queries
- All policies for a resource kind: label selector quota.miloapis.com/target-kind=<kind>
- Active policies only: field selector spec.disabled=false
- Environment-specific policies: label selector quota.miloapis.com/environment=<env>
- Failed policies: filter by status.conditions[type=Ready].status=False

### Troubleshooting
- **Policy not triggering**: Check spec.disabled=false and status.conditions[type=Ready]=True
- **Template errors**: Review status condition message for CEL expression syntax issues
- **CEL expression failures**: Validate expression syntax and available variables
- **Claims not created**: Verify trigger constraints match the incoming resource
- **Consumer resolution errors**: Check parent context resolution and ResourceRegistration setup

### Performance Considerations
- Policies are evaluated synchronously during admission (affects API latency)
- Complex CEL expressions can impact admission performance
- Template rendering occurs for every matching admission request
- Consider using specific trigger constraints to limit policy evaluation scope

### Security Considerations
- Templates can access complete trigger resource data (sensitive field exposure)
- CEL expressions have access to user information and request details
- Only trusted administrators should create or modify policies
- Review template output to ensure no sensitive data leakage in claim metadata

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
      <td>ClaimCreationPolicy</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#claimcreationpolicyspec">spec</a></b></td>
        <td>object</td>
        <td>
          ClaimCreationPolicySpec defines the desired state of ClaimCreationPolicy.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#claimcreationpolicystatus">status</a></b></td>
        <td>object</td>
        <td>
          ClaimCreationPolicyStatus defines the observed state of ClaimCreationPolicy.

Status fields
- conditions[type=Ready]: True when the policy is validated and active.

See also
- [ResourceClaim](#resourceclaim): The object created by this policy.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.spec
<sup><sup>[↩ Parent](#claimcreationpolicy)</sup></sup>



ClaimCreationPolicySpec defines the desired state of ClaimCreationPolicy.

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
        <td><b><a href="#claimcreationpolicyspectarget">target</a></b></td>
        <td>object</td>
        <td>
          Target defines how and where **ResourceClaims** should be created.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#claimcreationpolicyspectrigger">trigger</a></b></td>
        <td>object</td>
        <td>
          Trigger defines what resource changes should trigger claim creation.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>disabled</b></td>
        <td>boolean</td>
        <td>
          Disabled determines if this policy is inactive.
If true, no **ResourceClaims** will be created for matching resources.<br/>
          <br/>
            <i>Default</i>: false<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.spec.target
<sup><sup>[↩ Parent](#claimcreationpolicyspec)</sup></sup>



Target defines how and where **ResourceClaims** should be created.

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
        <td><b><a href="#claimcreationpolicyspectargetresourceclaimtemplate">resourceClaimTemplate</a></b></td>
        <td>object</td>
        <td>
          ResourceClaimTemplate defines how to create **ResourceClaims**.
String fields support CEL expressions for dynamic content.<br/>
          <br/>
            <i>Validations</i>:<li>!has(self.spec.resourceRef): resourceRef field is automatically populated and cannot be set in template</li>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.spec.target.resourceClaimTemplate
<sup><sup>[↩ Parent](#claimcreationpolicyspectarget)</sup></sup>



ResourceClaimTemplate defines how to create **ResourceClaims**.
String fields support CEL expressions for dynamic content.

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
        <td><b><a href="#claimcreationpolicyspectargetresourceclaimtemplatemetadata">metadata</a></b></td>
        <td>object</td>
        <td>
          Metadata for the created **ResourceClaim**.
String fields support CEL expressions.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#claimcreationpolicyspectargetresourceclaimtemplatespec">spec</a></b></td>
        <td>object</td>
        <td>
          Spec for the created ResourceClaim.
String fields support CEL expressions.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.spec.target.resourceClaimTemplate.metadata
<sup><sup>[↩ Parent](#claimcreationpolicyspectargetresourceclaimtemplate)</sup></sup>



Metadata for the created **ResourceClaim**.
String fields support CEL expressions.

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
        <td><b>annotations</b></td>
        <td>map[string]string</td>
        <td>
          Annotations specifies annotations to apply to the created ResourceClaim.
Values support CEL expressions wrapped in {{ }} delimiters for dynamic content.
The system automatically adds standard annotations for tracking.

Template variables available:
- trigger: The resource triggering claim creation
- requestInfo: Request details
- user: User information

Examples:
- created-for: "{{trigger.metadata.name}}" (CEL expression)
- requested-by: "{{user.name}}" (CEL expression)
- environment: "production" (literal string)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>generateName</b></td>
        <td>string</td>
        <td>
          GenerateName specifies a prefix for auto-generated names when Name is empty.
Kubernetes appends random characters to create unique names.
Supports CEL expressions wrapped in {{ }} delimiters.

Examples:
- "{{trigger.spec.type + '-claim-'}}" (CEL expression)
- "{{trigger.spec.type}}-claim-" (CEL + literal)
- "quota-claim-" (literal string)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>labels</b></td>
        <td>map[string]string</td>
        <td>
          Labels specifies static labels to apply to the created ResourceClaim.
Values are literal strings (no template processing).
The system automatically adds standard labels for policy tracking.

Useful for:
- Organizing claims by policy or resource type
- Adding environment or tier indicators
- Enabling label-based queries and monitoring<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name specifies the exact name for the created ResourceClaim.
Supports CEL expressions wrapped in {{ }} delimiters with access to template variables.
Leave empty to use GenerateName for auto-generated names.

CEL Expression Syntax: CEL expressions must be enclosed in double curly braces {{ }}.
Plain strings without {{ }} are treated as literal values.

Template variables available:
- trigger: The resource triggering claim creation
- requestInfo: Request details (verb, resource, name, etc.)
- user: User information (name, uid, groups, extra)

Examples:
- "{{trigger.metadata.name + '-quota-claim'}}" (CEL expression)
- "{{trigger.metadata.name}}-claim" (CEL + literal)
- "fixed-claim-name" (literal string)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace specifies where the ResourceClaim will be created.
Supports CEL expressions wrapped in {{ }} delimiters to derive namespace from trigger resource.
Leave empty to create in the same namespace as the trigger resource.

Examples:
- "{{trigger.metadata.namespace}}" (CEL: same namespace as trigger)
- "milo-system" (literal: fixed system namespace)
- "{{trigger.spec.organization + '-claims'}}" (CEL: derived namespace)<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.spec.target.resourceClaimTemplate.spec
<sup><sup>[↩ Parent](#claimcreationpolicyspectargetresourceclaimtemplate)</sup></sup>



Spec for the created ResourceClaim.
String fields support CEL expressions.

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
        <td><b><a href="#claimcreationpolicyspectargetresourceclaimtemplatespecrequestsindex">requests</a></b></td>
        <td>[]object</td>
        <td>
          Requests specifies the resource types and amounts being claimed from quota.
Each resource type can appear only once in the requests array. Minimum 1
request, maximum 20 requests per claim.

The system processes all requests as a single atomic operation: either all
requests are granted or all are denied.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#claimcreationpolicyspectargetresourceclaimtemplatespecconsumerref">consumerRef</a></b></td>
        <td>object</td>
        <td>
          ConsumerRef identifies the quota consumer making this claim. The consumer
must match the ConsumerType defined in the ResourceRegistration for each
requested resource type. The system validates this relationship during
claim processing.

When creating ResourceClaims via ClaimCreationPolicy, this field can be
omitted and the admission plugin will automatically fill it based on the
authenticated user's context (organization or project).

Examples:

  - Organization consuming Project quota
  - Project consuming User quota
  - Organization consuming storage quota<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#claimcreationpolicyspectargetresourceclaimtemplatespecresourceref">resourceRef</a></b></td>
        <td>object</td>
        <td>
          ResourceRef identifies the actual Kubernetes resource that triggered this
claim. ClaimCreationPolicy automatically populates this field during
admission. Uses unversioned reference (apiGroup + kind + name + namespace)
to remain valid across API version changes.

The referenced resource's kind must be listed in the ResourceRegistration's
spec.claimingResources for the claim to be valid.

Examples:

  - Project resource triggering Project quota claim
  - User resource triggering User quota claim
  - Organization resource triggering storage quota claim<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.spec.target.resourceClaimTemplate.spec.requests[index]
<sup><sup>[↩ Parent](#claimcreationpolicyspectargetresourceclaimtemplatespec)</sup></sup>



ResourceRequest defines a single resource request within a ResourceClaim.
Each request specifies a resource type and the amount of quota being claimed.

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
        <td><b>amount</b></td>
        <td>integer</td>
        <td>
          Amount specifies how much quota to claim for this resource type. Must be
measured in the BaseUnit defined by the corresponding ResourceRegistration.
Must be a positive integer (minimum value is 0, but 0 means no quota
requested).

For Entity registrations: Use 1 for single resource instances (1 Project, 1
User) For Allocation registrations: Use actual capacity amounts (2048 for
2048 MB, 1000 for 1000 millicores)

Examples:

  - 1 (claiming 1 Project)
  - 2048 (claiming 2048 bytes of storage)
  - 1000 (claiming 1000 CPU millicores)<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>resourceType</b></td>
        <td>string</td>
        <td>
          ResourceType identifies the specific resource type being claimed. Must
exactly match a ResourceRegistration.spec.resourceType that is currently
active. The quota system validates this reference during claim processing.

The format is defined by platform administrators when creating ResourceRegistrations.
Service providers can use any identifier that makes sense for their quota system usage.

Examples:

  - "resourcemanager.miloapis.com/projects"
  - "compute_cpu"
  - "storage.volumes"
  - "custom-service-quota"<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.spec.target.resourceClaimTemplate.spec.consumerRef
<sup><sup>[↩ Parent](#claimcreationpolicyspectargetresourceclaimtemplatespec)</sup></sup>



ConsumerRef identifies the quota consumer making this claim. The consumer
must match the ConsumerType defined in the ResourceRegistration for each
requested resource type. The system validates this relationship during
claim processing.

When creating ResourceClaims via ClaimCreationPolicy, this field can be
omitted and the admission plugin will automatically fill it based on the
authenticated user's context (organization or project).

Examples:

  - Organization consuming Project quota
  - Project consuming User quota
  - Organization consuming storage quota

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
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind specifies the type of consumer resource.
Must match an existing Kubernetes resource type that can receive quota grants.

Common consumer types:
- "Organization" (top-level quota consumer)
- "Project" (project-level quota consumer)
- "User" (user-level quota consumer)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name identifies the specific consumer resource instance.
Must match the name of an existing consumer resource in the cluster.

Examples:
- "acme-corp" (Organization name)
- "web-application" (Project name)
- "john.doe" (User name)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup specifies the API group of the consumer resource.
Use full group name for Milo resources.

Examples:
- "resourcemanager.miloapis.com" (Organization/Project resources)
- "iam.miloapis.com" (User/Group resources)
- "infrastructure.miloapis.com" (infrastructure resources)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace identifies the namespace of the consumer resource.
Required for namespaced consumer resources (e.g., Projects).
Leave empty for cluster-scoped consumer resources (e.g., Organizations).

Examples:
- "" (empty for cluster-scoped Organizations)
- "organization-acme-corp" (namespace for Projects within an organization)
- "project-web-app" (namespace for resources within a project)<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.spec.target.resourceClaimTemplate.spec.resourceRef
<sup><sup>[↩ Parent](#claimcreationpolicyspectargetresourceclaimtemplatespec)</sup></sup>



ResourceRef identifies the actual Kubernetes resource that triggered this
claim. ClaimCreationPolicy automatically populates this field during
admission. Uses unversioned reference (apiGroup + kind + name + namespace)
to remain valid across API version changes.

The referenced resource's kind must be listed in the ResourceRegistration's
spec.claimingResources for the claim to be valid.

Examples:

  - Project resource triggering Project quota claim
  - User resource triggering User quota claim
  - Organization resource triggering storage quota claim

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
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind specifies the type of the referenced resource.
Must match an existing Kubernetes resource type.

Examples:
- "Project" (Project resource that triggered quota claim)
- "User" (User resource that triggered quota claim)
- "Organization" (Organization resource that triggered quota claim)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name identifies the specific resource instance that triggered the quota claim.
Used for linking claims back to their triggering resources.

Examples:
- "web-app-project" (Project that triggered Project quota claim)
- "john.doe" (User that triggered User quota claim)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup specifies the API group of the referenced resource.
Use full group name for Milo resources.

Examples:
- "resourcemanager.miloapis.com" (Project, Organization)
- "iam.miloapis.com" (User, Group)
- "infrastructure.miloapis.com" (infrastructure resources)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace specifies the namespace containing the referenced resource.
Required for namespaced resources, omitted for cluster-scoped resources.

Examples:
- "acme-corp" (organization namespace containing Project)
- "team-alpha" (project namespace containing User)
- "" or omitted (for cluster-scoped resources like Organization)<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.spec.trigger
<sup><sup>[↩ Parent](#claimcreationpolicyspec)</sup></sup>



Trigger defines what resource changes should trigger claim creation.

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
        <td><b><a href="#claimcreationpolicyspectriggerresource">resource</a></b></td>
        <td>object</td>
        <td>
          Resource specifies which resource type triggers this policy.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#claimcreationpolicyspectriggerconstraintsindex">constraints</a></b></td>
        <td>[]object</td>
        <td>
          Constraints are CEL expressions that must evaluate to true for claim creation to occur.
These are pure CEL expressions WITHOUT {{ }} delimiters (unlike template fields).
Evaluated in the admission context.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.spec.trigger.resource
<sup><sup>[↩ Parent](#claimcreationpolicyspectrigger)</sup></sup>



Resource specifies which resource type triggers this policy.

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
        <td>
          APIVersion of the trigger resource in the format "group/version" or "version" for core resources.
Examples: "v1" for core resources like Secret, "resourcemanager.miloapis.com/v1alpha1" for custom resources.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind is the kind of the trigger resource.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.spec.trigger.constraints[index]
<sup><sup>[↩ Parent](#claimcreationpolicyspectrigger)</sup></sup>



ConditionExpression defines a CEL expression that determines when the policy should trigger.
All expressions in a policy's trigger conditions must evaluate to true for the policy to activate.

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
        <td><b>expression</b></td>
        <td>string</td>
        <td>
          Expression specifies the CEL expression to evaluate against the trigger resource.
This is a pure CEL expression WITHOUT {{ }} delimiters (unlike template fields).
Must return a boolean value (true to match, false to skip).
Maximum 1024 characters.

Available variables in GrantCreationPolicy context:
- trigger: The complete resource being watched (map[string]any)
  - trigger.metadata.name, trigger.spec.*, trigger.status.*, etc.

Common expression patterns:
- trigger.spec.tier == "premium" (check resource field)
- trigger.metadata.labels["environment"] == "prod" (check labels)
- trigger.status.phase == "Active" (check status)
- trigger.metadata.namespace == "production" (check namespace)
- has(trigger.spec.quotaProfile) (check field existence)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          Message provides a human-readable description explaining when this condition applies.
Used for documentation and debugging. Maximum 256 characters.

Examples:
- "Applies only to premium tier organizations"
- "Matches organizations in production environment"
- "Triggers when quota profile is specified"<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.status
<sup><sup>[↩ Parent](#claimcreationpolicy)</sup></sup>



ClaimCreationPolicyStatus defines the observed state of ClaimCreationPolicy.

Status fields
- conditions[type=Ready]: True when the policy is validated and active.

See also
- [ResourceClaim](#resourceclaim): The object created by this policy.

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
        <td><b><a href="#claimcreationpolicystatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of the policy's current state.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          ObservedGeneration is the most recent generation observed.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClaimCreationPolicy.status.conditions[index]
<sup><sup>[↩ Parent](#claimcreationpolicystatus)</sup></sup>



Condition contains details for one aspect of the current state of this API Resource.

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
        <td><b>lastTransitionTime</b></td>
        <td>string</td>
        <td>
          lastTransitionTime is the last time the condition transitioned from one status to another.
This should be when the underlying condition changed.  If that is not known, then using the time when the API field changed is acceptable.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          message is a human readable message indicating details about the transition.
This may be an empty string.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          reason contains a programmatic identifier indicating the reason for the condition's last transition.
Producers of specific condition types may define expected values and meanings for this field,
and whether the values are considered a guaranteed API.
The value should be a CamelCase string.
This field may not be empty.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          status of the condition, one of True, False, Unknown.<br/>
          <br/>
            <i>Enum</i>: True, False, Unknown<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          type of condition in CamelCase or in foo.example.com/CamelCase.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          observedGeneration represents the .metadata.generation that the condition was set based upon.
For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date
with respect to the current state of the instance.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>

## GrantCreationPolicy
<sup><sup>[↩ Parent](#quotamiloapiscomv1alpha1 )</sup></sup>






GrantCreationPolicy automates ResourceGrant creation when observed resources meet conditions.
Use it to provision quota based on resource lifecycle events and attributes.

### How It Works
- Watch the kind in `spec.trigger.resource` and evaluate all `spec.trigger.constraints[]`.
- When all constraints are true, evaluate `spec.target.resourceGrantTemplate` and create a `ResourceGrant`.
- Optionally target a parent control plane via `spec.target.parentContext` (CEL-resolved name) for cross-cluster allocation.
- Allowances (resource types and amounts) are static in `v1alpha1`.

### Template Expressions
Template expressions generate dynamic content for ResourceGrant fields including metadata and specification.
Content inside `{{ }}` delimiters is evaluated as CEL expressions, while content outside is treated as literal text.

**Template Expression Rules:**
- `{{expression}}` - Pure CEL expression, evaluated and substituted
- `literal-text` - Used as-is without any evaluation
- `{{expression}}-literal` - CEL output combined with literal text
- `prefix-{{expression}}-suffix` - Literal text surrounding CEL expression

**Template Expression Examples:**
- `{{trigger.metadata.name + '-grant'}}` - Pure CEL expression (metadata)
- `{{trigger.metadata.name}}-quota-grant` - CEL + literal suffix (metadata)
- `{{trigger.spec.type + "-consumer"}}` - Extract spec field for consumer name (spec)
- `{{trigger.metadata.labels["environment"] + "-grants"}}` - Label-based naming (spec)
- `fixed-grant-name` - Literal string only (no evaluation)

**Use Template Expressions For:** ResourceGrantTemplate fields (metadata and spec)

### Constraint Expressions
Constraint expressions determine whether a policy should trigger by evaluating boolean conditions.
These are pure CEL expressions without delimiters that must return true/false values.

**Constraint Expression Rules:**
- Write pure CEL expressions directly (no wrapping syntax)
- Must return boolean values (true = trigger policy, false = skip)
- All constraints in a policy must return true for the policy to activate

**Constraint Expression Examples:**
- `trigger.spec.tier == "premium"` - Field equality check
- `trigger.metadata.labels["environment"] == "prod"` - Label-based filtering
- `trigger.status.phase == "Active"` - Status condition check
- `has(trigger.spec.quotaProfile)` - Field existence check

**Use Constraint Expressions For:** spec.trigger.constraints fields

### Expression Variables
Both template and constraint expressions have access to the resource context variables:

**trigger**: The complete resource that triggered the policy, including all metadata, spec,
and status fields. Navigate using CEL property access: `trigger.metadata.name`, `trigger.spec.tier`.
This is the only variable available since GrantCreationPolicy runs during resource watching,
not during admission processing.

**CEL Functions**: Standard CEL functions available for data manipulation including conditional
expressions (`condition ? value1 : value2`), string methods (`lowerAscii()`, `upperAscii()`, `trim()`),
and collection operations (`exists()`, `all()`, `filter()`).

### Works With
- Creates [ResourceGrant](#resourcegrant) objects whose `allowances[].resourceType` must exist in a [ResourceRegistration](#resourceregistration).
- May target a parent control plane via `spec.target.parentContext` for cross-plane quota allocation.
- Policy readiness (`status.conditions[type=Ready]`) signals expression/constraint validity.

### Status
- `status.conditions[type=Ready]`: Policy validated and active.
- `status.conditions[type=ParentContextReady]`: Cross‑cluster targeting is resolvable.
- `status.observedGeneration`: Latest spec generation processed.

### Selectors and Filtering
  - Field selectors (server-side):
    `spec.trigger.resource.kind`, `spec.trigger.resource.apiVersion`,
    `spec.target.parentContext.kind`, `spec.target.parentContext.apiGroup`.
  - Label selectors (add your own):
  - `quota.miloapis.com/trigger-kind`: `Organization`
  - `quota.miloapis.com/environment`: `prod`
  - Common queries:
  - All policies for a trigger kind: label selector `quota.miloapis.com/trigger-kind`.
  - All active policies: field selector `spec.disabled=false`.

### Defaults and Limits
- Resource grant allowances are static (no expression-based amounts) in `v1alpha1`.

### Notes
- If `ParentContextReady=False`, verify `nameExpression` and referenced attributes.
- Disabled policies (`spec.disabled=true`) do not create grants.

### See Also
- [ResourceGrant](#resourcegrant): The object created by this policy.
- [ResourceRegistration](#resourceregistration): Resource types that grants must reference.
- [ClaimCreationPolicy](#claimcreationpolicy): Creates claims at admission for enforcement.

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
      <td>GrantCreationPolicy</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#grantcreationpolicyspec">spec</a></b></td>
        <td>object</td>
        <td>
          GrantCreationPolicySpec defines the desired state of GrantCreationPolicy.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#grantcreationpolicystatus">status</a></b></td>
        <td>object</td>
        <td>
          GrantCreationPolicyStatus defines the observed state of GrantCreationPolicy.

Status fields
- conditions[type=Ready]: True when the policy is validated and active.
- conditions[type=ParentContextReady]: True when cross‑cluster targeting is resolvable.
- observedGeneration: Latest spec generation processed by the quota system.

See also
- [ResourceGrant](#resourcegrant): The object created by this policy.
- [ResourceRegistration](#resourceregistration): Resource types for which grants are issued.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.spec
<sup><sup>[↩ Parent](#grantcreationpolicy)</sup></sup>



GrantCreationPolicySpec defines the desired state of GrantCreationPolicy.

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
        <td><b><a href="#grantcreationpolicyspectarget">target</a></b></td>
        <td>object</td>
        <td>
          Target defines where and how grants should be created.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#grantcreationpolicyspectrigger">trigger</a></b></td>
        <td>object</td>
        <td>
          Trigger defines what resource changes should trigger grant creation.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>disabled</b></td>
        <td>boolean</td>
        <td>
          Disabled determines if this policy is inactive.
If true, no **ResourceGrants** will be created for matching resources.<br/>
          <br/>
            <i>Default</i>: false<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.spec.target
<sup><sup>[↩ Parent](#grantcreationpolicyspec)</sup></sup>



Target defines where and how grants should be created.

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
        <td><b><a href="#grantcreationpolicyspectargetresourcegranttemplate">resourceGrantTemplate</a></b></td>
        <td>object</td>
        <td>
          ResourceGrantTemplate defines how to create **ResourceGrants**.
String fields support CEL expressions wrapped in {{ }} delimiters for dynamic content.
Plain strings without {{ }} are treated as literal values.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#grantcreationpolicyspectargetparentcontext">parentContext</a></b></td>
        <td>object</td>
        <td>
          ParentContext defines cross-control-plane targeting.
If specified, grants will be created in the target parent context
instead of the current control plane.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.spec.target.resourceGrantTemplate
<sup><sup>[↩ Parent](#grantcreationpolicyspectarget)</sup></sup>



ResourceGrantTemplate defines how to create **ResourceGrants**.
String fields support CEL expressions wrapped in {{ }} delimiters for dynamic content.
Plain strings without {{ }} are treated as literal values.

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
        <td><b><a href="#grantcreationpolicyspectargetresourcegranttemplatemetadata">metadata</a></b></td>
        <td>object</td>
        <td>
          Metadata for the created ResourceGrant.
String fields support CEL expressions wrapped in {{ }} delimiters.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#grantcreationpolicyspectargetresourcegranttemplatespec">spec</a></b></td>
        <td>object</td>
        <td>
          Spec for the created ResourceGrant.
String fields support CEL expressions wrapped in {{ }} delimiters.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.spec.target.resourceGrantTemplate.metadata
<sup><sup>[↩ Parent](#grantcreationpolicyspectargetresourcegranttemplate)</sup></sup>



Metadata for the created ResourceGrant.
String fields support CEL expressions wrapped in {{ }} delimiters.

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
        <td><b>annotations</b></td>
        <td>map[string]string</td>
        <td>
          Annotations specifies annotations to apply to the created ResourceClaim.
Values support CEL expressions wrapped in {{ }} delimiters for dynamic content.
The system automatically adds standard annotations for tracking.

Template variables available:
- trigger: The resource triggering claim creation
- requestInfo: Request details
- user: User information

Examples:
- created-for: "{{trigger.metadata.name}}" (CEL expression)
- requested-by: "{{user.name}}" (CEL expression)
- environment: "production" (literal string)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>generateName</b></td>
        <td>string</td>
        <td>
          GenerateName specifies a prefix for auto-generated names when Name is empty.
Kubernetes appends random characters to create unique names.
Supports CEL expressions wrapped in {{ }} delimiters.

Examples:
- "{{trigger.spec.type + '-claim-'}}" (CEL expression)
- "{{trigger.spec.type}}-claim-" (CEL + literal)
- "quota-claim-" (literal string)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>labels</b></td>
        <td>map[string]string</td>
        <td>
          Labels specifies static labels to apply to the created ResourceClaim.
Values are literal strings (no template processing).
The system automatically adds standard labels for policy tracking.

Useful for:
- Organizing claims by policy or resource type
- Adding environment or tier indicators
- Enabling label-based queries and monitoring<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name specifies the exact name for the created ResourceClaim.
Supports CEL expressions wrapped in {{ }} delimiters with access to template variables.
Leave empty to use GenerateName for auto-generated names.

CEL Expression Syntax: CEL expressions must be enclosed in double curly braces {{ }}.
Plain strings without {{ }} are treated as literal values.

Template variables available:
- trigger: The resource triggering claim creation
- requestInfo: Request details (verb, resource, name, etc.)
- user: User information (name, uid, groups, extra)

Examples:
- "{{trigger.metadata.name + '-quota-claim'}}" (CEL expression)
- "{{trigger.metadata.name}}-claim" (CEL + literal)
- "fixed-claim-name" (literal string)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace specifies where the ResourceClaim will be created.
Supports CEL expressions wrapped in {{ }} delimiters to derive namespace from trigger resource.
Leave empty to create in the same namespace as the trigger resource.

Examples:
- "{{trigger.metadata.namespace}}" (CEL: same namespace as trigger)
- "milo-system" (literal: fixed system namespace)
- "{{trigger.spec.organization + '-claims'}}" (CEL: derived namespace)<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.spec.target.resourceGrantTemplate.spec
<sup><sup>[↩ Parent](#grantcreationpolicyspectargetresourcegranttemplate)</sup></sup>



Spec for the created ResourceGrant.
String fields support CEL expressions wrapped in {{ }} delimiters.

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
        <td><b><a href="#grantcreationpolicyspectargetresourcegranttemplatespecallowancesindex">allowances</a></b></td>
        <td>[]object</td>
        <td>
          Allowances specifies the quota allocations provided by this grant.
Each allowance grants capacity for a specific resource type.
Minimum 1 allowance required, maximum 20 allowances per grant.

All allowances in a single grant:
- Apply to the same consumer (spec.consumerRef)
- Contribute to the same AllowanceBucket for each resource type
- Activate and deactivate together based on the grant's status<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#grantcreationpolicyspectargetresourcegranttemplatespecconsumerref">consumerRef</a></b></td>
        <td>object</td>
        <td>
          ConsumerRef identifies the quota consumer that receives these allowances.
The consumer type must match the ConsumerType defined in the ResourceRegistration
for each allowance resource type. The system validates this relationship.

Examples:
- Organization receiving Project quota allowances
- Project receiving User quota allowances
- Organization receiving storage quota allowances<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.spec.target.resourceGrantTemplate.spec.allowances[index]
<sup><sup>[↩ Parent](#grantcreationpolicyspectargetresourcegranttemplatespec)</sup></sup>



Allowance defines quota allocation for a specific resource type within a ResourceGrant.
Each allowance can contain multiple buckets that sum to provide total capacity.

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
        <td><b><a href="#grantcreationpolicyspectargetresourcegranttemplatespecallowancesindexbucketsindex">buckets</a></b></td>
        <td>[]object</td>
        <td>
          Buckets contains the quota allocations for this resource type.
All bucket amounts are summed to determine the total allowance.
Minimum 1 bucket required per allowance.

Multiple buckets can be used for:
- Separating quota from different sources or tiers
- Managing incremental quota increases over time
- Tracking quota attribution for billing or reporting<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>resourceType</b></td>
        <td>string</td>
        <td>
          ResourceType identifies the specific resource type receiving quota allocation.
Must exactly match a ResourceRegistration.spec.resourceType that is currently active.
The quota system validates this reference when processing the grant.

The identifier format is flexible, as defined by platform administrators
in their ResourceRegistrations.

Examples:
- "resourcemanager.miloapis.com/projects"
- "compute_cpu"
- "storage.volumes"
- "custom-service-quota"<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.spec.target.resourceGrantTemplate.spec.allowances[index].buckets[index]
<sup><sup>[↩ Parent](#grantcreationpolicyspectargetresourcegranttemplatespecallowancesindex)</sup></sup>



Bucket represents a single allocation of quota capacity within an allowance.
Each bucket contributes its amount to the total allowance for a resource type.

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
        <td><b>amount</b></td>
        <td>integer</td>
        <td>
          Amount specifies the quota capacity provided by this bucket.
Must be measured in the BaseUnit defined by the corresponding ResourceRegistration.
Must be a non-negative integer (0 is valid but provides no quota).

Examples:
- 100 (providing 100 projects)
- 2048000 (providing 2048000 bytes = 2GB)
- 5000 (providing 5000 CPU millicores = 5 cores)<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.spec.target.resourceGrantTemplate.spec.consumerRef
<sup><sup>[↩ Parent](#grantcreationpolicyspectargetresourcegranttemplatespec)</sup></sup>



ConsumerRef identifies the quota consumer that receives these allowances.
The consumer type must match the ConsumerType defined in the ResourceRegistration
for each allowance resource type. The system validates this relationship.

Examples:
- Organization receiving Project quota allowances
- Project receiving User quota allowances
- Organization receiving storage quota allowances

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
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind specifies the type of consumer resource.
Must match an existing Kubernetes resource type that can receive quota grants.

Common consumer types:
- "Organization" (top-level quota consumer)
- "Project" (project-level quota consumer)
- "User" (user-level quota consumer)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name identifies the specific consumer resource instance.
Must match the name of an existing consumer resource in the cluster.

Examples:
- "acme-corp" (Organization name)
- "web-application" (Project name)
- "john.doe" (User name)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup specifies the API group of the consumer resource.
Use full group name for Milo resources.

Examples:
- "resourcemanager.miloapis.com" (Organization/Project resources)
- "iam.miloapis.com" (User/Group resources)
- "infrastructure.miloapis.com" (infrastructure resources)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace identifies the namespace of the consumer resource.
Required for namespaced consumer resources (e.g., Projects).
Leave empty for cluster-scoped consumer resources (e.g., Organizations).

Examples:
- "" (empty for cluster-scoped Organizations)
- "organization-acme-corp" (namespace for Projects within an organization)
- "project-web-app" (namespace for resources within a project)<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.spec.target.parentContext
<sup><sup>[↩ Parent](#grantcreationpolicyspectarget)</sup></sup>



ParentContext defines cross-control-plane targeting.
If specified, grants will be created in the target parent context
instead of the current control plane.

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
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup specifies the API group of the parent context resource.
Must follow DNS subdomain format. Maximum 253 characters.

Examples:
- "resourcemanager.miloapis.com" (for Organization parent context)
- "infrastructure.miloapis.com" (for Cluster parent context)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind specifies the resource type that represents the parent context.
Must be a valid Kubernetes resource Kind. Maximum 63 characters.

Examples:
- "Organization" (create grants in organization's parent control plane)
- "Cluster" (create grants in cluster's parent infrastructure)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>nameExpression</b></td>
        <td>string</td>
        <td>
          NameExpression is a CEL expression that resolves the name of the parent context resource.
Must return a string value that identifies the specific parent context instance.
Maximum 512 characters.

Available variables:
- object: The trigger resource being evaluated (complete object)

Common expression patterns:
- object.spec.organization (direct field reference)
- object.metadata.labels["parent-org"] (label-based resolution)
- object.metadata.namespace.split("-")[0] (derived from namespace naming)

Examples:
- "acme-corp" (literal parent name)
- object.spec.parentOrganization (field from trigger resource)
- object.metadata.labels["quota.miloapis.com/organization"] (label value)<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.spec.trigger
<sup><sup>[↩ Parent](#grantcreationpolicyspec)</sup></sup>



Trigger defines what resource changes should trigger grant creation.

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
        <td><b><a href="#grantcreationpolicyspectriggerresource">resource</a></b></td>
        <td>object</td>
        <td>
          Resource specifies which resource type triggers this policy.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#grantcreationpolicyspectriggerconstraintsindex">constraints</a></b></td>
        <td>[]object</td>
        <td>
          Constraints are CEL expressions that must evaluate to true for grant creation.
These are pure CEL expressions WITHOUT {{ }} delimiters (unlike template fields).
All constraints must pass for the policy to trigger.
The 'object' variable contains the trigger resource being evaluated.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.spec.trigger.resource
<sup><sup>[↩ Parent](#grantcreationpolicyspectrigger)</sup></sup>



Resource specifies which resource type triggers this policy.

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
        <td>
          APIVersion of the trigger resource in the format "group/version".
For core resources, use "v1".<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind is the kind of the trigger resource.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.spec.trigger.constraints[index]
<sup><sup>[↩ Parent](#grantcreationpolicyspectrigger)</sup></sup>



ConditionExpression defines a CEL expression that determines when the policy should trigger.
All expressions in a policy's trigger conditions must evaluate to true for the policy to activate.

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
        <td><b>expression</b></td>
        <td>string</td>
        <td>
          Expression specifies the CEL expression to evaluate against the trigger resource.
This is a pure CEL expression WITHOUT {{ }} delimiters (unlike template fields).
Must return a boolean value (true to match, false to skip).
Maximum 1024 characters.

Available variables in GrantCreationPolicy context:
- trigger: The complete resource being watched (map[string]any)
  - trigger.metadata.name, trigger.spec.*, trigger.status.*, etc.

Common expression patterns:
- trigger.spec.tier == "premium" (check resource field)
- trigger.metadata.labels["environment"] == "prod" (check labels)
- trigger.status.phase == "Active" (check status)
- trigger.metadata.namespace == "production" (check namespace)
- has(trigger.spec.quotaProfile) (check field existence)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          Message provides a human-readable description explaining when this condition applies.
Used for documentation and debugging. Maximum 256 characters.

Examples:
- "Applies only to premium tier organizations"
- "Matches organizations in production environment"
- "Triggers when quota profile is specified"<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.status
<sup><sup>[↩ Parent](#grantcreationpolicy)</sup></sup>



GrantCreationPolicyStatus defines the observed state of GrantCreationPolicy.

Status fields
- conditions[type=Ready]: True when the policy is validated and active.
- conditions[type=ParentContextReady]: True when cross‑cluster targeting is resolvable.
- observedGeneration: Latest spec generation processed by the quota system.

See also
- [ResourceGrant](#resourcegrant): The object created by this policy.
- [ResourceRegistration](#resourceregistration): Resource types for which grants are issued.

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
        <td><b><a href="#grantcreationpolicystatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of the policy's current state.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          ObservedGeneration is the most recent generation observed.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### GrantCreationPolicy.status.conditions[index]
<sup><sup>[↩ Parent](#grantcreationpolicystatus)</sup></sup>



Condition contains details for one aspect of the current state of this API Resource.

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
        <td><b>lastTransitionTime</b></td>
        <td>string</td>
        <td>
          lastTransitionTime is the last time the condition transitioned from one status to another.
This should be when the underlying condition changed.  If that is not known, then using the time when the API field changed is acceptable.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          message is a human readable message indicating details about the transition.
This may be an empty string.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          reason contains a programmatic identifier indicating the reason for the condition's last transition.
Producers of specific condition types may define expected values and meanings for this field,
and whether the values are considered a guaranteed API.
The value should be a CamelCase string.
This field may not be empty.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          status of the condition, one of True, False, Unknown.<br/>
          <br/>
            <i>Enum</i>: True, False, Unknown<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          type of condition in CamelCase or in foo.example.com/CamelCase.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          observedGeneration represents the .metadata.generation that the condition was set based upon.
For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date
with respect to the current state of the instance.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>

## ResourceClaim
<sup><sup>[↩ Parent](#quotamiloapiscomv1alpha1 )</sup></sup>






ResourceClaim requests quota allocation during resource creation. Claims
consume quota capacity from AllowanceBuckets and link to the triggering
Kubernetes resource for lifecycle management and auditing.

### How It Works

**ResourceClaims** follow a straightforward lifecycle from creation to
resolution. When a **ClaimCreationPolicy** triggers during admission, it
creates a **ResourceClaim** that immediately enters the quota evaluation
pipeline. The quota system first validates that the consumer type matches the
expected `ConsumerType` from the **ResourceRegistration**, then verifies
that the triggering resource kind is authorized to claim the requested
resource types.

Once validation passes, the quota system checks quota availability by
consulting the relevant **AllowanceBuckets**, one for each (consumer,
resourceType) combination in the claim's requests. The quota system treats
all requests in a claim as an atomic unit: either sufficient quota exists for
every request and the entire claim is granted, or any shortage results in
denying the complete claim. This atomic approach ensures consistency and
prevents partial resource allocations that could leave the system in an
inconsistent state.

When a claim is granted, it permanently reserves the requested quota amounts
until the claim is deleted. This consumption immediately reduces the
available quota in the corresponding **AllowanceBuckets**, preventing other
claims from accessing that capacity. The quota system updates the claim's
status with detailed results for each resource request, including which
**AllowanceBucket** provided the quota and any relevant error messages.

### Core Relationships

  - **Created by**: **ClaimCreationPolicy** during admission (automatically) or
    administrators (manually)
  - **Consumes from**: **AllowanceBucket** matching
    (`spec.consumerRef`, `spec.requests[].resourceType`)
  - **Capacity sourced from**: **ResourceGrant** objects aggregated by the bucket
  - **Linked to**: Triggering resource via `spec.resourceRef` for lifecycle management
  - **Validated against**: **ResourceRegistration** for each `spec.requests[].resourceType`

### Claim Lifecycle States

  - **Initial**: `Granted=False`, `reason=PendingEvaluation` (claim created, awaiting processing)
  - **Granted**: `Granted=True`, `reason=QuotaAvailable` (all requests allocated successfully)
  - **Denied**: `Granted=False`, `reason=QuotaExceeded` or `ValidationFailed` (requests could not be satisfied)

### Automatic vs Manual Claims

**Automatic Claims** (created by **ClaimCreationPolicy**):

  - Include standard labels and annotations for tracking
  - Set owner references to triggering resource when possible
  - Automatically cleaned up when denied to prevent accumulation
  - Marked with `quota.miloapis.com/auto-created=true` label

**Manual Claims** (created by administrators):

  - Require explicit metadata and references
  - Not automatically cleaned up when denied
  - Used for testing or special allocation scenarios

### Status Information

  - **Overall Status**: `status.conditions[type=Granted]` indicates claim approval
  - **Detailed Results**: `status.allocations[]` provides per-request allocation details
  - **Bucket References**: `status.allocations[].allocatingBucket` identifies quota sources

### Field Constraints and Validation

  - Maximum 20 resource requests per claim
  - Each resource type can appear only once in requests
  - Consumer type must match `ResourceRegistration.spec.consumerType` for each requested type
  - Triggering resource kind must be listed in `ResourceRegistration.spec.claimingResources`

### Selectors and Filtering

  - **Field selectors**: spec.consumerRef.kind, spec.consumerRef.name, spec.resourceRef.apiGroup, spec.resourceRef.kind, spec.resourceRef.name, spec.resourceRef.namespace
  - **Auto-created labels**: quota.miloapis.com/auto-created, quota.miloapis.com/policy, quota.miloapis.com/gvk
  - **Auto-created annotations**: quota.miloapis.com/created-by, quota.miloapis.com/created-at,  quota.miloapis.com/resource-name

### Common Queries

  - All claims for a consumer: field selector spec.consumerRef.kind + spec.consumerRef.name
  - Claims from a specific policy: label selector quota.miloapis.com/policy=<policy-name>
  - Claims for a resource type: add custom labels via policy template
  - Failed claims: field  selector on status conditions

### Troubleshooting

  - **Denied claims**: Check status.allocations[].message for specific quota or validation errors
  - **Pending claims**: Verify ResourceRegistration is Active and AllowanceBucket exists
  - **Missing claims**: Check ClaimCreationPolicy conditions and trigger expressions

### Performance Considerations

  - Claims are processed synchronously during admission (affects API latency)
  - Large numbers of claims can impact bucket aggregation performance
  - Consider batch processing for bulk resource creation

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
      <td>ResourceClaim</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#resourceclaimspec">spec</a></b></td>
        <td>object</td>
        <td>
          ResourceClaimSpec defines the desired state of ResourceClaim.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#resourceclaimstatus">status</a></b></td>
        <td>object</td>
        <td>
          ResourceClaimStatus reports the claim's processing state and allocation
results. The system updates this status to communicate whether quota was
granted and provide detailed allocation information for each requested
resource type.<br/>
          <br/>
            <i>Default</i>: map[conditions:[map[lastTransitionTime:1970-01-01T00:00:00Z message:Awaiting capacity evaluation reason:PendingEvaluation status:False type:Granted]]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceClaim.spec
<sup><sup>[↩ Parent](#resourceclaim)</sup></sup>



ResourceClaimSpec defines the desired state of ResourceClaim.

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
        <td><b><a href="#resourceclaimspecrequestsindex">requests</a></b></td>
        <td>[]object</td>
        <td>
          Requests specifies the resource types and amounts being claimed from quota.
Each resource type can appear only once in the requests array. Minimum 1
request, maximum 20 requests per claim.

The system processes all requests as a single atomic operation: either all
requests are granted or all are denied.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#resourceclaimspecconsumerref">consumerRef</a></b></td>
        <td>object</td>
        <td>
          ConsumerRef identifies the quota consumer making this claim. The consumer
must match the ConsumerType defined in the ResourceRegistration for each
requested resource type. The system validates this relationship during
claim processing.

When creating ResourceClaims via ClaimCreationPolicy, this field can be
omitted and the admission plugin will automatically fill it based on the
authenticated user's context (organization or project).

Examples:

  - Organization consuming Project quota
  - Project consuming User quota
  - Organization consuming storage quota<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#resourceclaimspecresourceref">resourceRef</a></b></td>
        <td>object</td>
        <td>
          ResourceRef identifies the actual Kubernetes resource that triggered this
claim. ClaimCreationPolicy automatically populates this field during
admission. Uses unversioned reference (apiGroup + kind + name + namespace)
to remain valid across API version changes.

The referenced resource's kind must be listed in the ResourceRegistration's
spec.claimingResources for the claim to be valid.

Examples:

  - Project resource triggering Project quota claim
  - User resource triggering User quota claim
  - Organization resource triggering storage quota claim<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceClaim.spec.requests[index]
<sup><sup>[↩ Parent](#resourceclaimspec)</sup></sup>



ResourceRequest defines a single resource request within a ResourceClaim.
Each request specifies a resource type and the amount of quota being claimed.

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
        <td><b>amount</b></td>
        <td>integer</td>
        <td>
          Amount specifies how much quota to claim for this resource type. Must be
measured in the BaseUnit defined by the corresponding ResourceRegistration.
Must be a positive integer (minimum value is 0, but 0 means no quota
requested).

For Entity registrations: Use 1 for single resource instances (1 Project, 1
User) For Allocation registrations: Use actual capacity amounts (2048 for
2048 MB, 1000 for 1000 millicores)

Examples:

  - 1 (claiming 1 Project)
  - 2048 (claiming 2048 bytes of storage)
  - 1000 (claiming 1000 CPU millicores)<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>resourceType</b></td>
        <td>string</td>
        <td>
          ResourceType identifies the specific resource type being claimed. Must
exactly match a ResourceRegistration.spec.resourceType that is currently
active. The quota system validates this reference during claim processing.

The format is defined by platform administrators when creating ResourceRegistrations.
Service providers can use any identifier that makes sense for their quota system usage.

Examples:

  - "resourcemanager.miloapis.com/projects"
  - "compute_cpu"
  - "storage.volumes"
  - "custom-service-quota"<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ResourceClaim.spec.consumerRef
<sup><sup>[↩ Parent](#resourceclaimspec)</sup></sup>



ConsumerRef identifies the quota consumer making this claim. The consumer
must match the ConsumerType defined in the ResourceRegistration for each
requested resource type. The system validates this relationship during
claim processing.

When creating ResourceClaims via ClaimCreationPolicy, this field can be
omitted and the admission plugin will automatically fill it based on the
authenticated user's context (organization or project).

Examples:

  - Organization consuming Project quota
  - Project consuming User quota
  - Organization consuming storage quota

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
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind specifies the type of consumer resource.
Must match an existing Kubernetes resource type that can receive quota grants.

Common consumer types:
- "Organization" (top-level quota consumer)
- "Project" (project-level quota consumer)
- "User" (user-level quota consumer)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name identifies the specific consumer resource instance.
Must match the name of an existing consumer resource in the cluster.

Examples:
- "acme-corp" (Organization name)
- "web-application" (Project name)
- "john.doe" (User name)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup specifies the API group of the consumer resource.
Use full group name for Milo resources.

Examples:
- "resourcemanager.miloapis.com" (Organization/Project resources)
- "iam.miloapis.com" (User/Group resources)
- "infrastructure.miloapis.com" (infrastructure resources)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace identifies the namespace of the consumer resource.
Required for namespaced consumer resources (e.g., Projects).
Leave empty for cluster-scoped consumer resources (e.g., Organizations).

Examples:
- "" (empty for cluster-scoped Organizations)
- "organization-acme-corp" (namespace for Projects within an organization)
- "project-web-app" (namespace for resources within a project)<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceClaim.spec.resourceRef
<sup><sup>[↩ Parent](#resourceclaimspec)</sup></sup>



ResourceRef identifies the actual Kubernetes resource that triggered this
claim. ClaimCreationPolicy automatically populates this field during
admission. Uses unversioned reference (apiGroup + kind + name + namespace)
to remain valid across API version changes.

The referenced resource's kind must be listed in the ResourceRegistration's
spec.claimingResources for the claim to be valid.

Examples:

  - Project resource triggering Project quota claim
  - User resource triggering User quota claim
  - Organization resource triggering storage quota claim

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
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind specifies the type of the referenced resource.
Must match an existing Kubernetes resource type.

Examples:
- "Project" (Project resource that triggered quota claim)
- "User" (User resource that triggered quota claim)
- "Organization" (Organization resource that triggered quota claim)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name identifies the specific resource instance that triggered the quota claim.
Used for linking claims back to their triggering resources.

Examples:
- "web-app-project" (Project that triggered Project quota claim)
- "john.doe" (User that triggered User quota claim)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup specifies the API group of the referenced resource.
Use full group name for Milo resources.

Examples:
- "resourcemanager.miloapis.com" (Project, Organization)
- "iam.miloapis.com" (User, Group)
- "infrastructure.miloapis.com" (infrastructure resources)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace specifies the namespace containing the referenced resource.
Required for namespaced resources, omitted for cluster-scoped resources.

Examples:
- "acme-corp" (organization namespace containing Project)
- "team-alpha" (project namespace containing User)
- "" or omitted (for cluster-scoped resources like Organization)<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceClaim.status
<sup><sup>[↩ Parent](#resourceclaim)</sup></sup>



ResourceClaimStatus reports the claim's processing state and allocation
results. The system updates this status to communicate whether quota was
granted and provide detailed allocation information for each requested
resource type.

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
        <td><b><a href="#resourceclaimstatusallocationsindex">allocations</a></b></td>
        <td>[]object</td>
        <td>
          Allocations provides detailed status for each resource request in the
claim. The system creates one allocation entry for each request in
spec.requests. Use this field to understand which specific requests were
granted or denied.

List is indexed by ResourceType for efficient lookups.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#resourceclaimstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represents the overall status of the claim evaluation.
Controllers set these conditions to provide a high-level view of claim
processing.

Standard condition types:

  - "Granted": Indicates whether the claim was approved and quota allocated

Standard condition reasons for "Granted":

  - "QuotaAvailable": All requested quota was available and allocated
  - "QuotaExceeded": Insufficient quota prevented allocation (claim denied)
  - "ValidationFailed": Configuration errors prevented evaluation (claim denied)
  - "PendingEvaluation": Claim is still being processed (initial state)

Claim Lifecycle:

  1. Created: Granted=False, reason=PendingEvaluation
  2. Processed: Granted=True/False based on quota availability and validation
  3. Updated: Granted condition changes only when allocation results change<br/>
          <br/>
            <i>Validations</i>:<li>self.all(c, c.type == 'Granted' ? c.reason in ['QuotaAvailable', 'QuotaExceeded', 'ValidationFailed', 'PendingEvaluation'] : true): Granted condition reason must be valid</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          ObservedGeneration indicates the most recent spec generation the system has
processed. When ObservedGeneration matches metadata.generation, the status
reflects the current spec. When ObservedGeneration is lower, the system is
still processing recent changes.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceClaim.status.allocations[index]
<sup><sup>[↩ Parent](#resourceclaimstatus)</sup></sup>



ResourceClaimAllocationStatus tracks the allocation status for a specific resource
request within a claim. The system creates one allocation entry for each
request in the claim specification.

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
        <td><b>lastTransitionTime</b></td>
        <td>string</td>
        <td>
          LastTransitionTime records when this allocation status last changed.
Updates whenever Status, Reason, or Message changes.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>resourceType</b></td>
        <td>string</td>
        <td>
          ResourceType identifies which resource request this allocation status
describes. Must exactly match one of the resourceType values in
spec.requests.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          Status indicates the allocation result for this specific resource request.

Valid values:

  - "Granted": Quota was available and the request was approved
  - "Denied": Insufficient quota or validation failure prevented allocation
  - "Pending": Request is being evaluated (initial state)<br/>
          <br/>
            <i>Enum</i>: Granted, Denied, Pending<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>allocatedAmount</b></td>
        <td>integer</td>
        <td>
          AllocatedAmount specifies how much quota was actually allocated for this
request. Measured in the BaseUnit defined by the ResourceRegistration.
Currently always equals the requested amount or 0 (partial allocations not
supported).

Set to the requested amount when Status=Granted, 0 when Status=Denied or
Pending.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>allocatingBucket</b></td>
        <td>string</td>
        <td>
          AllocatingBucket identifies the AllowanceBucket that provided the quota for
this request. Set only when Status=Granted. Used for tracking and debugging
quota consumption.

Format: bucket name (generated as:
consumer-kind-consumer-name-resource-type-hash)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          Message provides a human-readable explanation of the allocation result.
Includes specific details about quota availability or validation errors.

Examples:

  - "Allocated 1 project from bucket organization-acme-projects"
  - "Insufficient quota: need 2048 bytes, only 1024 available"
  - "ResourceRegistration not found for resourceType"<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          Reason provides a machine-readable explanation for the current status.
Standard reasons include "QuotaAvailable", "QuotaExceeded",
"ValidationFailed".<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceClaim.status.conditions[index]
<sup><sup>[↩ Parent](#resourceclaimstatus)</sup></sup>



Condition contains details for one aspect of the current state of this API Resource.

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
        <td><b>lastTransitionTime</b></td>
        <td>string</td>
        <td>
          lastTransitionTime is the last time the condition transitioned from one status to another.
This should be when the underlying condition changed.  If that is not known, then using the time when the API field changed is acceptable.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          message is a human readable message indicating details about the transition.
This may be an empty string.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          reason contains a programmatic identifier indicating the reason for the condition's last transition.
Producers of specific condition types may define expected values and meanings for this field,
and whether the values are considered a guaranteed API.
The value should be a CamelCase string.
This field may not be empty.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          status of the condition, one of True, False, Unknown.<br/>
          <br/>
            <i>Enum</i>: True, False, Unknown<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          type of condition in CamelCase or in foo.example.com/CamelCase.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          observedGeneration represents the .metadata.generation that the condition was set based upon.
For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date
with respect to the current state of the instance.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>

## ResourceGrant
<sup><sup>[↩ Parent](#quotamiloapiscomv1alpha1 )</sup></sup>






ResourceGrant allocates quota capacity to a consumer for specific resource types.
Grants provide the allowances that AllowanceBuckets aggregate to determine
available quota for ResourceClaim evaluation.

### How It Works
**ResourceGrants** begin their lifecycle when either an administrator creates them manually or a
**GrantCreationPolicy** generates them automatically in response to observed resource changes. Upon
creation, the grant enters a validation phase where the quota system examines the consumer type
to ensure it matches the expected `ConsumerType` from each **ResourceRegistration** targeted by
the grant's allowances. The quota system also verifies that all specified resource types correspond
to active registrations and that the allowance amounts are valid non-negative integers.

When validation succeeds, the quota system marks the grant as `Active`, signaling to **AllowanceBucket**
resources that this grant should contribute to quota calculations. The bucket resources
continuously monitor for active grants and aggregate their allowance amounts into the appropriate
buckets based on consumer and resource type matching. This aggregation process makes the granted
quota capacity available for **ResourceClaim** consumption.

**ResourceClaims** then consume the capacity that active grants provide, creating a flow from grants
through buckets to claims. The grant's capacity remains reserved as long as claims reference it,
ensuring that quota allocations persist until the consuming resources are removed. This creates
a stable quota environment where capacity allocations remain consistent across resource lifecycles.

### Core Relationships
- **Provides capacity to**: AllowanceBucket matching (spec.consumerRef, spec.allowances[].resourceType)
- **Consumed by**: ResourceClaim objects processed against the aggregated buckets
- **Validated against**: ResourceRegistration for each spec.allowances[].resourceType
- **Created by**: Administrators manually or GrantCreationPolicy automatically

### Quota Aggregation Logic
Multiple ResourceGrants for the same (consumer, resourceType) combination:
- Aggregate into a single AllowanceBucket for that combination
- All bucket amounts from all allowances are summed for total capacity
- Only Active grants contribute to the aggregated limit
- Inactive grants are excluded from quota calculations

### Grant vs Bucket Relationship
- **ResourceGrant**: Specifies intended quota allocations
- **AllowanceBucket**: Aggregates actual available quota from active grants
- **ResourceClaim**: Consumes quota from buckets (which source from grants)

### Allowance Structure
Each grant can contain multiple allowances for different resource types:
- All allowances share the same consumer (spec.consumerRef)
- Each allowance can have multiple buckets (for tracking, attribution, or incremental increases)
- Bucket amounts within an allowance are summed for that resource type

### Manual vs Automated Grants
**Manual Grants** (created by administrators):
- Explicit quota allocations for specific consumers
- Require direct management and updates
- Useful for base quotas, special allocations, or testing

**Automated Grants** (created by GrantCreationPolicy):
- Generated based on resource lifecycle events
- Include labels/annotations for tracking policy source
- Automatically managed based on trigger conditions

### Validation Requirements
- Consumer type must match ResourceRegistration.spec.consumerType for each resource type
- All resource types must reference active ResourceRegistration objects
- Maximum 20 allowances per grant
- All amounts must be non-negative integers in BaseUnit

### Field Constraints and Limits
- Maximum 20 allowances per grant
- Each allowance must have at least 1 bucket
- Bucket amounts must be non-negative (0 is allowed but provides no quota)
- All amounts measured in BaseUnit from ResourceRegistration

### Status Information
- **Active condition**: Indicates whether grant is contributing to quota buckets
- **Validation errors**: Reported in condition message when Active=False
- **Processing status**: ObservedGeneration tracks spec changes

### Selectors and Filtering
- **Field selectors**: spec.consumerRef.kind, spec.consumerRef.name
- **Recommended labels** (add manually for better organization):
  - quota.miloapis.com/consumer-kind: Organization
  - quota.miloapis.com/consumer-name: acme-corp
  - quota.miloapis.com/source: policy-name or manual
  - quota.miloapis.com/tier: basic, premium, enterprise

### Common Queries
- All grants for a consumer: field selector spec.consumerRef.kind + spec.consumerRef.name
- Grants by source policy: label selector quota.miloapis.com/source=<policy-name>
- Grants by resource tier: label selector quota.miloapis.com/tier=<tier-name>
- Active vs inactive grants: check status.conditions[type=Active].status

### Cross-Cluster Allocation
GrantCreationPolicy can create grants in parent control planes for cross-cluster quota:
- Policy running in child cluster creates grants in parent cluster
- Grants provide capacity that spans multiple child clusters
- Enables centralized quota management across cluster hierarchies

### Troubleshooting
- **Inactive grants**: Check status.conditions[type=Active] for validation errors
- **Missing quota**: Verify grants are Active and contributing to correct buckets
- **Grant conflicts**: Multiple grants for same consumer+resourceType are aggregated, not conflicting

### Performance Considerations
- Large numbers of grants can impact bucket aggregation performance
- Consider consolidating grants where possible to reduce aggregation overhead
- Grant status updates are asynchronous and may lag spec changes

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
      <td>ResourceGrant</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#resourcegrantspec">spec</a></b></td>
        <td>object</td>
        <td>
          ResourceGrantSpec defines the desired state of ResourceGrant.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#resourcegrantstatus">status</a></b></td>
        <td>object</td>
        <td>
          ResourceGrantStatus reports the grant's operational state and processing status.
Controllers update status conditions to indicate whether the grant is active
and contributing capacity to AllowanceBuckets.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceGrant.spec
<sup><sup>[↩ Parent](#resourcegrant)</sup></sup>



ResourceGrantSpec defines the desired state of ResourceGrant.

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
        <td><b><a href="#resourcegrantspecallowancesindex">allowances</a></b></td>
        <td>[]object</td>
        <td>
          Allowances specifies the quota allocations provided by this grant.
Each allowance grants capacity for a specific resource type.
Minimum 1 allowance required, maximum 20 allowances per grant.

All allowances in a single grant:
- Apply to the same consumer (spec.consumerRef)
- Contribute to the same AllowanceBucket for each resource type
- Activate and deactivate together based on the grant's status<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#resourcegrantspecconsumerref">consumerRef</a></b></td>
        <td>object</td>
        <td>
          ConsumerRef identifies the quota consumer that receives these allowances.
The consumer type must match the ConsumerType defined in the ResourceRegistration
for each allowance resource type. The system validates this relationship.

Examples:
- Organization receiving Project quota allowances
- Project receiving User quota allowances
- Organization receiving storage quota allowances<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ResourceGrant.spec.allowances[index]
<sup><sup>[↩ Parent](#resourcegrantspec)</sup></sup>



Allowance defines quota allocation for a specific resource type within a ResourceGrant.
Each allowance can contain multiple buckets that sum to provide total capacity.

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
        <td><b><a href="#resourcegrantspecallowancesindexbucketsindex">buckets</a></b></td>
        <td>[]object</td>
        <td>
          Buckets contains the quota allocations for this resource type.
All bucket amounts are summed to determine the total allowance.
Minimum 1 bucket required per allowance.

Multiple buckets can be used for:
- Separating quota from different sources or tiers
- Managing incremental quota increases over time
- Tracking quota attribution for billing or reporting<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>resourceType</b></td>
        <td>string</td>
        <td>
          ResourceType identifies the specific resource type receiving quota allocation.
Must exactly match a ResourceRegistration.spec.resourceType that is currently active.
The quota system validates this reference when processing the grant.

The identifier format is flexible, as defined by platform administrators
in their ResourceRegistrations.

Examples:
- "resourcemanager.miloapis.com/projects"
- "compute_cpu"
- "storage.volumes"
- "custom-service-quota"<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ResourceGrant.spec.allowances[index].buckets[index]
<sup><sup>[↩ Parent](#resourcegrantspecallowancesindex)</sup></sup>



Bucket represents a single allocation of quota capacity within an allowance.
Each bucket contributes its amount to the total allowance for a resource type.

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
        <td><b>amount</b></td>
        <td>integer</td>
        <td>
          Amount specifies the quota capacity provided by this bucket.
Must be measured in the BaseUnit defined by the corresponding ResourceRegistration.
Must be a non-negative integer (0 is valid but provides no quota).

Examples:
- 100 (providing 100 projects)
- 2048000 (providing 2048000 bytes = 2GB)
- 5000 (providing 5000 CPU millicores = 5 cores)<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ResourceGrant.spec.consumerRef
<sup><sup>[↩ Parent](#resourcegrantspec)</sup></sup>



ConsumerRef identifies the quota consumer that receives these allowances.
The consumer type must match the ConsumerType defined in the ResourceRegistration
for each allowance resource type. The system validates this relationship.

Examples:
- Organization receiving Project quota allowances
- Project receiving User quota allowances
- Organization receiving storage quota allowances

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
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind specifies the type of consumer resource.
Must match an existing Kubernetes resource type that can receive quota grants.

Common consumer types:
- "Organization" (top-level quota consumer)
- "Project" (project-level quota consumer)
- "User" (user-level quota consumer)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name identifies the specific consumer resource instance.
Must match the name of an existing consumer resource in the cluster.

Examples:
- "acme-corp" (Organization name)
- "web-application" (Project name)
- "john.doe" (User name)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup specifies the API group of the consumer resource.
Use full group name for Milo resources.

Examples:
- "resourcemanager.miloapis.com" (Organization/Project resources)
- "iam.miloapis.com" (User/Group resources)
- "infrastructure.miloapis.com" (infrastructure resources)<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace identifies the namespace of the consumer resource.
Required for namespaced consumer resources (e.g., Projects).
Leave empty for cluster-scoped consumer resources (e.g., Organizations).

Examples:
- "" (empty for cluster-scoped Organizations)
- "organization-acme-corp" (namespace for Projects within an organization)
- "project-web-app" (namespace for resources within a project)<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceGrant.status
<sup><sup>[↩ Parent](#resourcegrant)</sup></sup>



ResourceGrantStatus reports the grant's operational state and processing status.
Controllers update status conditions to indicate whether the grant is active
and contributing capacity to AllowanceBuckets.

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
        <td><b><a href="#resourcegrantstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represents the latest available observations of the grant's state.
Controllers set these conditions to communicate operational status.

Standard condition types:
- "Active": Indicates whether the grant is operational and contributing to quota buckets.
  When True, allowances are aggregated into AllowanceBuckets and available for claims.
  When False, allowances do not contribute to quota decisions.

Standard condition reasons for "Active":
- "GrantActive": Grant is validated and contributing to quota buckets
- "ValidationFailed": Specification contains errors preventing activation (see message)
- "GrantPending": Grant is being processed by the quota system

Grant Lifecycle:
1. Created: Active=Unknown, reason=GrantPending
2. Validated: Active=True, reason=GrantActive OR Active=False, reason=ValidationFailed
3. Updated: Active condition changes only when validation results change<br/>
          <br/>
            <i>Validations</i>:<li>self.all(c, c.type == 'Active' ? c.reason in ['GrantActive', 'ValidationFailed', 'GrantPending'] : true): Active condition reason must be valid</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          ObservedGeneration indicates the most recent spec generation the quota system has processed.
When ObservedGeneration matches metadata.generation, the status reflects the current spec.
When ObservedGeneration is lower, the quota system is still processing recent changes.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceGrant.status.conditions[index]
<sup><sup>[↩ Parent](#resourcegrantstatus)</sup></sup>



Condition contains details for one aspect of the current state of this API Resource.

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
        <td><b>lastTransitionTime</b></td>
        <td>string</td>
        <td>
          lastTransitionTime is the last time the condition transitioned from one status to another.
This should be when the underlying condition changed.  If that is not known, then using the time when the API field changed is acceptable.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          message is a human readable message indicating details about the transition.
This may be an empty string.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          reason contains a programmatic identifier indicating the reason for the condition's last transition.
Producers of specific condition types may define expected values and meanings for this field,
and whether the values are considered a guaranteed API.
The value should be a CamelCase string.
This field may not be empty.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          status of the condition, one of True, False, Unknown.<br/>
          <br/>
            <i>Enum</i>: True, False, Unknown<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          type of condition in CamelCase or in foo.example.com/CamelCase.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          observedGeneration represents the .metadata.generation that the condition was set based upon.
For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date
with respect to the current state of the instance.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>

## ResourceRegistration
<sup><sup>[↩ Parent](#quotamiloapiscomv1alpha1 )</sup></sup>






ResourceRegistration enables quota tracking for a specific resource type.
Administrators create registrations to define measurement units, consumer relationships,
and claiming permissions.

### How It Works
- Administrators create registrations to enable quota tracking for specific resource types
- The system validates the registration and sets the "Active" condition when ready
- ResourceGrants can then allocate capacity for the registered resource type
- ResourceClaims can consume capacity when allowed resources are created

### Core Relationships
- **ResourceGrant.spec.allowances[].resourceType** must match this registration's **spec.resourceType**
- **ResourceClaim.spec.requests[].resourceType** must match this registration's **spec.resourceType**
- **ResourceClaim.spec.consumerRef** must match this registration's **spec.consumerType** type
- **ResourceClaim.spec.resourceRef** kind must be listed in this registration's **spec.claimingResources**

### Registration Lifecycle
1. **Creation**: Administrator creates **ResourceRegistration** with resource type and consumer type
2. **Validation**: System validates that referenced resource types exist and are accessible
3. **Activation**: System sets `Active=True` condition when validation passes
4. **Operation**: **ResourceGrants** and **ResourceClaims** can reference the active registration
5. **Updates**: Only mutable fields (`description`, `claimingResources`) can be changed

### Status Conditions
- **Active=True**: Registration is validated and operational; grants and claims can use it
- **Active=False, reason=ValidationFailed**: Configuration errors prevent activation (check message)
- **Active=False, reason=RegistrationPending**: Quota system is processing the registration

### Measurement Types
- **Entity registrations** (`spec.type=Entity`): Count discrete resource instances (**Projects**, **Users**)
- **Allocation registrations** (`spec.type=Allocation`): Measure capacity amounts (CPU, memory, storage)

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
- "user" (for Entity type tracking Users)<br/>
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
- "TB" (for large storage volumes)<br/>
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
- "custom-service-quota"<br/>
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
- `Feature`: A boolean entitlement grant used for org-level feature flags. No admission
  enforcement or claim machinery is used — the registration simply signals that a feature
  is available to an organization. Grants convey on/off entitlement rather than a numeric
  capacity.<br/>
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
- 1000000000000 (bytes to TB: 2000000000000 bytes displays as 2 TB)<br/>
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
- "Storage bytes claimed by volume requests"<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceRegistration.spec.claimingResources[index]
<sup><sup>[↩ Parent](#resourceregistrationspec)</sup></sup>



ClaimingResource identifies a resource type that can create **ResourceClaims**
for this registration. Uses unversioned references to remain valid across API version changes.

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
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind specifies the resource type that can create **ResourceClaims** for this registration.
Must match an existing resource type. Maximum 63 characters.

Examples:
- `Project` (**Project** resource creating claims for **Project** quota)
- `User` (**User** resource creating claims for **User** quota)
- `Organization` (**Organization** resource creating claims for **Organization** quota)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup specifies the API group of the resource that can create claims.
Use empty string for Kubernetes core resources (**Secret**, **ConfigMap**, etc.).
Use full group name for custom resources.

Examples:
- `""` (core resources like **Secret**, **ConfigMap**)
- `resourcemanager.miloapis.com` (custom resource group)
- `iam.miloapis.com` (Milo IAM resources)<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceRegistration.spec.consumerType
<sup><sup>[↩ Parent](#resourceregistrationspec)</sup></sup>



ConsumerType specifies which resource type receives grants and creates claims for this registration.
The consumer type must exist in the cluster before creating the registration.

Example: When registering "Projects per Organization", set `ConsumerType` to **Organization**
(apiGroup: `resourcemanager.miloapis.com`, kind: `Organization`). **Organizations** then
receive **ResourceGrants** allocating **Project** quota and create **ResourceClaims** when **Projects** are created.

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
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup specifies the API group of the quota consumer resource type.
Use empty string for Kubernetes core resources (**Secret**, **ConfigMap**, etc.).
Use full group name for custom resources (for example, `resourcemanager.miloapis.com`).
Must follow DNS subdomain format with lowercase letters, numbers, and hyphens.

Examples:
- `resourcemanager.miloapis.com` (**Organizations**, **Projects**)
- `iam.miloapis.com` (**Users**, **Groups**)
- `infrastructure.miloapis.com` (custom infrastructure resources)<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind specifies the resource type that receives quota grants and creates quota claims.
Must match an existing Kubernetes resource type (core or custom).
Use the exact Kind name as defined in the resource's schema.

Examples:
- **Organization** (receives **Project** quotas)
- **Project** (receives **User** quotas)
- **User** (receives resource quotas within projects)<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ResourceRegistration.status
<sup><sup>[↩ Parent](#resourceregistration)</sup></sup>



ResourceRegistrationStatus reports the registration's operational state and processing status.
The system updates status conditions to indicate whether the registration is active and
usable for quota operations.

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
        <td><b><a href="#resourceregistrationstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represents the latest available observations of the registration's state.
The system sets these conditions to communicate operational status.

Standard condition types:
- "Active": Indicates whether the registration is operational. When True, ResourceGrants
  and ResourceClaims can reference this registration. When False, quota operations are blocked.

Standard condition reasons for "Active":
- "RegistrationActive": Registration is validated and operational
- "ValidationFailed": Specification contains errors (see message for details)
- "RegistrationPending": Registration is being processed<br/>
          <br/>
            <i>Validations</i>:<li>self.all(c, c.type == 'Active' ? c.reason in ['RegistrationActive', 'ValidationFailed', 'RegistrationPending'] : true): Active condition reason must be valid</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          ObservedGeneration indicates the most recent spec generation that the system has processed.
When ObservedGeneration matches metadata.generation, the status reflects the current spec.
When ObservedGeneration is lower, the system is still processing recent changes.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ResourceRegistration.status.conditions[index]
<sup><sup>[↩ Parent](#resourceregistrationstatus)</sup></sup>



Condition contains details for one aspect of the current state of this API Resource.

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
        <td><b>lastTransitionTime</b></td>
        <td>string</td>
        <td>
          lastTransitionTime is the last time the condition transitioned from one status to another.
This should be when the underlying condition changed.  If that is not known, then using the time when the API field changed is acceptable.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          message is a human readable message indicating details about the transition.
This may be an empty string.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          reason contains a programmatic identifier indicating the reason for the condition's last transition.
Producers of specific condition types may define expected values and meanings for this field,
and whether the values are considered a guaranteed API.
The value should be a CamelCase string.
This field may not be empty.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          status of the condition, one of True, False, Unknown.<br/>
          <br/>
            <i>Enum</i>: True, False, Unknown<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          type of condition in CamelCase or in foo.example.com/CamelCase.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          observedGeneration represents the .metadata.generation that the condition was set based upon.
For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date
with respect to the current state of the instance.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>
