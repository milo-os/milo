# ProjectStuckDeleting

## What This Alert Means

A deleted project has waited more than an hour for the resources in its control plane to drain, and at least one
resource still holds it. A normal deletion drains in minutes.

The project controller deletes everything in the project's control plane, and it never removes a finalizer. Each
finalizer belongs to a component, and the project waits until that component removes it. So a finalizer that no
component serves holds the project for good.

The alert fires per project.

## Impact

The project stays in its deletion state. Its name cannot be used again, and whatever still lives in its control
plane keeps running until the deletion finishes.

## Investigation Steps

### 1. Read what holds the project

The alert's `project` label names the project. Its `ResourceCleanup` condition names each resource that still exists
and the finalizers on it:

```sh
kubectl get project <project> -o jsonpath='{.status.conditions[?(@.type=="ResourceCleanup")].message}'
```

For example: `Waiting for project resources to be removed: configmaps "default/example" (finalizers:
example.com/cleanup)`. A namespace named there without finalizers is still draining its own contents.

### 2. Find the component that owns each finalizer

A finalizer's prefix names the component that adds and removes it. Check that the component runs and reconciles the
resource: its logs, its reconcile errors and its own status on the resource.

### 3. Decide

- **The component runs but fails.** Fix the component; the project finishes on its own once the finalizer goes.
- **No component serves the finalizer**, for example a test value or a component that was removed. Remove that one
  finalizer from that one resource, with the project owner's consent or as staff. Remove nothing else.

## Resolution

The alert resolves when the project is gone, or when nothing holds it any more.
