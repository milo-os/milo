# Authentication provenance

Milo's shared API handler chain captures the authenticated user before Kubernetes
impersonation replaces it and removes the impersonation headers. Admission and
other downstream code can read the snapshot with
`filters.AuthenticationProvenanceFrom(ctx)`.

The execution order is authentication, provenance capture, impersonation, scope
decorators, and authorization. The core, CRD, and aggregation server handler
builders use this shared chain. Capture uses only `request.UserFrom(ctx)`; it
never extracts an identity or a trust claim from HTTP headers or user extras.

The snapshot contains name, UID, groups, and extras. Groups and all extra-value
slices are copied on capture and on every read. Mutating the authenticator's
user object later, or a returned snapshot, does not change stored provenance.
The reader's boolean result distinguishes missing provenance from a captured
request without impersonation. Consumers must not interpret missing provenance
as evidence that a request was authenticated directly.

`ImpersonationRequested` records the presence of any `Impersonate-*` header,
case-insensitively, including empty values and unsupported names. It records an
attempt, not whether Kubernetes accepted it. This deliberately catches
self-impersonation even when the effective user fields are identical to the
authenticated user's fields. Kubernetes remains responsible for validating and
authorizing the impersonation request.

This filter grants no permissions, blocks no requests, and classifies no Apps or
release identity as trusted. A future guard needs an independently verified
principal and credential-custody contract, project UID checks, and explicit
impersonation restrictions. A name, group, or extra value is not sufficient.
The existing authentication configuration remains the authority for the captured
identity, including trusted front-proxy authentication where configured.

Provenance is private in-process request context. It is not serialized to
downstream aggregated APIs or other HTTP services. A receiving server observes
the identity established by its own authenticator; it cannot reconstruct an
upstream caller's impersonation history from this snapshot. Enforcement must
therefore cover the entry boundary before forwarding, or use a separately
specified authenticated provenance protocol. This change does not introduce
such a protocol or alter audit payloads.

## Validation

```sh
go test ./pkg/server/filters ./cmd/milo/apiserver -count=1 -timeout=120s
```

Filter tests cover deep copies on capture/read, missing context, forged remote
identity headers and extra claims, and case/value variants of impersonation
headers. The API server test executes Milo's full middleware chain with the
actual Kubernetes authentication and impersonation filters. Its authorizer
deliberately permits impersonation, proving the original identity survives an
accepted user/UID/group/extra replacement and header removal. It also checks
self-impersonation, empty and noncanonical headers, and preservation of
Kubernetes' rejection of malformed impersonation requests.

This is an A2/A3 prerequisite, not ownership enforcement or evidence that Apps
installations can be enabled. No trusted native writer is enabled by this change.
