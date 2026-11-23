# Test: `organizationmembership-org-context`

Tests the OrganizationMembership validating webhook's organization-context
restriction.

An organization holder can reach OrganizationMemberships only through the
organization control plane, which injects the org's parent context into the
request user. This closes an escalation where an org admin holds update
rights on memberships (granted so they can manage roles) and could
otherwise repoint an existing membership's organizationRef at another
organization, or create a new membership in their org's namespace that
references another organization.

In organization context the webhook enforces:
- The membership must live in the organization's namespace.
- spec.organizationRef.name must be the request organization itself, on
  creation and update.
- A membership cannot be created directly; members join by accepting a
  UserInvitation, whose controller creates the membership outside
  organization scope. The refusal reads the same whether or not the
  referenced User exists.
- spec.userRef.name cannot be changed on update.

Requests that carry no organization parent context (platform scope,
internal controllers) are unaffected; requests made by system:masters (the
platform superuser) bypass the restriction entirely, matching the
organization, user, and policybinding webhooks.

This test verifies (the org-scope steps run as test-user,
system:authenticated, who is granted RBAC to create and update memberships
in the organization namespace by the setup — not the admin user, since the
webhook is bypassed for system:masters):
- Creating a membership referencing the request organization is denied,
  with the same error for an existing User and a missing one.
- Creating a membership referencing another organization is denied.
- Updating a membership while keeping organizationRef is accepted.
- Updating a membership to repoint organizationRef at another organization
  is denied.
- Updating a membership to reference a different User is denied.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 2 | 0 | 0 | 0 |
| 2 | [grant-membership-rbac](#step-grant-membership-rbac) | 0 | 1 | 0 | 0 | 0 |
| 3 | [create-user](#step-create-user) | 0 | 3 | 0 | 0 | 0 |
| 4 | [seed-membership](#step-seed-membership) | 0 | 2 | 0 | 0 | 0 |
| 5 | [deny-create-existing-user](#step-deny-create-existing-user) | 0 | 1 | 0 | 0 | 0 |
| 6 | [deny-create-missing-user](#step-deny-create-missing-user) | 0 | 1 | 0 | 0 | 0 |
| 7 | [deny-create-cross-org](#step-deny-create-cross-org) | 0 | 1 | 0 | 0 | 0 |
| 8 | [allow-update-keeps-orgref](#step-allow-update-keeps-orgref) | 0 | 2 | 0 | 0 | 0 |
| 9 | [deny-update-repoints-orgref](#step-deny-update-repoints-orgref) | 0 | 1 | 0 | 0 | 0 |
| 10 | [deny-update-changes-userref](#step-deny-update-changes-userref) | 0 | 1 | 0 | 0 | 0 |

### Step: `setup-organization`

Create the test organization and wait for its namespace

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |

### Step: `grant-membership-rbac`

Authorize test-user to create and update memberships in the org namespace

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

### Step: `create-user`

Create the Users referenced by the test memberships and wait for them to be ready

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `apply` | 0 | 0 | *No description* |
| 3 | `wait` | 0 | 0 | *No description* |

### Step: `seed-membership`

A platform-scope caller creates the membership the update steps edit

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `deny-create-existing-user`

Creating a membership for an existing User in organization scope is denied

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `deny-create-missing-user`

Creating a membership for a missing User is denied with the same error as an existing one

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `deny-create-cross-org`

A membership referencing another organization is denied at admission

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |

### Step: `allow-update-keeps-orgref`

An update that keeps organizationRef is accepted

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `deny-update-repoints-orgref`

An update that repoints organizationRef to another organization is denied

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

### Step: `deny-update-changes-userref`

An update that points the membership at a different User is denied

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

---

