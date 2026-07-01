# Unified organizations migration

Run after deploying milo CRD/controller changes and the matching datum quota
configuration for the target environment.

## Feature gate rollout

Unified organization behavior is controlled by the `UnifiedOrganizations` feature gate
(Alpha, default **false**) on both milo controller-manager and datum controller-manager.

| Component | Flag OFF (legacy) | Flag ON (unified) |
|-----------|-------------------|-------------------|
| milo controller-manager | Legacy org webhooks (user names, `spec.type` required) | System-assigned `org-` names, onboarding reconciler |
| datum controller-manager | PersonalOrganizationController active | Controller disabled |
| datum quota manifests | Default service config (personal/standard grant policies) | Apply `config/overlays/unified-organizations` |

Toggle per environment via `--feature-gates` / `FEATURE_GATES`:

```bash
# milo controller-manager / apiserver
FEATURE_GATES=UnifiedOrganizations=true

# datum controller-manager
--feature-gates=UnifiedOrganizations=true
```

Enable the gate and apply the unified quota overlay together in each environment
that opts in. Do not run both legacy and unified project quota grant policies
simultaneously.

## Prerequisites

- `kubectl` configured against the target Milo management cluster
- Milo version with deprecated `Organization.spec.type` and optional `spec.contactInfo`
- When `UnifiedOrganizations=false`: datum default service configuration (no extra overlay)
- When `UnifiedOrganizations=true`: datum `config/overlays/unified-organizations` applied

## Order of operations

1. Deploy milo and datum with `UnifiedOrganizations=false` (default). Production and
   other legacy environments need no gate or overlay changes.
2. To enable unified orgs in a target environment (e.g. staging):
   - Set `UnifiedOrganizations=true` on milo apiserver, milo controller-manager, and
     datum controller-manager
   - Add the `unified-organizations` component to datum-milo-customization
   - Run the migration steps below for existing organizations
3. Ship cloud-portal/staff-portal UI updates after backend rollout.

## 1. Strip legacy org type

Only when enabling unified orgs in an environment with existing organizations:

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

Organizations flip `status.conditions[OnboardingComplete]=True` once contactInfo,
a billing account, and a ready default payment method are present.

## 3. Bump personal org project quota grants (2 → 10)

When migrating existing personal organizations to unified quota:

```bash
kubectl get resourcegrants.quota.miloapis.com -A \
  -l quota.miloapis.com/policy=personal-organization-project-quota-policy
```

Patch each grant's project bucket to `10`, or delete the grant and let the unified
policy recreate it if your environment supports that.

## 4. Verify onboarding status

```bash
kubectl get organizations.resourcemanager.miloapis.com \
  -o custom-columns=NAME:.metadata.name,ONBOARDING:.status.conditions[?(@.type==\'OnboardingComplete\')].status,REASON:.status.conditions[?(@.type==\'OnboardingComplete\')].reason
```

## Rollback notes

- Do not rename legacy `personal-org-*` slugs in v1.
- Portal clients must stop sending/reading `spec.type` before CRD removal ships.
- To roll back an environment: disable `UnifiedOrganizations`, remove the datum
  unified-organizations overlay, and redeploy the default datum service bundle.
