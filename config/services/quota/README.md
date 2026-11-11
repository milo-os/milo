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

**Operational Metrics**: Status conditions, generation lag, controller health
**Business Metrics**: Quota limits/usage, capacity, allocation counts

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

#### ResourceGrant
- `milo_quota_grant_info` - Grant information
- `milo_quota_grant_status_condition` - Status conditions (condition, reason, status labels)
- `milo_quota_grant_observed_generation` - Observed generation
- `milo_quota_grant_current_generation` - Current generation

#### ResourceClaim
- `milo_quota_claim_info` - Claim information
- `milo_quota_claim_status_condition` - Status conditions (condition, reason, status labels)
- `milo_quota_claim_observed_generation` - Observed generation
- `milo_quota_claim_current_generation` - Current generation

The cluster-scoped quota resources (ResourceRegistration, ClaimCreationPolicy and GrantCreationPolicy) live on the root
control plane. The resource metrics collector reads project control planes, and the root control plane only with
`collectRootControlPlane` on, so this policy does not cover them.

### Metric Labels

**Every series**: `name`, and `namespace` for the namespaced resources (buckets, claims and grants). The collector adds
labels for where it read the resource, such as `milo.project.name`. No series carries a `component` label.

**Status conditions** (`*_status_condition`): `condition` (the condition's type), `reason` and `status`. The value is 1
when the status is True.

**Info series** (`*_info`) alone carry each resource's identity and spec. To group another family by them, join on
`name` (and `namespace`):
- `uid` - every info series
- `consumer_api_group`, `consumer_kind`, `consumer_name` - buckets, claims and grants
- `resource_type` - buckets
- `triggering_resource_api_group`, `triggering_resource_kind`, `triggering_resource_name`, `triggering_resource_namespace` - claims

### Monitoring

**Recommended Alerts**:
- Low quota: `milo_quota_bucket_available / milo_quota_bucket_limit < 0.2`
- High generation lag: `milo_quota_*_current_generation - milo_quota_*_observed_generation > 5`

**Alerts**: Located in `config/telemetry/alerts/quota/`, deployed to the infrastructure cluster.

## Deployment

Service configurations are automatically deployed to the Milo API server:

```bash
task kubectl -- apply -k config/services/
```

This deploys IAM roles, protected resources, and telemetry metric ResourceMetricsPolicy definitions.
 
