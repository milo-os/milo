# ProjectEventUserUnauthorized

**Severity:** Warning · **Fires after:** 30m

## What this alert means

Every controller manager replica asks Milo every 10 minutes whether it may create Project lifecycle events as the user its `--project-event-user` flag names. The default user is `system:milo:project-event-emitter`. This alert fires when Milo has refused for 30 minutes.

The flag ships with the controller manager to the hosting cluster. The RBAC that allows the user ships to Milo in `config/services/resourcemanager/project-events`. No single build sees both, so changing one without the other produces this alert.

## Impact

The next `Suspended` or `Reinstated` event will fail, and tenants won't see it in their activity feed. `ProjectLifecycleEventEmitFailed` fires once one does.

## Investigation steps

### 1. Read which check failed

```sh
kubectl logs -n milo-system -l app.kubernetes.io/component=controller-manager --tail=1000 | grep "Project lifecycle events will not reach Milo"
```

The error names the user and says which check Milo refused.

### 2. Compare the flag with Milo's RBAC

Find the controller manager's flag value. If the flag is absent, the default applies.

```sh
kubectl get deploy -n milo-system milo-controller-manager -o jsonpath='{.spec.template.spec.containers[0].args}' | tr ',' '\n' | grep project-event-user
```

Then read the binding subject and the impersonator role in Milo:

```sh
kubectl --kubeconfig <milo-kubeconfig> get clusterrolebinding resourcemanager.miloapis.com:project-event-emitter -o jsonpath='{.subjects}'
kubectl --kubeconfig <milo-kubeconfig> get clusterrole resourcemanager.miloapis.com:project-event-impersonator -o yaml
```

## Common causes and resolution

| Cause | Indicator | Resolution |
|---|---|---|
| The flag was changed and Milo's RBAC was not | The error says the user "cannot create events" | Patch the `project-event-emitter` binding subject, and the `users` resourceNames in `project-event-impersonator`, to the flag's value. Or set the flag back. |
| The project-events RBAC is missing from Milo | The binding is not found | Apply `config/services` to Milo. On a fresh install, wait for Flux to reconcile it. |
| The controller manager connects to Milo with a narrower identity than `system:masters` | The error says the controller manager "cannot impersonate" | Bind its Milo identity to `resourcemanager.miloapis.com:project-event-impersonator`. |
