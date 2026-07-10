# PolicyBindingsNotReady

**Severity:** Critical · **Fires after:** 2m

## What This Alert Means

One or more `PolicyBinding` resources have a `Ready` condition that is not
`True`. A `PolicyBinding` grants a subject (User, Group, or ServiceAccount) a
role over some resource selector; while it is not Ready, the grant it
describes is not in effect — the intended subject does not have the access
the binding was meant to provide.

The alert fires on `iam_miloapis_com:policybindings:not_ready`, a count of
all `PolicyBinding` objects across the cluster with `status.conditions[type
== Ready].status != "True"`. It does not identify *which* binding by itself
— see Investigation below.

## Impact

Read the `reason` on the affected binding's `Ready` condition first (see
Investigation) before assuming user-facing impact — in practice this alert
is dominated by orphaned bindings left over from deleted Organizations,
Projects, or Users (see Common Causes), which nothing is actively trying to
use. Confirm real impact via:

```promql
sum by (allowed) (rate(openfga_check_result_count[10m]))
increase(apiserver_authorization_decisions_total{decision="denied"}[1h])
```

If neither shows an anomaly relative to the same window a day earlier,
treat this as accumulated readiness debt rather than an active incident,
and prioritize accordingly.

## Investigation

### 1. Find the affected bindings and why

```sh
kubectl get policybindings -A -o json | jq -r '
  .items[]
  | select(([.status.conditions[]? | select(.type=="Ready" and .status!="True")] | length) > 0)
  | [.metadata.namespace, .metadata.name, (.status.conditions[] | select(.type=="Ready") | .reason)]
  | @tsv'
```

The `reason` tells you which failure mode you're in:

| Reason | Meaning |
|---|---|
| `SubjectValidationFailed` | The bound User/Group/ServiceAccount no longer resolves (deleted, or UID mismatch). |
| `ResourceSelectorValidationFailed` | The resource the binding selects (an Organization, Project, etc.) no longer exists. |
| `RoleNotFoundForBinding` | `spec.roleRef` points at a Role that doesn't exist (in the given namespace). |

### 2. Check whether it's a known, self-healing orphan

Two orphan classes are handled automatically as of the fix for this
runbook (see Common Causes) and should not persist:

- A binding owned by an `OrganizationMembership` whose Organization was
  deleted — the membership self-deletes (and cascades to the binding)
  within one reconcile of being re-evaluated.
- A binding labeled `resourcemanager.miloapis.com/subject-user-name` whose
  User was deleted — the `subject-binding-reaper` controller deletes it on
  the User's delete event.

If a binding matches one of these shapes and is still present after a few
minutes, the reaping controller itself may be unhealthy — check
`milo-controller-manager` logs and restart count (see
[controller-manager-crash-looping.md](controller-manager-crash-looping.md)).

### 3. Anything else: check controller logs

```sh
kubectl logs -n <namespace> deploy/milo-controller-manager --tail=200 | grep -i policybinding
```

## Common Causes

- **Organization deleted while an `OrganizationMembership` still referenced
  it.** The membership's owned PolicyBinding has an `OwnerReference` to the
  *membership*, not the Organization, so it was never garbage collected —
  the membership just sat reporting `OrganizationNotFound` forever.
  `OrganizationMembershipController` now self-deletes the membership on the
  second reconcile that observes this (mirroring the pre-existing
  `UserNotFound` self-delete), which cascades to the binding via its owner
  reference.
- **Project's creating User deleted while the Project still exists.** The
  project-owner `PolicyBinding` created by `ProjectValidator`'s admission
  webhook (`internal/webhooks/resourcemanager/v1alpha1/project_webhook.go`)
  is owned by the `Project`, not the creating `User` — nothing reacted to
  the User's deletion. The webhook now labels the binding with
  `resourcemanager.miloapis.com/subject-user-name`, and a new
  `SubjectBindingReaperController` watches `User` deletes and reaps any
  binding labeled for that user.
- **Manually-applied bindings.** A few bindings in `milo-system`
  (`machineaccount-policy-binding`, `swells-instance-*-delete`) were
  applied by hand rather than by a controller — nothing owns their
  lifecycle at all. These need manual cleanup; `kubectl delete
  policybinding` once you've confirmed the referenced principal/resource is
  genuinely gone for good.
- **Role renamed or removed** while bindings still reference the old name
  (`RoleNotFoundForBinding`) — fix the `roleRef` or restore the Role.

## Resolution

- If the reason matches one of the two self-healing classes above and
  hasn't cleared within a few minutes, restart `milo-controller-manager`
  and re-check.
- If it's a manually-applied binding with no owner, verify the referenced
  principal/resource is really gone, then `kubectl delete policybinding
  <name> -n <namespace>`.
- If it's `RoleNotFoundForBinding`, fix or restore the Role.

## Related

- [organization-memberships-not-ready.md](organization-memberships-not-ready.md) —
  fires from the same root cause (Organization deleted before its
  memberships) and usually appears alongside this alert.
- [controller-manager-crash-looping.md](controller-manager-crash-looping.md) —
  if the self-healing controllers themselves aren't running.
