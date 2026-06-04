# ProjectStuckCreatingSLOViolation

## What This Alert Means

A project has been in a "creating" state for more than 60 seconds without
reaching a "Ready" status. This exceeds the service level objective (SLO) for
project creation and indicates something is preventing the project from being
fully provisioned.

The alert fires per-project, so multiple alerts may fire simultaneously if
several projects are affected.

## Impact

Users who created the affected project(s) are waiting longer than expected.
The project may not be usable until it reaches a Ready state.

## Investigation Steps

### 1. Identify the affected project

The alert labels include `resource_name`, which identifies the project that is
stuck. Note this name for use in subsequent steps.

### 2. Check the project status

Use `kubectl` to inspect the project resource and its status conditions:

```sh
kubectl get project <resource_name> -o yaml
```

Look at `.status.conditions` for any condition with `status: "False"` or a
`reason` and `message` that explain what is failing.

### 3. Check controller manager logs

The `milo-controller-manager` is responsible for reconciling projects. Check its
logs for errors related to the affected project:

```sh
kubectl logs -l app=milo-controller-manager --tail=200 | grep <resource_name>
```

Look for:
- **Permission errors** (e.g., RBAC forbidden): The controller may lack
  permissions to create dependent resources.
- **Resource creation failures**: Errors when creating namespaces,
  ProjectControlPlane resources, or other dependent objects.
- **OOMKilled or CrashLoopBackOff**: The controller pod itself may be
  unhealthy.

### 4. Check controller pod health

Verify the controller manager pod is running and not restarting:

```sh
kubectl get pods -l app=milo-controller-manager
```

If the pod is restarting, check its resource limits and recent events:

```sh
kubectl describe pod -l app=milo-controller-manager
```

### 5. Check for upstream dependencies

Project creation depends on several subsystems. Verify these are healthy:
- **ProjectControlPlane** resources are being created and reconciled.
- **Authorization system** (e.g., OpenFGA) is reachable and responding.
- **Infrastructure cluster** connectivity is functioning.

### 6. Check for resource conflicts

If multiple controllers or deployment systems manage overlapping resources
(e.g., ClusterRoles, ConfigMaps), one may overwrite changes made by another.
Check for recent changes to RBAC resources:

```sh
kubectl get clusterrole -l app=milo-controller-manager -o yaml
```

Look for unexpected annotations or labels that indicate a different system is
managing the same resource.

## Common Causes

| Cause | Indicators |
|---|---|
| RBAC permission errors | "forbidden" errors in controller logs |
| Controller OOM crashes | Pod restarts, OOMKilled events |
| Authorization service unavailable | Timeout or connection errors in logs |
| Resource ownership conflicts | Oscillating resource annotations/labels |
| High reconciliation backlog | Many projects stuck simultaneously, controller processing slowly |

## Resolution

Resolution depends on the root cause identified above:

- **Permission errors**: Verify and restore the correct RBAC configuration for
  the controller.
- **Controller crashes**: Increase memory limits or investigate the source of
  excessive memory consumption.
- **Service unavailability**: Restore connectivity to dependent services.
- **Resource conflicts**: Ensure each deployment system manages uniquely named
  resources to avoid collisions.

After resolving the underlying issue, affected projects should automatically
reconcile and reach a Ready state. Monitor the alert to confirm it resolves.

## Escalation

If the alert persists after investigation and you cannot identify the root cause,
escalate to the platform engineering team with the following information:

- The affected project name(s)
- Controller manager logs from the time of the alert
- Status of the controller manager pod(s)
- Any error messages found during investigation
