# Activity Events Proxy

Milo proxies Kubernetes Events to the Activity API server for ClickHouse-backed storage with longer retention and richer querying than etcd.

## Configuration

Enable the feature gate:

```bash
--feature-gates=EventsProxy=true
```

Configure the Activity API server connection:

| Flag | Description | Default |
|------|-------------|---------|
| `--events-provider-url` | Activity API server URL | Required |
| `--events-provider-ca-file` | TLS CA certificate | Required |
| `--events-provider-client-cert` | mTLS client certificate | Required |
| `--events-provider-client-key` | mTLS client key | Required |
| `--events-provider-timeout` | Request timeout in seconds | `30` |
| `--events-provider-retries` | Retry attempts for transient errors | `3` |
| `--events-forward-extras` | User.Extra keys to forward | `iam.miloapis.com/parent-type`, `iam.miloapis.com/parent-name`, `iam.miloapis.com/parent-api-group` |

Example:

```bash
milo apiserver \
  --feature-gates=EventsProxy=true \
  --events-provider-url=https://activity-apiserver.activity-system.svc:443 \
  --events-provider-ca-file=/etc/milo/activity/ca.crt \
  --events-provider-client-cert=/etc/milo/activity/tls.crt \
  --events-provider-client-key=/etc/milo/activity/tls.key
```

## Supported Operations

Both `core/v1.Event` and `events.k8s.io/v1.Event` APIs are proxied:

| Operation | Method | Endpoint |
|-----------|--------|----------|
| Create | POST | `/api/v1/namespaces/{ns}/events` |
| Get | GET | `/api/v1/namespaces/{ns}/events/{name}` |
| List | GET | `/api/v1/namespaces/{ns}/events` |
| Update | PUT | `/api/v1/namespaces/{ns}/events/{name}` |
| Delete | DELETE | `/api/v1/namespaces/{ns}/events/{name}` |
| Watch | GET | `/api/v1/namespaces/{ns}/events?watch=true` |

Field selectors work as expected:

```bash
kubectl get events --field-selector involvedObject.name=my-pod
kubectl get events --field-selector type=Warning
```

## Architecture

```
┌────────────────────────────────────────────────────────┐
│                    Milo API Server                     │
│                                                         │
│  Authentication → Authorization → EventsREST           │
│                                        │                │
│                                        ▼                │
│                               DynamicProvider           │
│                         (injects X-Remote-* headers)    │
└────────────────────────────────┬───────────────────────┘
                                 │ HTTPS + mTLS
                                 ▼
┌────────────────────────────────────────────────────────┐
│              Activity API Server                        │
│                                                         │
│  activity.miloapis.com/v1alpha1/namespaces/{ns}/events │
└────────────────────────────────────────────────────────┘
```

### Components

**CoreProviderWrapper** (`storageprovider.go`): Wraps the core API provider to inject events proxy storage. Only one provider can register `core/v1` resources, so we override the events storage after the core provider creates the base APIGroupInfo.

**DynamicProvider** (`dynamic.go`): Proxies requests to Activity using client-go's dynamic client. Injects X-Remote-User, X-Remote-Group, and X-Remote-Extra-* headers from the authenticated user context.

**REST** (`rest.go`): Implements Kubernetes REST storage interfaces (Creater, Getter, Lister, Updater, Deleter, Watcher). Calls `injectScopeAnnotations` before Create/Update to add multi-tenant scope.

**Scope Injection** (`scope.go`): Extracts scope from `user.Extra["iam.miloapis.com/parent-type"]` and `user.Extra["iam.miloapis.com/parent-name"]` and injects as `platform.miloapis.com/scope.type` and `platform.miloapis.com/scope.name` annotations.

### Multi-Tenancy

Events are automatically scoped based on the authenticated user's context.

The proxy extracts scope from user.Extra fields:

