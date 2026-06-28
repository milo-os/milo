# Unified organizations migration

Run after deploying milo CRD/controller changes and the datum unified quota grant policy.

## Prerequisites

- `kubectl` configured against the target Milo management cluster
- Milo version with unified `Organization` schema (no `spec.type`, optional `spec.contactInfo`)
- Datum unified `organization-project-quota-policy` applied

## Order of operations

1. Apply new milo CRDs and controller-manager (organization onboarding reconciler, mutating webhook).
2. Apply datum `organization-project-quota-policy` **before** removing legacy personal/standard grant policies.
3. Run the migration steps below.
4. Remove legacy grant policies and the personal organization controller deployment.
5. Ship cloud-portal/staff-portal UI updates.

## 1. Strip legacy org type

```bash
kubectl get organizations.resourcemanager.miloapis.com -o name | while read -r org; do
  kubectl patch "$org" --type=json -p='[{"op":"remove","path":"/spec/type"}]' 2>/dev/null || true
done
```

## 2. Backfill org contactInfo from owner user

For each organization, find the owner membership and copy user email/name:

```bash
# Inspect one org first:
# kubectl get organizationmemberships -A -l ... 
# kubectl get organizationmemberships -A -o json | jq '.items[] | select(.spec.organizationRef.name=="<org>")'
```

Patch template (adjust values per org):

```bash
kubectl patch organization/<org-name> --type=merge -p '{
  "spec": {
    "contactInfo": {
      "email": "user@example.com",
      "name": "Jane Doe"
    }
  }
}'
```

Legacy orgs with billing + payment method will flip `status.conditions[OnboardingComplete]=True` once contactInfo is present.

## 3. Bump personal org project quota grants (2 → 10)

Identify grants created by the old personal policy:

```bash
kubectl get resourcegrants.quota.miloapis.com -A \
  -l quota.miloapis.com/policy=personal-organization-project-quota-policy
```

Patch each grant's project bucket to `10`, or delete the grant and let the unified policy recreate on next org reconcile if your environment supports that.

## 4. Verify onboarding status

```bash
kubectl get organizations.resourcemanager.miloapis.com \
  -o custom-columns=NAME:.metadata.name,ONBOARDING:.status.conditions[?(@.type==\'OnboardingComplete\')].status,REASON:.status.conditions[?(@.type==\'OnboardingComplete\')].reason
```

## Rollback notes

- Do not rename legacy `personal-org-*` slugs in v1.
- Portal clients must stop sending/reading `spec.type` before CRD removal ships.
