# ProjectSuspensionNotReady

**Severity:** Warning · **Fires after:** 5m

## What This Alert Means

An active `ProjectSuspension` has been declared for a project, but the project's status condition `Suspended` has remained `False` (active) for over 5 minutes. This indicates that the suspension has failed to propagate to the project, leaving the project active when it should be suspended.

## Impact

The project remains accessible and active despite a suspension request. This could lead to policy violations, financial exposure (for unpaid accounts), or ongoing abuse if the suspension was triggered for fraud or compliance reasons.

## Investigation Steps

### 1. Identify the affected project and suspension
The alert labels include:
- `project`: The name of the project that should be suspended.
- `resource_name`: The name of the `ProjectSuspension` resource.
- `reason`: The reason for the suspension (e.g., Abuse, Fraud, Billing).

### 2. Check the project status and conditions
Verify the project's current status conditions:
```sh
kubectl get project <project> -o yaml
```
Look at the `.status.conditions` array, specifically the `Suspended` condition. It should have `status: "False"`, which is causing the alert.

### 3. Check the ProjectSuspension resource
Verify the suspension exists and is not lifted:
```sh
kubectl get projectsuspension <resource_name> -o yaml
```
Verify that the `spec.projectRef.name` matches the project, and `status.phase` is empty or `Active` (not `Lifted`).

### 4. Check controller manager logs
The `project-suspension-controller` (part of `milo-controller-manager`) is responsible for propagating suspensions. Check its logs for errors related to the project:
```sh
kubectl logs -n milo-system -l app.kubernetes.io/component=controller-manager --tail=500 | grep "reconciling project suspension status"
```
Look for errors such as:
- **Patch failures**: Failure to patch the project status or conditions (e.g. conflict errors, permission errors).
- **List failures**: The controller failing to list `ProjectSuspension` resources.

## Common Causes & Resolution

| Cause | Indicator | Resolution |
|---|---|---|
| Controller lacks patch permissions | "forbidden" or "RBAC" errors in logs | Update the controller-manager RBAC roles to grant status patching permissions. |
| Resource version conflict | "the object has been modified" in logs | The controller will automatically retry. If stuck, restart the controller pod. |
| Feature gate disabled | Reconciler does not log any activity for suspensions | Ensure the `ProjectSuspension` feature gate is enabled in the controller manager configuration. |
