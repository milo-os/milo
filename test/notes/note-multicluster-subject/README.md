# Test: `note-multicluster-subject`

Tests that Notes can reference subjects (ConfigMaps) in project control planes.

Validates:
- Note created in project control plane can reference ConfigMap in same control plane
- Owner reference is correctly set on the Note
- Note is garbage collected when ConfigMap is deleted


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 2 | 0 | 0 | 0 |
| 2 | [create-project](#step-create-project) | 0 | 2 | 0 | 0 | 0 |
| 3 | [create-configmap-in-project](#step-create-configmap-in-project) | 0 | 2 | 0 | 0 | 0 |
| 4 | [create-note-referencing-configmap](#step-create-note-referencing-configmap) | 0 | 2 | 0 | 0 | 0 |
| 5 | [delete-configmap-verify-note-deletion](#step-delete-configmap-verify-note-deletion) | 0 | 2 | 0 | 0 | 0 |

### Step: `setup-organization`

Create test organization

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `create-project`

Create project in org control plane

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `create-configmap-in-project`

Create ConfigMap resource in project control plane

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `create-note-referencing-configmap`

Create Note referencing the ConfigMap in project control plane

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `delete-configmap-verify-note-deletion`

Delete ConfigMap and verify Note is garbage collected

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

---

