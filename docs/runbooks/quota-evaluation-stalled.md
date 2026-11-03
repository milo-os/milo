# QuotaClaimsStalled / QuotaAdmissionFailing

**Severity:** Warning

## What These Alerts Mean

Every create that consumes quota waits up to 30 seconds in admission for its
`ResourceClaim` to be granted. When the quota controllers in
`milo-controller-manager` stop evaluating claims, those creates are refused
with:

> Your request took too long to be checked against your quota. Please try
> again in a moment

Controllers that create objects on a user's behalf (instances, network
interfaces, IP claims) retry, so users see rollouts stall rather than a
single error.

- **QuotaClaimsStalled** counts claims that have stayed `PendingEvaluation`
  for more than two minutes. It comes from the claims themselves, so it covers
  every resource type no matter which API server admitted it.
- **QuotaAdmissionFailing** fires when more than 10% of quota admission checks
  on one API server time out or fail over 5 minutes. It only sees API servers
  whose metrics are scraped.

A claim over its limit is denied, not left pending, so neither alert fires on
real quota exhaustion.

## Impact

Creates that need quota fail across every project until evaluation resumes.
Existing resources are unaffected.

## Investigation

### 1. Check whether the controller manager just restarted

```sh
kubectl -n <namespace> get pods -l app.kubernetes.io/name=milo-controller-manager
kubectl -n <namespace> logs <pod> --previous | grep -E 'leaderelection lost|Failed to renew lease'
```

`leaderelection lost` after `Failed to renew lease ... context deadline
exceeded` means a lease renewal to the Milo API server was too slow. The new
leader syncs caches for every project before it evaluates claims, which takes
minutes. The alerts clear on their own once it catches up.

### 2. Find the stalled claims

```promql
count by ("milo.project.name") (
  milo_quota_claim_status_condition{condition="Granted", reason="PendingEvaluation"}
)
```

Claims spread across many projects point at the controller. Claims in one
project point at that project's buckets or grants.

### 3. If the controller is healthy

Check Milo API server latency and the bucket controller's queue. A slow
control plane delays evaluation without any restart.

## Common Causes

- The controller manager losing its leader lease to a slow API server.
- A Milo rollout, while the new leader syncs.
- The resource-metrics exporter not scraping, which silences
  QuotaClaimsStalled without fixing anything. Confirm the exporter is up
  before treating silence as recovery.
