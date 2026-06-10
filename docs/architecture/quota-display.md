# Quota Display Metadata

How quota resources expose human-readable names and group membership to UIs
(the cloud-portal Quotas page). This is a thin, forward-compatible convention
that anticipates [milo-os/service-catalog](https://github.com/milo-os/service-catalog);
when the catalog ships, the central resolution below is replaced by
`Service.spec.displayName` with no change to what services author here.

## What each service authors (on its ResourceRegistration)

| Key | Kind | Purpose |
|---|---|---|
| `kubernetes.io/display-name` | annotation | Friendly row name, e.g. "HTTP Proxies" |
| `kubernetes.io/description` | annotation | Row subtitle/tooltip |
| `services.miloapis.com/owner` | label | The owning service's canonical name (a reference, not a display name), e.g. `networking.datumapis.com` |

The `owner` value is the service's reverse-DNS canonical name — the same value
that will become `Service.spec.serviceName` in the service-catalog. It is a
**reference**: services never declare their product's display name here, only
which service they belong to.

## How the group display name is resolved (central)

The owning service's **display name** ("Networking", "DNS") is resolved
centrally — never re-declared per registration. Until the service-catalog is
deployed, the cloud-portal carries an interim `serviceName → displayName` map
(plus a `resourceType → serviceName` bridge for registrations that have not yet
adopted the `owner` label). When the catalog ships, both are replaced by
`Service` lookups via graphql-gateway.

## Example

```yaml
apiVersion: quota.miloapis.com/v1alpha1
kind: ResourceRegistration
metadata:
  name: httpproxies-per-project
  labels:
    app.kubernetes.io/name: network-services-operator
    app.kubernetes.io/component: quota-system
    services.miloapis.com/owner: networking.datumapis.com
  annotations:
    kubernetes.io/display-name: "HTTP Proxies"
    kubernetes.io/description: "Maximum number of HTTP proxies per project"
spec:
  # … unchanged …
```
