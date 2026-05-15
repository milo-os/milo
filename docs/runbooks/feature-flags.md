# Feature Flags

Diagnostic runbook for feature flags backed by `ResourceRegistration` /
`ResourceGrant` / `AllowanceBucket`. Three failure modes are covered:

1. [Flag not taking effect](#1-flag-not-taking-effect)
2. [Wrong org granted](#2-wrong-org-granted)
3. [OpenFeature provider erroring](#3-openfeature-provider-erroring)

All queries below target the **milo core control plane** (`KUBECONFIG`
pointed at the milo apiserver). The org namespace is always
`organization-<orgname>`. Flag name (e.g. `feature-ai-edge-dns`) is the
`ResourceRegistration` name; the OpenFeature key strips the `feature-`
prefix.

## 1. Flag not taking effect

Reported as: "We toggled this on in the staff portal but the feature is still
off in the product."

### 1.1 Confirm the registration exists

```sh
kubectl get resourceregistration <flag-name> -o yaml
```

If this 404s, the flag was never registered on this environment — the
service team owns deploying the `ResourceRegistration` (see the [developer
guide](../providers/openfeature.md)).

Check `spec.type == Feature` and that the
`app.kubernetes.io/component=feature-flags` label is present. Without that
label the staff portal won't list it, so the staff user may have toggled a
*different* flag than they thought.

### 1.2 Confirm the grant exists for the org

```sh
kubectl -n organization-<orgname> get resourcegrant \
  -l quota.miloapis.com/resource-type=quota.miloapis.com/<flag-name> \
  -o yaml
```

No grant = nothing was toggled, or the toggle was reverted. Check
`metadata.annotations["staff-portal.miloapis.com/enabled-by"]` on the grant
to confirm who created it.

### 1.3 Confirm the bucket reconciled

The grant must be reconciled into an `AllowanceBucket` with `available > 0`
before any provider will return `true`.

```sh
kubectl -n organization-<orgname> get allowancebucket \
  --field-selector spec.resourceType=quota.miloapis.com/<flag-name> \
  -o yaml
```

If the bucket is missing, check the quota controller:

```sh
kubectl -n milo-system logs deploy/milo-quota-controller --tail=200 \
  | grep -i <flag-name>
```

Common causes of a missing bucket: the org namespace doesn't exist (org not
fully provisioned), or the quota controller is wedged on an unrelated
resource. The latter shows as repeated reconcile errors in the controller
log.

If the bucket exists but `available == 0`, the grant amount is zero or has
expired. Re-inspect the grant from §1.2.

### 1.4 Confirm the caller reads the bucket

If grant + bucket both look right, the issue is on the calling service:

- Restart the caller pods to force the OpenFeature provider cache to
  refresh. Steady-state evaluation is in-memory, so a stale cache from
  before the grant was created will return `false` until the informer picks
  up the new bucket.
- Verify the caller is passing `organization=<orgname>` in the
  OpenFeature evaluation context. Missing context → provider returns the
  default (`false`).

## 2. Wrong org granted

Reported as: "We meant to enable this for Acme but enabled it for Acme Inc by
mistake."

### 2.1 Find the offending grant

```sh
kubectl get resourcegrant -A \
  -l quota.miloapis.com/resource-type=quota.miloapis.com/<flag-name>
```

This lists every org with the flag enabled, with namespace =
`organization-<orgname>`. Confirm with the staff user which one was
unintended.

### 2.2 Delete it

```sh
kubectl -n organization-<wrong-org> delete resourcegrant <grant-name>
```

The `AllowanceBucket` will reconcile down to `available == 0` within
seconds, and the next OpenFeature evaluation will return the default.

### 2.3 Audit trail

The original create and the corrective delete are both in the milo
apiserver audit log. Pull them by `objectRef.resource=resourcegrants` and
`objectRef.name=<grant-name>` — this is the artifact for incident write-up
or customer comms.

## 3. OpenFeature provider erroring

Reported as: "Provider logs are full of errors" or "Every flag is evaluating
to its default even where I know a grant exists."

Only the **TypeScript** provider exists today (used by cloud portal and staff
portal). A Go provider is planned but not yet built; server-side Go callers
query `AllowanceBucket` directly. The steps below cover both.

### 3.1 Common causes

- **RBAC.** The caller lacks `list` (and, for the TS provider, `watch`) on
  `allowancebuckets.quota.miloapis.com` for the org namespace. For
  service-account callers verify with:

  ```sh
  kubectl auth can-i list allowancebuckets.quota.miloapis.com \
    --as=system:serviceaccount:<ns>:<sa> \
    -n organization-<orgname>
  ```

  Browser callers go through the staff/cloud portal proxy, which forwards
  the user's OIDC identity — for those, check that the user has the
  expected role on the org.

- **Network/DNS.** The caller can't reach the milo apiserver (or the portal
  proxy can't). Check the caller's egress and DNS — unrelated to flags,
  but flag evaluation fails first because it runs on every request.

- **Missing evaluation context.** The provider returns the default whenever
  `organization` is missing from the OpenFeature evaluation context. Grep
  the caller for `OpenFeature.setContext` / `setProviderAndWait` and
  confirm the org is being threaded in.

- **Schema skew.** The caller pinned an old provider/SDK version that
  doesn't understand a current `AllowanceBucket` field. Bump the dep.

### 3.2 Confirm the bucket itself looks healthy

If grant + bucket pass §1's checks, watch the bucket while the caller
retries:

```sh
kubectl -n organization-<orgname> get allowancebucket \
  --field-selector spec.resourceType=quota.miloapis.com/<flag-name> \
  -w
```

If the bucket flaps (created/deleted repeatedly), a controller upstream is
fighting itself — escalate to the quota system owner. If the bucket is
stable but the caller still defaults, the caller's local cache is wedged;
reload the page (TS provider) or restart the pod (direct Go callers).

## Quick reference

| Symptom | First check |
|---|---|
| Staff portal page is empty | Registrations exist but lack `app.kubernetes.io/component=feature-flags` label |
| Toggle succeeds, feature still off | `AllowanceBucket` exists and `available > 0`? (§1.3) |
| Toggle succeeds, bucket missing | Quota controller logs (§1.3) |
| Feature on for wrong org | Grant `kubectl delete` (§2.2) |
| Provider logs show errors | Cold-start vs steady-state (§3.1) |
| `kubectl auth can-i` returns no | Service account RBAC (§3.2) |
