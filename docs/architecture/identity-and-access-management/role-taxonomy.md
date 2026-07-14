# IAM Role Taxonomy

IAM roles are classified with annotations and labels under the `taxonomy.miloapis.com` prefix to support UI grouping, filtering, and display ordering.

## Labels

Labels are used for **filtering and selection** (e.g., `filterByLabel` in the cloud portal).

### `taxonomy.miloapis.com/role-category`

Classifies what kind of concern the role governs.

| Value | Description | Examples |
|---|---|---|
| `platform` | Cross-cutting platform concerns present in every deployment — IAM, resource management, org/project hierarchy, quota, and core primitives. | `iam-admin`, `resourcemanager-admin`, `quota-admin`, `owner` |
| `service` | Infrastructure or data-plane services that teams deploy and operate independently. | `dns-admin`, `network-admin`, `activity-admin`, `search-admin` |
| `feature` | Product capabilities that end-users interact with directly. | `crm-note-admin`, `notification-contact-admin` |

**Every role file must include this label:**

```yaml
metadata:
  labels:
    taxonomy.miloapis.com/role-category: service   # platform | service | feature
```

## Annotations

Annotations are used for **display metadata** (grouping headers, sort order, human-readable names).

### `taxonomy.miloapis.com/product`

The product group name shown as a header in the UI role picker.

```yaml
annotations:
  taxonomy.miloapis.com/product: "DNS"
```

### `taxonomy.miloapis.com/sort-order`

Controls the ordering of roles within a product group. Use multiples of 10.

| Sort order | Conventional meaning |
|---|---|
| `"10"` | Admin / full access |
| `"20"` | Editor / manager / operator |
| `"30"` | Viewer / reader |
| `"40"` | Scoped self-service roles |

```yaml
annotations:
  taxonomy.miloapis.com/sort-order: "10"
```

## Full example

```yaml
apiVersion: iam.miloapis.com/v1alpha1
kind: Role
metadata:
  name: dns-admin
  labels:
    taxonomy.miloapis.com/role-category: service
  annotations:
    kubernetes.io/display-name: DNS Admin
    kubernetes.io/description: Full administrative access to DNS zones and records.
    taxonomy.miloapis.com/product: DNS
    taxonomy.miloapis.com/sort-order: "10"
spec:
  launchStage: Beta
  inheritedRoles:
    - name: dns-editor
```
