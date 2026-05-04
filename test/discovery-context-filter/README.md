# Test: `discovery-context-filter`

End-to-end test for parent-context-aware API discovery.

Verifies that the discovery endpoint response is filtered per parent
context (Platform vs Organization), driven by the
`discovery.miloapis.com/parent-contexts` CRD annotation. Exercises both
the bundled-CRD path (organizations, projects, organizationmemberships)
and the third-party-CRD path (a synthetic DiscoveryWidget CRD installed
mid-test).

The discovery filter is URL-driven — it consults only the request path
prefix — so this test does not need a real Organization to exist. The
kubeconfig points at `.../organizations/demo-discovery-filter-org/control-plane`
and the filter honours that alone.

Expectations:
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


## Steps

| # | Name | Bindings | Try | Catch | Finally | Cleanup |
|:-:|---|:-:|:-:|:-:|:-:|:-:|
| 1 | [install-external-crd](#step-install-external-crd) | 0 | 3 | 0 | 0 | 0 |
| 2 | [verify-platform-context-discovery](#step-verify-platform-context-discovery) | 0 | 1 | 0 | 0 | 0 |
| 3 | [verify-org-context-discovery](#step-verify-org-context-discovery) | 0 | 1 | 0 | 0 | 0 |
| 4 | [verify-user-context-discovery](#step-verify-user-context-discovery) | 0 | 1 | 0 | 0 | 0 |
| 5 | [cleanup-external-crd](#step-cleanup-external-crd) | 0 | 1 | 0 | 0 | 0 |

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

### Step: `cleanup-external-crd`

Remove the external CRD so the registry forgets the entry.

#### Try

| # | Operation | Bindings | Outputs | Description |
|:-:|---|:-:|:-:|---|
| 1 | `delete` | 0 | 0 | *No description* |

---