- `iam.miloapis.com/parent-type` → `platform.miloapis.com/scope.type` (annotation)
- `iam.miloapis.com/parent-name` → `platform.miloapis.com/scope.name` (annotation)

Activity filters events based on these scope annotations:

| User Scope | Visibility |
|------------|------------|
| Platform | All events |
| Organization | Events with matching scope.type=Organization and scope.name |
| Project | Events with matching scope.type=Project and scope.name |

Users cannot modify scope annotations. The proxy re-injects scope on every Create and Update operation to prevent tampering.

### Retry Logic

Transient errors are retried automatically:

- Network errors
- Timeouts
- 5xx server errors
- 429 Too Many Requests

Non-transient errors fail immediately:

- 400 Bad Request
- 404 Not Found
- 403 Forbidden
- 409 Conflict

## Deployment

### Certificate Setup

Create mTLS certificates for Milo to authenticate with Activity:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: milo-activity-client
  namespace: milo-system
spec:
  secretName: milo-activity-client-cert
  issuerRef:
    name: activity-ca-issuer
    kind: ClusterIssuer
  commonName: milo-apiserver
  dnsNames:
    - milo-apiserver.milo-system.svc
  usages:
    - client auth
```

### API Server Deployment

Update the Milo API server deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: milo-apiserver
  namespace: milo-system
spec:
  template:
    spec:
      containers:
        - name: apiserver
          args:
            - --feature-gates=EventsProxy=true
            - --events-provider-url=https://activity-apiserver.activity-system.svc:443
            - --events-provider-ca-file=/etc/milo/activity/ca.crt
            - --events-provider-client-cert=/etc/milo/activity/tls.crt
            - --events-provider-client-key=/etc/milo/activity/tls.key
            - --events-provider-timeout=30
            - --events-provider-retries=3
            - --events-forward-extras=iam.miloapis.com/parent-type
            - --events-forward-extras=iam.miloapis.com/parent-name
            - --events-forward-extras=iam.miloapis.com/parent-api-group
          volumeMounts:
            - name: activity-certs
              mountPath: /etc/milo/activity
              readOnly: true
      volumes:
        - name: activity-certs
          secret:
            secretName: milo-activity-client-cert
```

### Kustomization

Use kustomize patches to enable the feature:

```yaml
# config/overlays/staging/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - ../../base

patches:
  - target:
      kind: Deployment
      name: milo-apiserver
    patch: |-
      - op: add
        path: /spec/template/spec/containers/0/args/-
        value: --feature-gates=EventsProxy=true
      - op: add
        path: /spec/template/spec/containers/0/args/-
        value: --events-provider-url=https://activity-apiserver.activity-system.svc:443
      # ... additional flags
```

## Verification

Check that events are proxied to Activity:

```bash
# Create a test event
kubectl create -f - <<EOF
apiVersion: v1
kind: Event
metadata:
  name: test-event
  namespace: default
involvedObject:
  kind: Pod
  name: test-pod
reason: Testing
message: This is a test event
type: Normal
EOF

# Verify it appears in kubectl
kubectl get events -n default

# Verify it's stored in Activity (not etcd)
# Check Activity logs for incoming requests
kubectl logs -n activity-system -l app=activity-apiserver
```

## Troubleshooting

**Events not appearing**:
- Check EventsProxy feature gate is enabled
- Verify Activity API server URL is reachable
- Check mTLS certificates are valid
- Review Milo API server logs for proxy errors

**Permission errors**:
- Verify Activity trusts Milo's client certificate
- Check X-Remote-* headers are being forwarded
- Ensure scope annotations are being injected

**High latency**:
- Increase `--events-provider-timeout`
- Check Activity API server performance
- Review network latency between Milo and Activity

## References

- Activity Events Storage: `activity-events-pipeline/docs/enhancements/001-kubernetes-events-storage.md`
- Sessions Provider: `internal/apiserver/identity/sessions/`
- Feature Gates: `pkg/features/features.go`
