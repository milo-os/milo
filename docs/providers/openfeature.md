# OpenFeature Provider

Feature flags in Datum are not a separate system. A flag is a
`ResourceRegistration` of `type: Feature`, an "on" state is a `ResourceGrant`
targeting an organization, and "is it on?" is `AllowanceBucket.status.available
> 0`. This document shows service developers how to (1) register a flag and (2)
gate code on it via the OpenFeature SDK.

## Registering a flag

A flag is a cluster-scoped `ResourceRegistration` with `spec.type: Feature` and
no claiming resources. The `metadata.name` becomes the flag's API resource
name; the human-readable name and description are conveyed via annotations and
labels so the staff portal can list and group them.

```yaml
apiVersion: quota.miloapis.com/v1alpha1
kind: ResourceRegistration
metadata:
  name: feature-ai-edge-dns
  labels:
    app.kubernetes.io/component: feature-flags
  annotations:
    kubernetes.io/display-name: AI Edge DNS (private beta)
    kubernetes.io/description: >-
      Routes DNS requests through the AI Edge proxy instead of the legacy
      resolver path.
spec:
  type: Feature
  claimingResources: []
```

Ship the manifest as part of your service's Milo-control-plane kustomization
(see `apps/<your-service>/components/feature-flags/` in the infra repo for
examples). Once reconciled, the flag appears in the staff portal's
**Organization → Quota → Feature Flags** view, where staff users can toggle it
on or off per org.

The `app.kubernetes.io/component: feature-flags` label is mandatory — the
staff portal lists registrations by that label selector.

## Gating code

Both the Go and TypeScript OpenFeature providers evaluate a flag by querying
`AllowanceBucket` for the caller's organization with a field selector on
`spec.resourceType`. Steady-state evaluation is in-memory (an informer keeps
the bucket cache warm); the provider performs a single `LIST` on cold start.
On miss, on error, or on any provider degradation, the provider returns the
caller-supplied default — flags are **closed by default**.

### Go

```go
import (
    "github.com/open-feature/go-sdk/openfeature"
    miloprovider "go.miloapis.com/milo/providers/openfeature"
)

openfeature.SetProvider(miloprovider.New(miloprovider.Config{
    KubeConfig: cfg,
}))

client := openfeature.NewClient("ai-edge-dns")

evalCtx := openfeature.NewEvaluationContext("", map[string]any{
    "organization": orgName,
})

if client.BooleanValue(ctx, "ai-edge-dns", false, evalCtx) {
    return enableEdgeDNSPath(req)
}
return legacyDNSPath(req)
```

### TypeScript

```ts
import { OpenFeature } from "@openfeature/web-sdk";
import { MiloProvider } from "@datum-cloud/openfeature-provider";

await OpenFeature.setProviderAndWait(new MiloProvider({ baseURL }));
const client = OpenFeature.getClient();

OpenFeature.setContext({ organization: orgName });

if (await client.getBooleanValue("ai-edge-dns", false)) {
  showEdgeDNSPanel();
}
```

The flag key (`"ai-edge-dns"`) is the `ResourceRegistration` name with the
`feature-` prefix stripped. The evaluation context **must** include
`organization`; without it the provider returns the default.

## Evaluation contract

| Outcome | When |
|---|---|
| `true` | An `AllowanceBucket` exists for `(organization, resourceType)` and `status.available > 0` |
| `false` (default) | No bucket, `available == 0`, evaluation context missing `organization`, or any provider error |

A flag is never `null`/`unknown` — providers always resolve to the
caller-supplied default on any failure. This is intentional: flag-system
degradation must not fail the data path.

## Non-goals

- Per-user, per-project, or percentage rollouts. Org-scoped boolean only.
- Non-boolean flag values.
- A/B or experimentation framework.

For toggling flags as a staff user, see the [operator
guide](../../../staff-portal/docs/operators/feature-flags.md). For diagnosing a
flag that isn't taking effect, see the [feature-flags
runbook](../runbooks/feature-flags.md).
