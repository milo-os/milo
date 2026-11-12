<!-- omit from toc -->
# Milo Quota System Architecture

- [System Overview](#system-overview)
  - [Purpose](#purpose)
  - [Design Principles](#design-principles)
  - [Key Components](#key-components)
  - [Runtime Architecture](#runtime-architecture)
- [Core Concepts](#core-concepts)
  - [ResourceRegistration](#resourceregistration)
  - [ResourceGrant](#resourcegrant)
  - [AllowanceBucket](#allowancebucket)
  - [ResourceClaim](#resourceclaim)
- [Policy Automation](#policy-automation)
  - [GrantCreationPolicy](#grantcreationpolicy)
  - [ClaimCreationPolicy](#claimcreationpolicy)
- [Data Flows](#data-flows)
  - [Quota Provisioning Flow](#quota-provisioning-flow)
  - [Admission Control Flow](#admission-control-flow)
  - [Resource Claiming Flow](#resource-claiming-flow)
- [Resource Cleanup](#resource-cleanup)
- [Operational Visibility](#operational-visibility)
  - [Resource state metrics](#resource-state-metrics)
  - [Quota System Implementation Metrics](#quota-system-implementation-metrics)
  - [Monitoring and Alerting](#monitoring-and-alerting)

## System Overview

The Milo quota system controls resource consumption in real time. The system
automates quota provisioning, enforces limits during resource creation, and
tracks usage across distributed deployments.

### Purpose

Platform administrators define resource consumption limits through the quota
system. The system enforces these limits without manual intervention and provides:

- **Real-time Enforcement**: Blocks resource creation when quota limits are
  exceeded
- **Automated Provisioning**: Dynamically allocates quota based on
  organizational policies
- **Multi-Resource Support**: Tracks consumption across different resource types
- **Distributed Operation**: Manages quota across multiple Milo deployments
- **Audit Trail**: Maintains complete history of quota decisions and allocations

### Design Principles

- **Declarative Configuration**: Define all quota rules through API resources,
  enabling version control and reproducible deployments
- **Event-Driven Architecture**: Components react to resource lifecycle events,
  ensuring quota decisions reflect real-time system state
- **Separation of Concerns**: Clear boundaries between quota allocation
  (grants), consumption (claims), and enforcement (admission control)
- **Controller-Based**: Uses reconciliation loops to maintain desired state and
  handle eventual consistency

### Key Components

The quota system includes four main component types:

- **Configuration**: ResourceRegistration defines which resource types the
  system tracks and which entity types consume them
- **Automation**: GrantCreationPolicy and ClaimCreationPolicy automate quota
  allocation and enforcement based on resource lifecycle events
- **Resources**: ResourceGrant allocates quota capacity, ResourceClaim requests
  quota consumption, and AllowanceBucket aggregates real-time availability
- **Runtime**: Controllers coordinate between resources and admission webhooks
  enforce quota decisions during resource creation

### Runtime Architecture

The quota system uses an event-driven architecture with different watching patterns optimized for each component:

**Controller Watches**:
- Controllers use standard Kubernetes informers for resource watching and caching
- AllowanceBucket Controller watches ResourceGrants and ResourceClaims to maintain aggregated quota state
- ResourceGrant Controller watches ResourceRegistrations to validate grant configurations
- Policy controllers watch trigger resources dynamically based on policy specifications
- All controllers use predicate filters to ignore irrelevant events

**Admission Plugin - One Watch per Waiting Create**:

The admission plugin waits for each claim it creates on a watch of that one claim, and keeps no state between
requests:

- **Watch from the create's version**: The plugin creates the ResourceClaim, then watches it by name from the
  resource version the create returned, with client-go's `watchtools.Until`. The watch cache accepts a version it
  has not reached yet and sends only later events, so no grant or denial after the create is missed.
- **One key**: The watch carries a `metadata.name` field selector, so the watch cache serves it from that claim's
  key alone, and the watcher receives only that claim's events.
- **410 Gone**: If the create's version has left the watch cache's window, the watch ends, and one GET decides: the
  claim's outcome if it has one, or a new watch from the GET's version. At most three such rounds per wait.
- **Restarts**: client-go's `RetryWatcher` restarts a failed watch at most once a second, honours a server's
  Retry-After, and ends the wait on a 403 or 401.
- **Lifetime**: A wait lasts at most the claim wait timeout (30 s by default) and ends with its request.
- **Kept denials**: Cleanup keeps a denied auto-created claim for 60 s from its creation, twice the wait, so a wait that must re-read
  its claim after a 410 still finds the denial and its reason. A retry of the same resource replaces a kept denied
  claim.

**Key Characteristics of the Claim Wait**:

1. **Deterministic Claim Naming**: A policy's template names the claim, so a retry of the same resource meets the
   claim of the previous attempt and resolves it (granted: admit; denied or stale pending: replace).
2. **Stateless Operation**: No cache and no per-project manager; the only state is the open waits of admission
   requests in flight.
3. **Safe Deletes**: Every delete of one claim, by the plugin, cleanup or the ownership controller, carries the UID
   and resourceVersion it judged, so it never removes a newer claim of the same name.
4. **Scalability**: One short watch per waiting create, not one long watch per project

**Shared Components**:
- **ResourceTypeValidator**: Uses informers to maintain a cache of all registered resource types
  - Shared across controllers and admission plugin to validate resource references
  - Asynchronously initialized to prevent startup blocking
  - Provides efficient type checking without API calls
- **Policy Engine**: Caches ClaimCreationPolicies using informers for fast lookup during admission
- **Grant Creation Engine**: Watches resources specified in GrantCreationPolicies
  - Dynamically creates informers based on policy trigger specifications
  - Evaluates CEL expressions against cached resources

This hybrid architecture ensures controllers benefit from informer caching for background reconciliation while the admission plugin achieves minimal latency and resource usage for synchronous request-time enforcement.

## Core Concepts

### ResourceRegistration

ResourceRegistration defines which resource types the system tracks and
establishes relationships between consumers and resources.

```yaml
apiVersion: quota.miloapis.com/v1alpha1
kind: ResourceRegistration
metadata:
  name: projects-per-organization
spec:
  consumerType:
    apiGroup: resourcemanager.miloapis.com
    kind: Organization
  type: Entity
  resourceType: resourcemanager.miloapis.com/projects
  baseUnit: project
  claimingResources:
  - apiGroup: resourcemanager.miloapis.com
    kind: Project
```

**Key Responsibilities:**
- Define resource type identity and measurement specifications
- Define consumer relationships (Organizations consume Project quota)
- Authorize which resources can create claims

### ResourceGrant

ResourceGrant allocates quota capacity to specific consumers. The system
automatically aggregates multiple grants for the same consumer and resource type.

```yaml
apiVersion: quota.miloapis.com/v1alpha1
kind: ResourceGrant
metadata:
  name: acme-corp-project-quota
spec:
  consumerRef:
    apiGroup: resourcemanager.miloapis.com
    kind: Organization
    name: acme-corp
  allowances:
  - resourceType: resourcemanager.miloapis.com/projects
    buckets:
    - amount: 50
```

**Key Responsibilities:**
- Allocate capacity to consumers
- Support multiple resources in single grants
- Attribute sources for billing and reporting

### AllowanceBucket

AllowanceBucket aggregates quota capacity from ResourceGrants and tracks
consumption from ResourceClaims. The bucket calculates real-time quota
availability.

**Status Example:**
```yaml
status:
  limit: 100        # From active ResourceGrants
  allocated: 25     # From granted ResourceClaims
  available: 75     # limit - allocated
  contributingGrantRefs:
  - name: basic-quota-grant
    amount: 50
  - name: bonus-quota-grant
    amount: 50
```

**Key Responsibilities:**
- Aggregate capacity from all active ResourceGrants
- Track consumption from all granted ResourceClaims
- Calculate available quota (Available = Limit - Allocated) for admission decisions
- Create buckets on-demand when first referenced

### ResourceClaim

ResourceClaim requests quota allocation during resource creation and links to
triggering resources for lifecycle management.

```yaml
apiVersion: quota.miloapis.com/v1alpha1
kind: ResourceClaim
metadata:
  name: web-app-project-claim-abc123
spec:
  consumerRef:
    apiGroup: resourcemanager.miloapis.com
    kind: Organization
    name: acme-corp
  requests:
  - resourceType: resourcemanager.miloapis.com/projects
    amount: 1
  resourceRef:
    apiGroup: resourcemanager.miloapis.com
    kind: Project
    name: web-app-project
```

**Key Responsibilities:**
- Request quota during resource creation
- Identify consumers and link to resources
- Ensure atomic all-or-nothing allocation for multi-resource requests

## Policy Automation

### GrantCreationPolicy

GrantCreationPolicy automates ResourceGrant creation based on resource lifecycle
events, eliminating manual quota provisioning.

```yaml
apiVersion: quota.miloapis.com/v1alpha1
kind: GrantCreationPolicy
metadata:
  name: organization-project-quota
spec:
  trigger:
    resource:
      apiVersion: resourcemanager.miloapis.com/v1alpha1
      kind: Organization
    constraints:
    - expression: 'trigger.status.phase == "Active"'
  target:
    resourceGrantTemplate:
      spec:
        consumerRef:
          apiGroup: resourcemanager.miloapis.com
          kind: Organization
          name: '{{ trigger.metadata.name}}'
        allowances:
        - resourceType: resourcemanager.miloapis.com/projects
          buckets:
          - amount: 50
```

**Capabilities:**
- Watch any Kubernetes resource type for changes
- Use CEL expressions for flexible trigger conditions
- Define templates with CEL expressions in `{{ }}` delimiters
- Create grants across clusters via parent context resolution

### ClaimCreationPolicy

ClaimCreationPolicy automates ResourceClaim creation during admission control to
enforce quota in real time.

```yaml
apiVersion: quota.miloapis.com/v1alpha1
kind: ClaimCreationPolicy
metadata:
  name: project-quota-enforcement
spec:
  trigger:
    resource:
      apiVersion: resourcemanager.miloapis.com/v1alpha1
      kind: Project
    constraints:
    - expression: 'trigger.spec.type == "application"'
  target:
    resourceClaimTemplate:
      spec:
        consumerRef:
          apiGroup: resourcemanager.miloapis.com
          kind: Organization
          name: '{{ trigger.spec.ownerRef.name }}'
        requests:
        - resourceType: resourcemanager.miloapis.com/projects
          amount: 1
```

**Capabilities:**
- Integrate with Kubernetes admission controller framework
- Create claims during resource creation requests
- Block resource creation when quota is exceeded
- Resolve consumers automatically via parent context

## Data Flows

### Quota Provisioning Flow

This flow shows how the system allocates quota capacity when new consumers are
created.

```mermaid
sequenceDiagram
    actor PlatformAdmin as Platform<br/>Administrator
    actor OrgOwner as Organization<br/>Owner
    participant API as API Server

    box Controller Manager
        participant GPC as GrantCreationPolicy<br/>Controller
        participant GCE as Grant Creation<br/>Engine
        participant GRC as ResourceGrant<br/>Controller
    end

    Note over PlatformAdmin,GRC: Setup Phase
    PlatformAdmin->>API: 1. Create ResourceRegistration
    Note over API: Stores "Projects per Organization"<br/>Consumer: Organization<br/>ResourceType: projects<br/>BaseUnit: project

    PlatformAdmin->>API: 2. Create GrantCreationPolicy
    Note over API: Stores policy with<br/>Trigger: Organization creation<br/>Template: Grant 100 projects

    API-->>GPC: 3. GrantCreationPolicy change event (watch)
    GPC->>GPC: 4. Validate policy (CEL expressions, templates, resource types)
    GPC->>API: 5. Update policy status
    Note over API: GrantCreationPolicy status:<br/>Ready=True condition

    API-->>GCE: 6. Policy status change event (watch)
    GCE->>API: 7. Load Ready policy into engine cache
    Note over GCE: Policy loaded and available<br/>for trigger processing

    Note over OrgOwner,GRC: Runtime Phase - New Organization Created
    OrgOwner->>API: 8. Create Organization "acme-corp"

    API-->>GCE: 9. Organization change event (watch)
    GCE->>GCE: 10. Lookup cached Ready policies for Organization
    Note over GCE: Uses cached Ready policies
    GCE->>GCE: 11. Evaluate trigger conditions
    Note over GCE: object.status.phase == "Active"<br/>CEL expression returns true

    GCE->>API: 12. Create ResourceGrant
    Note over API: Stores ResourceGrant<br/>Consumer: acme-corp<br/>Allowance: 100 projects<br/>Status: Pending

    API-->>GRC: 13. ResourceGrant change event (watch)
    GRC->>API: 14. Validate against ResourceRegistration
    GRC->>API: 15. Update ResourceGrant status
    Note over API: ResourceGrant status:<br/>Active=True condition
```

**Key Points:**
- **Trigger evaluation**: Grant Creation Engine watches for resource changes and
  evaluates CEL expressions
- **Template rendering**: Grant templates use context from triggering resources
- **Validation**: The system validates ResourceGrants against ResourceRegistrations
- **Bucket creation**: The system creates AllowanceBuckets on-demand when the
  first ResourceClaim references them

### Admission Control Flow

This flow shows how the admission webhook intercepts resource creation and
generates ResourceClaims for quota evaluation.

```mermaid
sequenceDiagram
    actor PlatformAdmin as Platform<br/>Administrator
    actor Developer as Developer
    participant API as API Server

    box API Server
        participant Plugin as Admission<br/>Plugin
        participant TE as Template<br/>Engine
    end

    box Controller Manager
        participant CCP as ClaimCreationPolicy<br/>Controller
    end

    Note over PlatformAdmin,CCP: Setup Phase
    PlatformAdmin->>API: 1. Create ClaimCreationPolicy
    Note over API: Stores policy with<br/>Trigger: Project creation<br/>Template: Claim 1 project quota

    API-->>CCP: 2. ClaimCreationPolicy change event (watch)
    CCP->>CCP: 3. Validate policy (CEL expressions, templates, resource types)
    CCP->>API: 4. Update policy status
    Note over API: ClaimCreationPolicy status:<br/>Ready=True condition

    API-->>Plugin: 5. Policy status change event (watch)
    Plugin->>API: 6. Load Ready policy into admission cache
    Note over Plugin: Policy loaded and available<br/>for admission enforcement

    Note over Developer,CCP: Admission Phase - Project Creation
    Developer->>API: 7. Create Project "web-app"
    API->>Plugin: 8. Admission webhook intercept

    Plugin->>Plugin: 9. Lookup cached Ready policies for Project
    Note over Plugin: Uses cached Ready policies

    Plugin->>Plugin: 10. Evaluate trigger conditions
    Note over Plugin: trigger.spec.type == "application"<br/>CEL expression returns true

    Plugin->>TE: 12. Render ResourceClaim template
    TE-->>Plugin: Rendered claim spec

    Plugin->>Plugin: 13. Generate deterministic claim name
    Note over Plugin: Hash-based name: policy + resource GVK + name

    Plugin->>API: 15. Create ResourceClaim with predetermined name
    Note over API: Stores ResourceClaim<br/>Consumer: acme-corp (resolved)<br/>Request: 1 project<br/>ResourceRef: web-app project

    Plugin->>API: 15b. Watch the claim by name from the create's resourceVersion
    Note over Plugin: The watch cache sends every event after the create;<br/>a 410 ends the watch and one GET decides

    alt Claim Eventually Granted
        Note over Plugin: ResourceClaim status:<br/>Granted=True condition observed via watch
        Plugin-->>Developer: 16a. Allow Project creation
        Note over API: Project "web-app" created successfully

    else Claim Eventually Denied
        Note over Plugin: ResourceClaim status:<br/>Granted=False condition observed via watch
        Plugin-->>Developer: 16b. Block Project creation (403 Forbidden)
        Note over Developer: "Insufficient quota resources available"
    end

    Note over Plugin: The watch ends with the outcome or the request
```

**Key Points:**
- **Synchronous admission**: The webhook blocks resource creation until the
  ResourceClaim is processed
- **Policy lookup**: Admission plugin maintains indexed ClaimCreationPolicies for
  fast lookup
- **Template context**: Claim templates access the triggering resource, user
  context, and request information
- **Watch from the create's version**: The plugin watches its claim from the
  resource version the create returned, so no outcome after the create is missed
- **No shared state**: Each wait holds one watch of one claim, for at most the
  claim wait timeout, and ends with its request

### Resource Claiming Flow

This flow shows how controllers asynchronously process ResourceClaims to make
quota decisions and update bucket allocations.

```mermaid
sequenceDiagram
    participant API as API Server

    box Controller Manager
        participant BC as Bucket<br/>Controller
        participant RCC as ResourceClaim<br/>Controller
    end

    Note over API,RCC: Asynchronous Claim Processing

    Note over API: ResourceClaim created by:<br/>• Admission Plugin (admission flow)<br/>• Controllers (policy-driven)<br/>• Administrators (manual)

    API-->>BC: 1. ResourceClaim change event (watch)
    alt AllowanceBucket exists
        BC->>API: 2a. Query existing AllowanceBucket
    else AllowanceBucket doesn't exist
        BC->>API: 2b. Create AllowanceBucket on-demand
        Note over API: Bucket Controller initializes bucket by<br/>aggregating active grants<br/>Limit=100, Allocated=0, Available=100
    end

    BC->>BC: 3. Process quota decision
    alt Quota Available (Available >= 1)
        BC->>API: 4a. Update ResourceClaim request allocations
        Note over API: Individual request status:<br/>Granted=True, AllocatedAmount=1<br/>AllocatingBucket: organization-acme-corp-projects

        BC->>API: 5a. Update AllowanceBucket status
        Note over API: AllowanceBucket status:<br/>Allocated=1, Available=99

        API-->>RCC: 6a. ResourceClaim allocation change event (watch)
        RCC->>RCC: 7a. Aggregate individual allocations
        RCC->>API: 8a. Update overall ResourceClaim status
        Note over API: Overall ResourceClaim status:<br/>Granted=True (all requests granted)

    else Quota Exceeded (Available < 1)
        BC->>API: 4b. Update ResourceClaim request allocations
        Note over API: Individual request status:<br/>Granted=False, Reason=QuotaExceeded

        API-->>RCC: 6b. ResourceClaim allocation change event (watch)
        RCC->>RCC: 7b. Aggregate individual allocations
        RCC->>API: 8b. Update overall ResourceClaim status
        Note over API: Overall ResourceClaim status:<br/>Granted=False (insufficient quota)
    end

    Note over API,RCC: Status available for claim creators
```

**Key Points:**
- **Asynchronous processing**: Controllers process ResourceClaims independently
  of admission
- **On-demand bucket creation**: The system creates AllowanceBuckets when the
  first ResourceClaim references them
- **Atomic decisions**: All requests within a ResourceClaim must be satisfied, or
  the system denies the entire claim
- **Controller separation**: Bucket Controller makes quota decisions;
  ResourceClaim Controller aggregates results

## Resource Cleanup

The quota system reclaims allocated quota through owner references and garbage
collection when resources are deleted:

**ResourceClaim Cleanup**:
- ResourceClaims include owner references to their triggering resources (e.g.,
  Project)
- When the triggering resource is deleted, the system automatically deletes the
  ResourceClaim
- Bucket Controller detects ResourceClaim deletion and updates AllowanceBucket
  allocated amounts
- Quota capacity becomes available for new claims immediately

**ResourceGrant Cleanup**:
- Policy-created ResourceGrants are cleaned up when their trigger resources are
  deleted
- Manual ResourceGrants persist until explicitly deleted by administrators
- When ResourceGrants are deleted, Bucket Controller recalculates
  AllowanceBucket limits
- Any ResourceClaims that exceed the new reduced limits are marked as denied

**AllowanceBucket Cleanup**:
- The system deletes AllowanceBuckets when no active ResourceGrants or
  ResourceClaims reference them
- Automatic deletion prevents resource accumulation and maintains clean quota state

The cleanup process reclaims quota capacity immediately without manual
intervention, maintaining accurate quota availability for future resource
creation requests.

## Operational Visibility

The quota system provides operational visibility through two complementary metric
systems: the resource metrics collector tracks resource state, and application-level metrics
monitor runtime behavior.

### Resource state metrics

The quota `ResourceMetricsPolicy` (`config/services/quota/telemetry/metrics/policy.yaml`) tells the resource metrics
collector which quota resources to read and how to turn their state into metrics. The collector reads project control
planes, so the policy covers the namespaced quota resources: allowance buckets, claims and grants. The cluster-scoped
ones (ResourceRegistration, ClaimCreationPolicy and GrantCreationPolicy) live on the root control plane, which the
collector reads only with `collectRootControlPlane` on, so the policy does not cover them.

**ResourceGrant Metrics**:
- `milo_quota_grant_info`: Grant metadata and consumer references
  - Labels: `uid`, `name`, `namespace`, `consumer_api_group`, `consumer_kind`, `consumer_name`
- `milo_quota_grant_status_condition`: Active status conditions
  - Labels: `condition`, `reason`, `status`
- `milo_quota_grant_observed_generation`: Controller processing progress
- `milo_quota_grant_current_generation`: Grant specification version

**ResourceClaim Metrics**:
- `milo_quota_claim_info`: Claim metadata with consumer and triggering resource references
  - Labels: `uid`, `name`, `namespace`, `consumer_api_group`, `consumer_kind`, `consumer_name`, `triggering_resource_api_group`, `triggering_resource_kind`, `triggering_resource_name`, `triggering_resource_namespace`
- `milo_quota_claim_status_condition`: Granted/Denied status conditions
  - Labels: `condition`, `reason`, `status`
- `milo_quota_claim_observed_generation`: Controller processing progress
- `milo_quota_claim_current_generation`: Claim specification version

**AllowanceBucket Metrics**:
- `milo_quota_bucket_info`: Bucket metadata and consumer references
  - Labels: `uid`, `name`, `namespace`, `consumer_api_group`, `consumer_kind`, `consumer_name`, `resource_type`
- `milo_quota_bucket_limit`: Total quota capacity from contributing grants
- `milo_quota_bucket_allocated`: Total quota consumed by granted claims
- `milo_quota_bucket_available`: Available quota capacity (limit - allocated)
- `milo_quota_bucket_claim_count`: Number of claims consuming from this bucket
- `milo_quota_bucket_grant_count`: Number of grants contributing to this bucket
- `milo_quota_bucket_last_reconciliation_timestamp`: Time of last bucket update
- `milo_quota_bucket_observed_generation`: Controller processing progress
- `milo_quota_bucket_current_generation`: Bucket specification version

Every series carries `name` and `namespace`, and the collector adds labels for where it read the resource, such as
`milo.project.name`. Only the `*_info` series carry the other labels above; join on `name` and `namespace` to group
another family by them.

### Quota System Implementation Metrics

The quota system exposes application-level Prometheus metrics that provide visibility into runtime behavior, performance, and operational health. These metrics are exposed on the standard Kubernetes `/metrics` endpoint.

**Admission Plugin Metrics**:

*Decision Tracking*:
- `milo_quota_admission_result_total`: Total admission decisions by outcome
  - Labels: `result` (granted|denied|policy_disabled), `policy_name`, `policy_namespace`, `resource_group`, `resource_kind`
  - Use case: Track quota enforcement patterns and denial rates per policy

*Claim Waits*:
- `milo_quota_admission_waiter_registrations_total`: Total claim waits started
  - Use case: Track admission request volume
- `milo_quota_admission_waiter_completions_total`: Claim waits completed, by result
  - Labels: `result` (granted|denied|timeout|deleted|watch_failed)
  - Use case: Track admission outcomes and timeout frequency
- `milo_quota_admission_waiters_current`: Current number of claim waits, each holding one claim watch (gauge)
  - Use case: Monitor concurrent admission request load
- `milo_quota_admission_waiter_duration_seconds`: Duration of a claim wait, from the claim's create to its outcome (histogram)
  - Labels: `result` (granted|denied|timeout|deleted|watch_failed)
  - Buckets: [0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60]
  - Use case: Track admission latency by outcome
- `milo_quota_admission_claim_wait_gets_after_gone_total`: Claim reads made because a wait's watch answered 410 Gone
  - Use case: Track how often a claim's version leaves the watch cache's window during a wait

The claim watches themselves show in the API server's own `apiserver_longrunning_requests` and
`apiserver_request_total` series, with `resource="resourceclaims"` and `verb="WATCH"`.

*Policy Engine Performance*:
- `milo_quota_policy_informer_events_total`: ClaimCreationPolicy events observed by shared informer
  - Labels: `type` (add|update|delete|unknown)
  - Use case: Track policy lifecycle events and cache updates
- `milo_quota_policy_load_total`: Total ClaimCreationPolicy load operations
  - Use case: Monitor policy cache refresh frequency
- `milo_quota_policies_active`: Current number of active ClaimCreationPolicy objects in cache
  - Use case: Track active policy count for capacity planning

**Metric Registration**: All quota system metrics register with the [Kubernetes
legacy registry](https://pkg.go.dev/k8s.io/component-base/metrics/legacyregistry)
(`k8s.io/component-base/metrics/legacyregistry`) during initialization. This
ensures metrics appear on the apiserver `/metrics` endpoint alongside standard
Kubernetes metrics.

**Implementation Locations**:
- Admission Plugin: `pkg/quota/admission/plugin.go`
- Claim Wait: `pkg/quota/admission/claim_wait.go`
- Claim Wait Metrics: `pkg/quota/admission/claim_wait_metrics.go`

### Monitoring and Alerting

The quota system metrics support monitoring through [Prometheus](https://prometheus.io/)
and visualization through [Grafana](https://grafana.com/). The following sections
provide [PromQL](https://prometheus.io/docs/prometheus/latest/querying/basics/)
queries for common operational scenarios.

**Key Monitoring Scenarios**:

*Quota Exhaustion Detection*:
```promql
# Organizations approaching quota limits (>90% utilized)
(milo_quota_bucket_allocated / milo_quota_bucket_limit) > 0.9
```

*Claim Wait Health*:
```promql
# Claim watches refused while a claim cache starts (each delays a wait by its Retry-After)
sum(rate(apiserver_request_total{resource="resourceclaims", verb="WATCH", code="429"}[5m]))

# Claim reads after a 410 (a claim's version left the watch cache's window during a wait)
rate(milo_quota_admission_claim_wait_gets_after_gone_total[5m])

# Waits that ended without an outcome other than a timeout
rate(milo_quota_admission_waiter_completions_total{result=~"deleted|watch_failed"}[5m])
```

*Admission Performance*:
```promql
# 95th percentile admission latency by result
histogram_quantile(0.95,
  rate(milo_quota_admission_waiter_duration_seconds_bucket[5m])
) by (result)

# Timeout rate (should be near zero)
rate(milo_quota_admission_waiter_completions_total{result="timeout"}[5m])

# High waiter counts indicating system load
milo_quota_admission_waiters_current > 100

# High denial rates
rate(milo_quota_admission_result_total{result="denied"}[5m])
```

*System Health*:
```promql
# Stale AllowanceBuckets (not reconciled in 5 minutes)
(time() - milo_quota_bucket_last_reconciliation_timestamp) > 300

# Policy cache stability
rate(milo_quota_policy_load_total[5m])
```

*Resource State Tracking*:
```promql
# Claims in pending state (condition status != True|False)
milo_quota_claim_status_condition{condition="Granted", status="Unknown"}
```

**Dashboard Recommendations**:
- **Quota Utilization**: Allocated vs limit per consumer, approaching capacity alerts
- **Claim Wait Health**: Claim watches refused with 429, reads after a 410, waits ended as deleted or watch_failed
- **Admission Latency**: Histograms by result type (granted/denied/timeout), p50/p95/p99 percentiles
- **System Load**: Open claim waits, claim WATCH request rate, policy cache size
- **Denial Analysis**: Denial rate trends with policy and resource type breakdowns
