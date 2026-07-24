# OrganizationMembershipsNotReady

**Severity:** Warning · **Fires after:** 5m

## What This Alert Means

One or more `OrganizationMembership` resources have a `Ready` condition that
is not `True`. The alert fires on
`resourcemanager_miloapis_com:organizationmemberships:not_ready`, grouped by
`resource_namespace` (the `organization-<name>` namespace the membership
lives in), so the affected organization is directly visible in the alert's
`Organization namespace` field.

## Impact

A membership stuck not-Ready means the role bindings it manages for that
user (see `reconcileRoles` in
`internal/controllers/resourcemanager/organization_membership_controller.go`)
are not being kept in sync — new roles won't be applied and stale ones
won't be removed. In practice, the dominant cause (see below) is a
membership left behind after its Organization was deleted, which affects no
one currently using the platform. Cross-check impact the same way as
[policy-bindings-not-ready.md](policy-bindings-not-ready.md#impact) — this
alert and that one usually fire together from the same root cause.

## Investigation

### 1. Find the affected membership(s)

The alert's `Organization namespace` label names the namespace directly:

```sh
kubectl get organizationmemberships -n <organization-namespace-from-alert> -o wide
```

Or across all namespaces:

```sh
kubectl get organizationmemberships -A -o json | jq -r '
  .items[]
  | select(([.status.conditions[]? | select(.type=="Ready" and .status!="True")] | length) > 0)
  | [.metadata.namespace, .metadata.name, (.status.conditions[] | select(.type=="Ready") | .reason)]
  | @tsv'
```

### 2. Check the reason

| Reason | Meaning |
|---|---|
| `OrganizationNotFound` | The referenced Organization was deleted. Self-deletes automatically — see Common Causes. |
| `UserNotFound` | The referenced User was deleted. Self-deletes automatically (pre-existing behavior). |
| `ReconcileError` | Transient API error reaching the Organization or User. Check controller logs. |

### 3. If it's not resolving on its own

`OrganizationNotFoundReason` and `UserNotFoundReason` both use a two-pass
self-delete: the controller sets the reason on the first reconcile, then
deletes the membership on the *next* reconcile that observes the same
reason still set. If a membership has been sitting on one of these reasons
for more than a couple of minutes, the controller isn't getting re-enqueued
or isn't running — check `milo-controller-manager` health:

```sh
kubectl get pods -l app.kubernetes.io/name=milo-controller-manager
kubectl logs -n <namespace> deploy/milo-controller-manager --tail=200 | grep -i organizationmembership
```

## Common Causes

- **Organization deleted while a membership still referenced it.**
  Previously, `OrganizationMembershipController` only self-deleted a
  membership when its *User* went away (`UserNotFoundReason`) — there was
  no equivalent for `OrganizationNotFoundReason`, so memberships (and the
  PolicyBindings they own) were orphaned forever once their Organization
  was deleted. The controller now applies the same two-pass self-delete to
  `OrganizationNotFoundReason`. This is almost always the reason you'll see
  this alert alongside `PolicyBindingsNotReady`.
- **User deleted.** Pre-existing, working self-delete behavior —
  investigate only if it isn't clearing within a couple of reconciles.

## Resolution

Resolution is automatic for both reasons above once
`milo-controller-manager` is healthy and reconciling. If a membership has
been stuck for longer than a few minutes on `OrganizationNotFound` or
`UserNotFound`, restart the controller and re-check before investigating
further:

```sh
kubectl rollout restart deploy/milo-controller-manager -n <namespace>
```

## Related

- [policy-bindings-not-ready.md](policy-bindings-not-ready.md) — the
  membership's owned PolicyBinding shows up here too until the membership
  self-deletes.
- [controller-manager-crash-looping.md](controller-manager-crash-looping.md)
