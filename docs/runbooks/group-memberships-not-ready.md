# GroupMembershipsNotReady

**Severity:** Critical · **Fires after:** 2m

## What This Alert Means

One or more `GroupMembership` resources have a `Ready` condition that is not `True`. The alert fires on `iam_miloapis_com:groupmemberships:not_ready`, which means that there are group memberships failing to synchronize with the underlying OpenFGA authorization provider.

## Impact

A membership stuck not-Ready means the user is not being successfully added to (or verified in) the specified group in the authorization system. This directly breaks access control, as the user will lack any permissions granted to that group.

## Investigation

### 1. Find the affected membership(s)

To find the specific `GroupMembership` resources that are not ready:

```sh
kubectl get groupmemberships -A -o json | jq -r '
  .items[]
  | select(([.status.conditions[]? | select(.type=="Ready" and .status!="True")] | length) > 0)
  | [.metadata.namespace, .metadata.name, (.status.conditions[] | select(.type=="Ready") | .reason)]
  | @tsv'
```

### 2. Check the reason

Look at the `Ready` condition's reason and the specific reference validation conditions (`UserRefValid` and `GroupRefValid`):

| Reason | Meaning |
|---|---|
| `ReferenceInvalid` | The referenced User or Group does not exist or is invalid. |

You can inspect a specific membership for more details:
```sh
kubectl describe groupmembership <name> -n <namespace>
```
Look for `Warning` events (e.g., `OpenFGAError`) and the status of the `UserRefValid` and `GroupRefValid` conditions.

## Common Causes

- **Invalid User Reference:** The `User` specified in `spec.userRef.name` does not exist.
- **Invalid Group Reference:** The `Group` specified in `spec.groupRef.name` does not exist in the specified namespace.
- **OpenFGA Synchronization Issues:** The controller failed to write the tuple to OpenFGA (e.g., due to network issues or invalid model).

## Resolution

1. If the reason is `ReferenceInvalid`, verify that the referenced `User` and `Group` resources actually exist:
   ```sh
   kubectl get user <user-name>
   kubectl get group <group-name> -n <group-namespace>
   ```
2. If the resources are missing, either create them or delete the orphaned `GroupMembership`.
3. If it's an `OpenFGAError`, check the controller logs for more details on why the write failed:
   ```sh
   kubectl logs -l app.kubernetes.io/name=milo-controller-manager -n <namespace> | grep -i groupmembership
   ```
