# Milo Apps ownership guard HTTP probe

This is a bounded local feasibility probe against Milo source at commit
`d913e35c822825195a911030c4ee039c1aed7d71` (detached clone
`/tmp/apps-milo-api-probe`). It does not edit or switch the provider checkout.

## Reproduction

The test is `cmd/milo/apiserver/appsownership_integration_test.go`:

```sh
GOCACHE=/tmp/apps-milo-api-gocache go test ./cmd/milo/apiserver \
  -run '^TestAppsOwnershipHTTPIntegration$' -count=1 -timeout=120s
```

The harness starts a disposable local etcd through controller-runtime `envtest`
and starts the actual Milo `NewCommand()` in a child test process, bounded by a
105-second context and 60-second readiness deadline. It serves HTTPS on a
temporary loopback port and accesses the public project route
`/apis/resourcemanager.miloapis.com/v1alpha1/projects/<test-project>/control-plane/...`.
Temporary bearer credentials, service-account key, certificate, project and
namespace names are generated for each run in `t.TempDir`; the test does not
print credentials. Local `etcd` must be available on `PATH` or via
`TEST_ASSET_ETCD` / `KUBEBUILDER_ASSETS`.

The options include `--etcd-servers`, `--bind-address=127.0.0.1`, a free
`--secure-port`, temporary `--cert-dir`, `--token-auth-file`,
`--anonymous-auth=false`, service-account issuer/audiences/key flags,
`--storage-media-type=application/json`, and `--watch-cache=false`.
`--authorization-mode=AlwaysAllow` is deliberately used so the probe tests
admission registration and behavior without OpenFGA/project-role fixtures.
This is test-only and proves no IAM authorization, deployment identity,
production TLS, or deployed topology.

## Verified over actual HTTP

The strengthened test passes in 3.332 seconds. Every expected 403 is also
checked for the exact guard-specific denial text (`Apps ownership is reserved;
managed resource writes remain disabled`), so another admission or
impersonation denial cannot satisfy the assertion. It verifies for four token identities
(customer, Apps controller, release worker, and `system:masters`):

* creating a ConfigMap bearing either Apps ownership marker is HTTP 403;
* a normal unmarked ConfigMap can be created (HTTP 201);
* JSON merge-patching either marker onto the existing unmarked object is
  HTTP 403; and
* after rejected patches, GET confirms the ownership label/annotation did not
  persist, then ordinary unmarked deletion succeeds.

It also verifies an admin request impersonating
`system:serviceaccount:apps-system:controller` cannot create a marked object
(HTTP 403). For ConfigMaps with old markers, it verifies HTTP PUT, JSON merge
PATCH, marker stripping by PATCH, server-side apply PATCH, and DELETE all get
the guard-specific 403. For a Namespace with old markers it verifies the
`/status` PATCH is also denied. Both objects are created normally first; the
test enumerates the temporary etcd records, identifies them by decoded kind,
namespace, and name, mutates marker metadata only, and uses a ModRevision CAS.
GET through Milo confirms each marker seed is visible before exercising the
old-object admission path. No object values or credentials are logged. The
server is reached through Milo's project-control-plane route, so the guard is
exercised through its registered API admission chain, not by calling the
plugin directly.

## Limits

The current test does not use actual OpenFGA, customer project roles, deployed
service identities, production TLS, or deployed topology. Authorization is
AlwaysAllow solely to reach admission by each temporary bearer identity. The
negative assertion is encoded in every expected 403 check: disabling/removing
the guard must not yield a passing assertion because the exact Apps-specific
denial reason is required; no separate no-guard binary was built.

The sandbox rejected loopback binding under its default restriction; the
successful command was run with the explicit elevated local-test permission.
No external service or production system was contacted.
