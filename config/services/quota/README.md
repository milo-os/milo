# Quota Service Configuration

Service configuration for Milo's quota management system, providing comprehensive resource quota management across tenants with real-time enforcement.

## Overview

The quota system enables organizations to control and monitor resource usage through six core resource types working together to provide real-time quota enforcement. For detailed API documentation, see:

- [ResourceRegistration API](../../../docs/api/resourceregistrations.md)
- [ResourceGrant API](../../../docs/api/resourcegrants.md)
- [ResourceClaim API](../../../docs/api/resourceclaims.md)
- [AllowanceBucket API](../../../docs/api/allowancebuckets.md)
- [GrantCreationPolicy API](../../../docs/api/grantcreationpolicies.md)
- [ClaimCreationPolicy API](../../../docs/api/claimcreationpolicies.md)

## IAM Integration

Each quota resource type is registered as a ProtectedResource in Milo's IAM system, enabling fine-grained permission control and audit logging. Five predefined roles provide different access levels:

- **quota-admin**: Full access to all quota resources (platform administrators)
- **quota-manager**: Quota allocation and management (quota administrators)
- **quota-operator**: System automation access (controllers, automated systems)
- **quota-viewer**: Read-only monitoring access (monitoring systems, auditors)
- **organization-quota-manager**: Organization-scoped read access (organization administrators)

## Telemetry and Metrics

The quota system exports comprehensive metrics for monitoring quota usage and system health via ResourceMetricsPolicy resources. Metrics definitions are found in `telemetry/metrics/policy.yaml` under this directory and are automatically discovered and processed by the resource-metrics-collector for export.

Metrics fall into two categories:

**Operational Metrics**: Status conditions, generation lag, policy enablement, controller health
**Business Metrics**: Quota limits/usage, capacity, utilization ratios, allocation counts

### Exported Metrics

#### AllowanceBucket (Real-time Quota Tracking)
- `milo_quota_bucket_info` - Bucket information
- `milo_quota_bucket_limit` - Total quota capacity
- `milo_quota_bucket_allocated` - Consumed quota
- `milo_quota_bucket_available` - Remaining capacity
- `milo_quota_bucket_claim_count` - Number of active claims
- `milo_quota_bucket_grant_count` - Number of contributing grants
- `milo_quota_bucket_last_reconciliation_timestamp` - Last reconciliation time
- `milo_quota_bucket_observed_generation` - Observed generation
- `milo_quota_bucket_current_generation` - Current generation

#### ResourceRegistration
- `milo_quota_registration_info` - Registration information
- `milo_quota_registration_status_condition` - Status conditions (type, status labels)
- `milo_quota_registration_observed_generation` - Observed generation
- `milo_quota_registration_current_generation` - Current generation

#### ResourceGrant
- `milo_quota_grant_info` - Grant information
- `milo_quota_grant_status_condition` - Status conditions (type, status labels)
- `milo_quota_grant_observed_generation` - Observed generation
- `milo_quota_grant_current_generation` - Current generation

#### ResourceClaim
- `milo_quota_claim_info` - Claim information
- `milo_quota_claim_status_condition` - Status conditions (type, status labels)
- `milo_quota_claim_observed_generation` - Observed generation
- `milo_quota_claim_current_generation` - Current generation

#### ClaimCreationPolicy
- `milo_quota_claim_policy_info` - Policy information
- `milo_quota_claim_policy_status_condition` - Status conditions (type, status labels)
- `milo_quota_claim_policy_enabled` - Policy enabled status (1 = enabled, 0 = disabled)
- `milo_quota_claim_policy_observed_generation` - Observed generation
- `milo_quota_claim_policy_current_generation` - Current generation

#### GrantCreationPolicy
- `milo_quota_grant_policy_info` - Policy information
- `milo_quota_grant_policy_status_condition` - Status conditions (type, status labels)
- `milo_quota_grant_policy_enabled` - Policy enabled status (1 = enabled, 0 = disabled)
- `milo_quota_grant_policy_observed_generation` - Observed generation
- `milo_quota_grant_policy_current_generation` - Current generation

### Metric Labels

**Common Labels** (all metrics):
- `uid` - Unique resource identifier
- `component` - Always `quota_system`
- `resource_type` - Resource type being tracked

**Resource-Specific Labels**:
- `consumer_kind`, `consumer_name`, `consumer_api_group` - Consumer reference
- `registration_type` - Entity or Allocation (ResourceRegistration)
- `base_unit`, `display_unit` - Unit configuration (ResourceRegistration)
- `target_kind`, `target_api_version` - Target resource (Policies)
- `trigger_kind`, `trigger_api_version` - Trigger resource (Policies)
- `triggering_resource_*` - Resource that triggered the claim (ResourceClaim)
- `parent_context_*` - Cross-cluster targeting (GrantCreationPolicy)
- `enabled` - Policy enabled state (Policies)
- `has_parent_context` - Whether policy has parent context (GrantCreationPolicy)

### Monitoring

**Recommended Alerts**:
- Low quota: `milo_quota_bucket_available / milo_quota_bucket_limit < 0.2`
- Policy failures: `milo_quota_*_policy_status_condition{type="Ready", status="False"}`
- High generation lag: `milo_quota_*_current_generation - milo_quota_*_observed_generation > 5`

**Recording Rules and Alerts**: Located in `config/telemetry/recording-rules/quota/` and `config/telemetry/alerts/quota/`, deployed to the infrastructure cluster.

## Deployment

Service configurations are automatically deployed to the Milo API server:

```bash
task kubectl -- apply -k config/services/
```

This deploys IAM roles, protected resources, and telemetry metric ResourceMetricsPolicy definitions.
 
