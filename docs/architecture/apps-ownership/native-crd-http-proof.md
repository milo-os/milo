# Apps ownership guard with native CRD schemas

This probe extends [`appsownership_integration_test.go`](../../../cmd/milo/apiserver/appsownership_integration_test.go) in a disposable Milo clone at `64ede47c4cc0d3831e052d37694725a6e72f57a8` (branch `codex/apps-ownership-native-crd-probe`). It starts isolated local etcd and the actual Milo API server, installs the CRDs through the platform/root API, then tests their real project control-plane API routes. It does not start the Workload controller, Gateway controller, webhook chain, or a data plane.

## Auditable CRD inputs

The checked-in fixtures under `cmd/milo/apiserver/testdata/appsownership/` are complete upstream files, not simplified schemas:

| Fixture | Upstream source | SHA-256 |
|---|---|---|
| `workloads.compute.datumapis.com.yaml` | `datum-cloud/compute` commit `88a7101c2b91f7195c2bb5433c50f54c384a90ce`, `config/base/crd/bases/compute.datumapis.com_workloads.yaml` | `ba38aeadb5d6c7a97c9b05a031d9955099820f5e7edd206a43fe3dd7f031b889` |
| `httproutes.gateway.networking.k8s.io.yaml` | Gateway API v1.5.1 standard bundle at `config/crd/standard/gateway.networking.k8s.io_httproutes.yaml`; v1.5.1 is pinned by `network-services-operator/go.mod` | `682203b109baecb18d38073a578f7bc2c17d22fe9b0c27bf030850abe014c370` |

The Workload upstream CRD already carries `discovery.miloapis.com/parent-contexts: Project`. The Gateway API upstream CRD does not. At installation time the test adds only `discovery.miloapis.com/parent-contexts: Project` to the HTTPRoute CRD metadata annotations. This documented Milo overlay changes discovery visibility, not its OpenAPI validation schema. Milo's `config/discovery/core/discovery-context-policy.yaml` restricts CRD definitions to Platform context; therefore the test installs both definitions through the platform/root CRD endpoint, waits for `Established=True`, then checks the resource/version discovery and CRUD requests under the project path.

## Reproduction

The full test runs with local envtest etcd assets and a loopback listener:

```sh
TEST_ASSET_ETCD=/path/to/envtest/etcd \
  go test ./cmd/milo/apiserver -run '^TestAppsOwnershipHTTPIntegration$' -count=1 -timeout=120s
```

`TEST_ASSET_ETCD` can be replaced by `KUBEBUILDER_ASSETS` pointing to a directory containing `etcd`, or by an `etcd` binary on `PATH`. Milo runs with a temporary token file and self-signed TLS, `--authorization-mode=AlwaysAllow`, and `--watch-cache=false`. The identities are test-only identities; all are expected to be denied by the Apps ownership guard. AlwaysAllow only makes the requests reach admission and is not evidence of IAM roles, controller credentials, or deployed behavior.

## Covered API behavior

After root-installing the CRDs and waiting for their `Established` conditions, the test verifies project discovery for Workload `v1alpha` and HTTPRoute `v1` plus `v1beta1`. It creates schema-valid unmarked Workload and HTTPRoute controls, and verifies unmarked deletion succeeds. Each of four temporary token identities (customer, Apps controller, release worker, `system:masters`) receives the guard-specific denial on marker-bearing create, label/annotation marker-forging merge patch, and writes to old marked objects. For old markers it checks PUT, merge patch, marker stripping, server-side apply, status patch, and DELETE. HTTPRoute `v1beta1` is separately exercised to cover the additional served version.

Old marked objects are not inserted with an assumed storage path. Each is first created through Milo, then the test enumerates only the disposable project's etcd records, decodes JSON, matches `kind`, namespace, and name, changes only ownership marker metadata, and writes with an etcd `ModRevision` compare-and-swap. It reads the seeded object back through Milo before testing admission. No object values or temporary credentials are printed.

## Claims and limits

This proves the bootstrap admission plugin's marker behavior through Milo's HTTP router using native CRD validation and project-context discovery. It does not prove IAM authorization, trusted writer identity, release authorization, CRD webhooks, native controller reconciliation, endpoint traffic, customer-facing acceptance, or production rollout. The valid objects satisfy the vendored CRD schemas; they intentionally do not claim that external references such as the Workload location or HTTPRoute backend exist.

## Review and race validation

Lead independently checked both fixture hashes and ran the real HTTP probe plus
log-buffer regression under `-race`; combined package result was 20.144s.
CRD installation must return 201; an existing definition is a setup failure.
Project discovery is polled for the exact plural and kind, and denied operations
are followed by reads asserting unchanged UID, resourceVersion and markers.

The first race run exposed promoted `bytes.Buffer.ReadFrom` bypassing the log
buffer's Write lock. Holding a mutex across ReadFrom then blocked diagnostics
until child shutdown. The final fix uses a private buffer with short synchronized
Write/String calls; its regression test keeps child output open while requiring
String to return. The final run has neither race nor stream-lifetime lock.
