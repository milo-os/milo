# ProjectLifecycleEventEmitFailed

**Severity:** Warning · **Fires after:** the first failure in a 30-minute window

## What this alert means

The controller manager failed to create a Project `Suspended` or `Reinstated` event in Milo. The alert's `reason` label names which one. The controller logs the failure and carries on, so the suspension itself still took effect.

## Impact

Tenants don't see the suspension or reinstatement in their project's activity feed. Project status still shows the `Suspended` condition.

## Investigation steps

### 1. Read the controller manager's error

```sh
kubectl logs -n milo-system -l app.kubernetes.io/component=controller-manager --tail=1000 | grep "failed to emit project lifecycle event"
```

The error carries the API server's response. `forbidden` points at RBAC. Anything else, such as a timeout or a 5xx, points at Milo or the events proxy.

### 2. Check whether the access check also fails

If `ProjectEventUserUnauthorized` is firing too, the cause is RBAC. Follow [its runbook](project-event-user-unauthorized.md).

### 3. Check the events proxy

With the `EventsProxy` feature gate on, Milo forwards events to the Activity API server. Check that the Activity API server is up and that Milo's apiserver logs show no events proxy errors.

## Common causes and resolution

| Cause | Indicator | Resolution |
|---|---|---|
| The project event user lacks event create | `forbidden` in the error, and `ProjectEventUserUnauthorized` firing | Follow the `ProjectEventUserUnauthorized` runbook. |
| Activity API server unavailable | Timeouts or 5xx in the error | Restore the Activity API server. Events created during the outage are lost; the controller does not retry them. |
