# Test: `discovery-context-filter`

End-to-end test for parent-context-aware API discovery.

Verifies that the discovery endpoint response is filtered per parent
context (Platform vs Organization), driven by either the
`discovery.miloapis.com/parent-contexts` CRD annotation, **or dynamically via**
`DiscoveryContextPolicy` resources. This test exercises both the bundled-CRD path
(organizations, projects, organizationmemberships) and the third-party-CRD path
(a synthetic DiscoveryWidget CRD installed mid-test), as well as the new runtime
override behavior via `DiscoveryContextPolicy`.

The discovery filter is URL-driven — it consults only the request path
prefix — so this test does not need a real Organization to exist. The
kubeconfig points at `.../organizations/demo-discovery-filter-org/control-plane`
and the filter honours that alone.

**Discovery context filtering precedence:**
  1. If a `DiscoveryContextPolicy` exists that matches a resource, it *overrides* any static CRD annotation or static registration. Policies can be added or deleted at runtime and have immediate effect.
  2. If no policy matches, static CRD annotation (`discovery.miloapis.com/parent-contexts`) is used.
  3. Resources with neither a policy nor an annotation are treated as visible in all contexts.

## Expectations (CRD annotation only)
  At ROOT:
    - organizations            present (tagged Platform)
    - projects                 hidden (tagged Organization)
    - organizationmemberships  hidden (tagged Organization,User)
    - discoverywidgets         hidden (tagged Organization)

  At ORGANIZATION:
    - organizations            hidden
    - projects                 present
    - organizationmemberships  present
    - discoverywidgets         present

  At USER:
    - organizations            hidden
    - projects                 hidden
    - organizationmemberships  present (multi-context)
    - discoverywidgets         hidden

## Dynamic Policy Override Coverage

This test covers and verifies the following behaviors for `DiscoveryContextPolicy`:

- Applying a `DiscoveryContextPolicy` that sets or overrides the context visibility for a resource (e.g., changes `discoverywidgets` from Organization-only (CRD annotation) to Platform-only) updates discovery endpoints in all clusters/contexts at runtime.
- With such a policy active, discoverywidgets are **not** returned in Organization context (org), but **are** in Platform context (root).
- Deleting the policy causes discovery filtering to revert immediately to the original CRD annotation; resources return to their original visibility (e.g., discoverywidgets reappears in Organization context, and is hidden in Platform).

## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [install-external-crd](#step-install-external-crd) | 0 | 3 | 0 | 0 | 0 |
| 2 | [verify-platform-context-discovery](#step-verify-platform-context-discovery) | 0 | 1 | 0 | 0 | 0 |
| 3 | [verify-org-context-discovery](#step-verify-org-context-discovery) | 0 | 1 | 0 | 0 | 0 |
| 4 | [verify-user-context-discovery](#step-verify-user-context-discovery) | 0 | 1 | 0 | 0 | 0 |
| 5 | [apply-policy-override](#step-apply-policy-override) | 0 | 2 | 0 | 0 | 0 |
| 6 | [verify-policy-overrides-crd-annotation](#step-verify-policy-overrides-crd-annotation) | 0 | 1 | 0 | 0 | 0 |
| 7 | [verify-policy-shows-in-platform-context](#step-verify-policy-shows-in-platform-context) | 0 | 1 | 0 | 0 | 0 |
| 8 | [delete-policy-override](#step-delete-policy-override) | 0 | 2 | 0 | 0 | 0 |
| 9 | [verify-reverts-to-crd-annotation](#step-verify-reverts-to-crd-annotation) | 0 | 1 | 0 | 0 | 0 |
|10 | [cleanup-external-crd](#step-cleanup-external-crd) | 0 | 1 | 0 | 0 | 0 |

### Step: `install-external-crd`

Install a third-party CRD tagged for the Organization context.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | *No description* |
| 2 | `wait` | 0 | 0 | *No description* |
| 3 | `sleep` | 0 | 0 | *No description* |

### Step: `verify-platform-context-discovery`

Platform context is unfiltered — controllers and admin tools must see all resources.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `verify-org-context-discovery`

Organization context should expose Organization-tagged resources and hide Platform-tagged ones.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `verify-user-context-discovery`

User context should expose multi-context resources (organizationmemberships) and hide the rest.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `apply-policy-override`

Applies a `DiscoveryContextPolicy` that overrides the CRD annotation for `discoverywidgets`, making it visible only in Platform context.

#### Try
| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `apply` | 0 | 0 | Apply the override policy |
| 2 | `sleep` | 0 | 0 | Wait for policy informer to sync |

### Step: `verify-policy-overrides-crd-annotation`

Verifies that, with the policy active, `discoverywidgets` is **not** visible in Organization context (it has become Platform-only).

#### Try
| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `verify-policy-shows-in-platform-context`

Verifies that, with the policy active, `discoverywidgets` **is** visible in Platform context (root).

#### Try
| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `delete-policy-override`

Deletes the `DiscoveryContextPolicy`, so that the system reverts to static CRD annotation-based visibility.

#### Try
| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | Delete the policy |
| 2 | `sleep` | 0 | 0 | Wait for informer to process deletion |

### Step: `verify-reverts-to-crd-annotation`

Verifies that, after the policy is deleted, `discoverywidgets` is visible again in Organization context, per its CRD annotation.

#### Try
| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `script` | 0 | 0 | *No description* |

### Step: `cleanup-external-crd`

Remove the external CRD so the registry forgets the entry.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |

---

