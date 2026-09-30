# Test: `policybinding-org-restrictions`

Tests the PolicyBinding validating webhook's organization-context
restrictions.

An organization holder can reach PolicyBindings only through the
organization control plane, which injects the org's parent context into the
request user. In that context the webhook enforces:

- The target must be a Project that belongs to the request organization,
  referenced as a resourceRef. Kind-level (resourceKind) targets are
  rejected.
- The binding must live in the organization's namespace.
- Subjects are unrestricted: an org holder may grant a role to a User,
  Group, or ServiceAccount. What keeps those grants inside the organization
  is the namespace and target containment above, not a subject-kind
  restriction. A ServiceAccount also need not live in the target Project's
  control plane — an organization admin can grant a ServiceAccount access
  outside of its own project (for example, IAM admin access across the
  whole org).

This test verifies:
- A ServiceAccount bound to a Project within the same organization and
  control plane is accepted.
- A ServiceAccount that lives in a different project may be granted a role
  targeting a Project in the organization.
- User and system-group subjects are accepted.
- Binding a Project that belongs to another organization is denied.
- Kind-level (resourceKind) targets are denied.
- A binding created in a namespace other than the organization's namespace
  is denied.

The PolicyBinding steps run as test-user (system:authenticated), who is
granted RBAC to create PolicyBindings in each organization namespace by the
setup. They cannot run as the admin user: the validating webhook is bypassed
for system:masters (so the controller manager and other system components
can create bindings through an org control plane, matching the organization
and user webhooks), which would let every denied binding through.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organizations](#step-setup-organizations) | 0 | 4 | 0 | 0 | 0 |
| 2 | [grant-policybinding-rbac](#step-grant-policybinding-rbac) | 0 | 1 | 0 | 0 | 0 |
| 3 | [create-user](#step-create-user) | 0 | 2 | 0 | 0 | 0 |
| 4 | [create-project-a](#step-create-project-a) | 0 | 2 | 0 | 0 | 0 |
| 5 | [create-project-b](#step-create-project-b) | 0 | 2 | 0 | 0 | 0 |
| 6 | [create-service-account-a](#step-create-service-account-a) | 0 | 2 | 0 | 0 | 0 |
| 7 | [create-service-account-b](#step-create-service-account-b) | 0 | 2 | 0 | 0 | 0 |
| 8 | [allow-serviceaccount-bound-to-in-project](#step-allow-serviceaccount-bound-to-in-project) | 0 | 2 | 0 | 0 | 0 |
| 9 | [deny-project-from-another-organization](#step-deny-project-from-another-organization) | 0 | 1 | 0 | 0 | 0 |
| 10 | [allow-serviceaccount-from-another-project](#step-allow-serviceaccount-from-another-project) | 0 | 2 | 0 | 0 | 0 |
| 11 | [deny-resourcekind-target](#step-deny-resourcekind-target) | 0 | 1 | 0 | 0 | 0 |
| 12 | [allow-user-subject](#step-allow-user-subject) | 0 | 2 | 0 | 0 | 0 |
| 13 | [allow-system-group-subject](#step-allow-system-group-subject) | 0 | 2 | 0 | 0 | 0 |
| 14 | [deny-binding-outside-org-namespace](#step-deny-binding-outside-org-namespace) | 0 | 1 | 0 | 0 | 0 |

### Step: `setup-organizations`

Create the two test organizations and wait for their namespaces

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |
| 4 | `wait` | 0 | 0 | *No description* |

### Step: `grant-policybinding-rbac`

Authorize test-user to create PolicyBindings in both org namespaces

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

### Step: `create-user`

Create the User used by the denied User-subject binding

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `create-project-a`

Create the Project that belongs to Organization A and wait for its control plane

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `create-project-b`

Create the Project that belongs to Organization B and wait for its control plane

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `create-service-account-a`

Create a ServiceAccount in Project A's control plane

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `create-service-account-b`

Create a ServiceAccount in Project B's control plane

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `allow-serviceaccount-bound-to-in-project`

A ServiceAccount bound to a Project in its own organization and control plane is accepted

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `deny-project-from-another-organization`

Binding a Project owned by another organization is denied

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `allow-serviceaccount-from-another-project`

A ServiceAccount that lives in a different project may be granted a role targeting a Project in the organization

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `deny-resourcekind-target`

A kind-level (resourceKind) target is denied in organization context

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `allow-user-subject`

A User subject binding targeting a Project in the organization is accepted

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `allow-system-group-subject`

A system group subject binding targeting a Project in the organization is accepted

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `deny-binding-outside-org-namespace`

A binding created outside the organization's namespace is denied

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

---

