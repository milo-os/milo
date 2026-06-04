# Events Proxy Component

This component enables the Events proxy feature that forwards Kubernetes Events
to the Activity API server.

## What It Does

Intercepts requests to the core/v1.Event API and proxies them to the Activity
service. This provides centralized event storage with consistent retention and
querying. The proxy automatically injects scope annotations and forwards user
context via X-Remote-* headers.

## Prerequisites

- Activity API server deployed and accessible
- mTLS certificates in a secret named `milo-activity-client-cert`
- Network connectivity from Milo to Activity

## Configuration

### Feature Gate

This component enables the `EventsProxy` feature gate (currently Alpha).
Sessions and UserIdentities are GA and enabled by default.

```yaml
- name: FEATURE_GATES
  value: "EventsProxy=true"
```

### Required Configuration in Your Overlay

1. **Enable the component** in `kustomization.yaml`:
   ```yaml
   components:
     - ../../components/events-proxy
   ```

2. **Set Activity URL** via environment variable:
   ```yaml
   - name: EVENTS_PROVIDER_URL
     value: "https://activity-apiserver.activity-system.svc.cluster.local:443"
   ```

3. **Provision mTLS certificate** secret with `ca.crt`, `tls.crt`, and `tls.key`

## Verification

Check API server logs for Events proxy initialization:
```bash
kubectl logs -n milo-system deployment/milo-apiserver | grep -i "events"
```

Create a test event and verify it appears in Activity:
```bash
kubectl get events test-event -o yaml
```

## Troubleshooting

**Events proxy not initialized**: Verify `FEATURE_GATES` includes `EventsProxy=true`,
`EVENTS_PROVIDER_URL` is set, and certificate secret exists.

**Certificate errors**: Verify certificate is signed by Activity's CA, includes
`client auth` usage, and hasn't expired.

**Network issues**: Check Activity is running, DNS resolves, and network
policies allow traffic.

**Events not appearing**: Verify `EVENTS_FORWARD_EXTRAS` is configured and
Activity processes Event resources.

## Implementation

See `internal/apiserver/events/` for implementation details including storage
provider wrapping, REST storage with proxying, dynamic client with header
injection, and scope annotation handling.
