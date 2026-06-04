# Test: `clusternote-multicluster-subject`

Tests that ClusterNotes can reference cluster-scoped subjects (Namespaces)
in project control planes.

Validates:
- ClusterNote in project control plane can reference cluster-scoped Namespace
- Owner reference is correctly set on the ClusterNote
- ClusterNote is garbage collected when Namespace is deleted


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 2 | 0 | 0 | 0 |
| 2 | [create-project](#step-create-project) | 0 | 2 | 0 | 0 | 0 |
| 3 | [create-namespace-in-project](#step-create-namespace-in-project) | 0 | 2 | 0 | 0 | 0 |
| 4 | [create-clusternote-referencing-namespace](#step-create-clusternote-referencing-namespace) | 0 | 2 | 0 | 0 | 0 |
| 5 | [delete-namespace-verify-clusternote-deletion](#step-delete-namespace-verify-clusternote-deletion) | 0 | 2 | 0 | 0 | 0 |

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

### Step: `create-namespace-in-project`

Create cluster-scoped Namespace in project control plane

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `create-clusternote-referencing-namespace`

Create ClusterNote referencing the Namespace in project control plane

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `delete-namespace-verify-clusternote-deletion`

Delete Namespace and verify ClusterNote is garbage collected

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

---

