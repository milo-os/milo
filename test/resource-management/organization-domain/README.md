# Test: `organization-domain`

Tests OrganizationDomain end to end: an organization proving it controls a
domain without adding it to one of its projects, and email domain joining
accepting a domain verified that way.

The domain-manager steps run as test-user through the organization's
control plane, so the webhook sees the organization's parent context the
way an owner's request would. Setup and verification run as the admin.

This test verifies:
- The webhook rejects public email providers, public suffixes, and an
  OrganizationDomain that isn't named after its domain.
- The controller publishes a TXT record to add and reports that the domain
  isn't verified yet.
- spec.domainName can't be changed.
- Email domain joining rejects the domain while it's unverified.
- Once the domain is verified, email domain joining accepts it and offers
  membership to a user at the domain, but not to a user elsewhere.
- Removing the domain pauses joining and withdraws the pending offer.

CI can't publish a real DNS record, so the admin marks the domain verified
through the status subresource. The controller never checks a verified
domain again, so it keeps that status. Unit tests cover finding the record.


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [setup-organization](#step-setup-organization) | 0 | 3 | 0 | 0 | 0 |
| 2 | [reject-domains-an-organization-cannot-own](#step-reject-domains-an-organization-cannot-own) | 0 | 3 | 0 | 0 | 0 |
| 3 | [add-domain-and-publish-record](#step-add-domain-and-publish-record) | 0 | 2 | 0 | 0 | 0 |
| 4 | [domain-name-is-immutable](#step-domain-name-is-immutable) | 0 | 1 | 0 | 0 | 0 |
| 5 | [joining-rejects-unverified-domain](#step-joining-rejects-unverified-domain) | 0 | 1 | 0 | 0 | 0 |
| 6 | [mark-domain-verified](#step-mark-domain-verified) | 0 | 2 | 0 | 0 | 0 |
| 7 | [joining-accepts-verified-domain](#step-joining-accepts-verified-domain) | 0 | 2 | 0 | 0 | 0 |
| 8 | [offer-membership-at-the-domain](#step-offer-membership-at-the-domain) | 0 | 3 | 0 | 0 | 0 |
| 9 | [remove-domain-withdraws-offer](#step-remove-domain-withdraws-offer) | 0 | 3 | 0 | 0 | 0 |

### Step: `setup-organization`

Create the organization, wait for its namespace, and let test-user manage its domains

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `apply` | 0 | 0 | *No description* |

### Step: `reject-domains-an-organization-cannot-own`

The webhook rejects a public email provider, a public suffix, and a name that isn't the domain

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |
| 2 | `create` | 0 | 0 | *No description* |
| 3 | `create` | 0 | 0 | *No description* |

### Step: `add-domain-and-publish-record`

Adding a domain publishes the TXT record to add, and the domain stays unverified until it exists

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `create` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `domain-name-is-immutable`

Changing spec.domainName is rejected

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `patch` | 0 | 0 | *No description* |

### Step: `joining-rejects-unverified-domain`

Email domain joining rejects a domain the organization hasn't verified

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |

### Step: `mark-domain-verified`

Stand in for DNS by marking the domain verified through the status subresource

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `joining-accepts-verified-domain`

Email domain joining accepts the verified domain and reports it as verified

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |

### Step: `offer-membership-at-the-domain`

A user at the domain gets an offer; a user elsewhere doesn't

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `error` | 0 | 0 | *No description* |

### Step: `remove-domain-withdraws-offer`

Removing the domain pauses joining and withdraws the pending offer

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |
| 2 | `assert` | 0 | 0 | *No description* |
| 3 | `error` | 0 | 0 | *No description* |

---

