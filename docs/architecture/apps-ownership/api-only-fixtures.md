# API-only ownership guard fixtures

This test-only slice is based directly on guard #850 (`d913e35c`). It does not
merge #852 or later storage-seeding fixtures. Production ownership enforcement,
its mandatory startup registration, and its absence of acquisition privileges
remain unchanged.

`TestAppsOwnershipAPIFixture` starts an isolated Milo HTTP server and creates all
objects through controller-runtime clients. Milo alone accesses its normal
backend. The test has no KV client, key-prefix lookup, raw record writes, storage
factory, registry REST calls, or assumed persistence encoding.

## Setup boundary

The child test binary constructs ordinary Milo options and uses Kubernetes'
supported admission decorator interface. A test-only decorator temporarily lets
one exact fixture username/UID prepare an explicit set of namespaced ConfigMaps
and a Workload. It wraps only AppsOwnershipEnforcement; other admission and native
validation still run. It does not expose a production flag, HTTP endpoint,
request header, or trusted-writer configuration.

After all setup HTTP responses complete, the parent sends one byte through the
child's stdin. The decorator's one-way latch seals permanently and acknowledges
closure through a temporary local file. All enforcement assertions run afterward.
Both customer and setup credentials must then fail to create ownership, acquire
ownership on an unmarked fixture, mutate marked resources, strip markers or
finalizers, or delete protected resources. Vacant/acquirable fixture names are
explicitly allowed during setup, so the closure tests distinguish a sealed latch
from merely requesting an out-of-scope fixture name.

This prepares pre-existing protected state for testing. It is not an Apps
ownership-acquisition design, credential-custody proof, or installation privilege.
The test binary and its environment variables are not present in production.

## Native resources and assertions

The core ConfigMap, native Compute Workload, and terminating ConfigMap are created
through the Project control-plane URL. Termination uses an ordinary DELETE with
UID/resourceVersion preconditions and an existing finalizer; the server assigns
UIDs, versions and deletion timestamps. No storage metadata is manufactured.

The Workload CRD is byte-for-byte Compute source
`88a7101c2b91f7195c2bb5433c50f54c384a90ce`, with SHA-256
`ba38aeadb5d6c7a97c9b05a031d9955099820f5e7edd206a43fe3dd7f031b889`.
The test checks this digest before installing it through the root CRD API. Its
upstream Project discovery annotation is retained. This validates native schema
and Milo routing, not Compute webhooks, reference existence, placement, or native
controller lifecycle.

After denied PUT, merge patch, SSA, and DELETE requests, GET must return the exact
unchanged resource, including UID/resourceVersion, markers and finalizers.
An unmarked ConfigMap proves ordinary updates remain usable, stale RV writes and
deletes conflict, and a stale UID delete cannot affect a same-name replacement.
Authorization is deliberately AlwaysAllow to isolate admission; this does not
prove production IAM, Project teardown or customer-install acceptance.

## Running

Run the focused API and guard unit race tests through the repository Taskfile:

```sh
TASK_X_REMOTE_TASKFILES=1 task test:apps-ownership-api
```

Set `KUBEBUILDER_ASSETS` to the installed Kubernetes 1.33.0 envtest asset directory.
This starts disposable local processes only; it does not use a kubeconfig or any
live cluster. Do not use `test:end-to-end` for this fixture; that task targets a
configured cluster. The root Taskfile loads its existing checksum-pinned remote
Taskfile during parsing; the focused task does not invoke its infrastructure tasks.

The existing `IAM API testing` workflow already installs envtest 1.33.0 and runs
`go test -timeout 5m ./...`, which includes this untagged fixture. Missing assets
fail the test rather than skipping it. Changes to the pinned Workload fixture
also trigger that workflow. CI does not currently enable the race detector; the
focused local task does. `go test -short` intentionally skips this API fixture.

Local validation used Kubernetes 1.33.0 darwin-arm64 envtest assets and the
repository `test:apps-ownership-api` task: focused HTTP race tests passed in
14.701s and guard unit race tests in 1.316s. Affected-package vet also passed
through a temporary Task wrapper.
Temporarily replacing the production validator with `return nil` made
`customer_cannot_reacquire` fail because marker creation unexpectedly succeeded.
The original validator was restored byte-for-byte, then the complete HTTP suite
passed again. No production source changes are part of this slice.

Independent lead validation repeated the focused Taskfile task: HTTP race
tests passed in 14.663s and guard unit race tests in 1.318s. A separate read-only
review found no actionable issue in setup closure, API-only fixture creation,
or unchanged-state assertions. This remains admission isolation evidence, not
production IAM or lifecycle acceptance.
