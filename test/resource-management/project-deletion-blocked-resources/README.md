# Test: `project-deletion-blocked-resources`

Tests that deleting a Project does not strand resources that are still
being finalized in its control plane.

Consumers put finalizers on the resources they create for a project, and
resolve their own state through the project's namespaces. A project that
finishes deleting while those resources are still held leaves them with a
finalizer that can never run, and a namespace removed ahead of its contents
breaks the guarantee that a namespace outlives what lives in it.

This test verifies, for the two places project resources live:
- A resource held by a finalizer in the "default" namespace, which the API
  server does not allow to be deleted, keeps the project in Terminating.
- A resource held by a finalizer in a project namespace keeps both the
  namespace and the project alive, so the namespace outlives its contents.
- The ResourceCleanup condition names the blocking resource and the
  finalizer holding it, so the component responsible is identifiable.
- Once the finalizers are released, both projects delete promptly.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [create-projects](#step-create-projects) | 0 | 4 | 0 | 0 | 0 |
| 2 | [seed-held-resource-in-default](#step-seed-held-resource-in-default) | 0 | 1 | 0 | 0 | 0 |
| 3 | [seed-held-resource-in-namespace](#step-seed-held-resource-in-namespace) | 0 | 2 | 0 | 0 | 0 |
| 4 | [delete-projects](#step-delete-projects) | 0 | 1 | 0 | 0 | 0 |
| 5 | [verify-projects-wait-for-their-resources](#step-verify-projects-wait-for-their-resources) | 0 | 3 | 0 | 0 | 0 |
| 6 | [verify-held-resource-in-default-survives](#step-verify-held-resource-in-default-survives) | 0 | 1 | 0 | 0 | 0 |
| 7 | [verify-namespace-outlives-its-contents](#step-verify-namespace-outlives-its-contents) | 0 | 2 | 0 | 0 | 0 |
| 8 | [release-finalizers](#step-release-finalizers) | 0 | 1 | 0 | 0 | 0 |
| 9 | [verify-projects-delete-promptly](#step-verify-projects-delete-promptly) | 0 | 2 | 0 | 0 | 0 |
| 10 | [cleanup-organization](#step-cleanup-organization) | 0 | 1 | 0 | 0 | 0 |

### Step: `create-projects`

Create the organization and both projects, and wait for them to be ready

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |

### Step: `seed-held-resource-in-default`

Create a finalizer-held resource in the project's default namespace

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

### Step: `seed-held-resource-in-namespace`

Create a project namespace holding a finalizer-held resource

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `delete-projects`

Delete both projects without waiting. Neither can finish while its
resources are held, so the delete call must not block the test.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `verify-projects-wait-for-their-resources`

Give cleanup time to run, then verify neither project has been removed,
and that each names what is holding it.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `sleep` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `assert` | 0 | 0 | *No description* |

### Step: `verify-held-resource-in-default-survives`

The held resource is still there, in a control plane that still answers

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `assert` | 0 | 0 | *No description* |

### Step: `verify-namespace-outlives-its-contents`

The namespace is terminating, but must not be removed while the
resource inside it still exists.


#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `assert` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `release-finalizers`

Release the consumer finalizers, as a working consumer would

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `verify-projects-delete-promptly`

With nothing left holding them, both projects finish deleting

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `wait` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `cleanup-organization`

Remove the organization the test created

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |

---

