# ProjectReinstatementFailed

**Severity:** Warning · **Fires after:** 5m

## What This Alert Means

A project's status condition `Suspended` is currently `True` (meaning the project is suspended), but there are no active `ProjectSuspension` resources targeting it in the cluster. This indicates that the project has failed to return to the active state after its suspensions were removed or lifted.

## Impact

The project remains suspended and inaccessible to the user even though all active suspensions have been removed. This results in service denial for the tenant and a bad user experience.

## Investigation Steps

### 1. Identify the affected project
The alert label `resource_name` identifies the project that is stuck in the suspended state.

### 2. Check the project status conditions
Verify the project's current status and conditions:
```sh
kubectl get project <resource_name> -o yaml
```
Look at the `.status.conditions` array. The `Suspended` condition type will have `status: "True"`. Check the `message` and `reason` fields for details.

### 3. Check for any remaining ProjectSuspension resources
Verify if there are any active suspensions targeting this project that Prometheus might have missed:
```sh
kubectl get projectsuspensions -A -o yaml | grep <resource_name>
```
If no suspensions are returned (or all have `status.phase: Lifted`), then the project should indeed be active.

### 4. Inspect controller manager logs
Check the `milo-controller-manager` logs for errors related to reconciling the project's suspension status:
```sh
kubectl logs -n milo-system -l app.kubernetes.io/component=controller-manager --tail=500 | grep <resource_name>
```
Look for:
- **Patch failures**: The controller failing to patch the project's `Suspended` condition to `False` (e.g. optimistic locking/conflict errors).
- **Controller blockages**: Any panic or loop blocking the manager from processing the project.

## Common Causes & Resolution

| Cause | Indicator | Resolution |
|---|---|---|
| Reconcile patch failure | "the object has been modified" or patch errors in logs | The controller manager should retry automatically. If stuck, restart the manager pod to force a reconciliation. |
| Feature gate disabled | Propagator does not run or reconcile | Ensure the `ProjectSuspension` feature gate is enabled in the controller manager configuration. |
| Missing indexer | List operations fail or don't return accurate status | Ensure the field indexer for `spec.projectRef.name` is configured correctly on startup. |
