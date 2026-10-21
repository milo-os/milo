# Disabled AppInstallGrant family and registration reservation

This change reserves the native `apps.miloapis.com/appinstallgrants` resource
family in Milo's mandatory compiled Apps ownership admission plugin. Every
CREATE, UPDATE (including subresources), and DELETE reaching native admission is
denied, independently of API version, markers, Project routing, authenticated
identity, impersonation, and optional native status/finalizer writers. Reads are
unchanged. There are no runtime exceptions, clients, lookups, issuance privileges,
or grant consumption in this slice.

The reservation runs before the existing ownership checks. It also reserves the
exact `appinstallgrants.apps.miloapis.com` CRD, identified by request/object name
or old/new group and plural, and every APIService for the `apps.miloapis.com`
group, identified by name suffix or old/new spec.group. CRD deletion could erase
grant instances indirectly. Aggregation could route the group/version around
native admission; APIService registration has group/version granularity, so this
reservation deliberately affects the whole Apps group, including other resources
served through such an APIService. Unrelated CRDs, APIServices and ordinary APIs
retain their existing behavior.

## Deployment and authority boundary

Production bootstrap is **unavailable**: this guard does not provide a way to
create or establish the grant CRD, issue a grant, request revocation, alter CRD
status, or remove a grant/registration. It is a prerequisite for a separately
reviewed issuance/custody protocol, not an implementation of that protocol.

Before any deployment, inventory existing grant CRDs/instances and all Apps-group
APIServices through native APIs, including their current schemas, conversion,
routing and serving versions. An already-present APIService can route around the
new native guard; denying its future changes does not make that preexisting route
safe. The guard also freezes status updates and removal of a preexisting Apps
APIService or grant CRD. Such registrations require an explicit reviewed migration
before rollout. No live inventory, migration or deployment is claimed here.

This change does not establish fresh install authorization, grant field provenance,
permit consumption, Project teardown/drain, native resource ownership acquisition,
or protection against trusted control-plane/backend administration. No customer
installation or native resource writer is enabled.

## Pinned wire contract and API-only fixture

The embedded CRD and valid wire vector are copied unchanged from Apps draft #46,
commit `e1be5c515cbffb798a67bc2da8278e0d522dddc4`:

- CRD SHA256: `33c9be9ec2f9ac76c1945776b53568c18159bb50743f48144cd28bbc6e039d6d`.
- Vector SHA256: `7fa22024487f856f7fb168572a3e8995819a0ef74422fdf61528d9c575d0a8d0`.

`TestAppsGrantReservationAPIFixture` uses the existing isolated Milo HTTP fixture
on base `200de48a305732720377cb83308d57c84fd5f688`. All setup and assertions use
controller-runtime native API clients. Only the Milo API server accesses its
ephemeral backend; fixture code has no KV client, backend records, storage keys,
or storage factory. Apps webhooks are absent, deliberately proving that this
reservation does not depend on a dynamic hook.

A test-binary-only admission decorator permits the exact setup principal to
create the fixture grant CRD and two named grants while a one-way latch is open.
During that window, native CRD controllers may establish only this CRD through
its status endpoint. The test closes the latch permanently before assertions;
even the setup principal cannot create its second previously permitted name.
There is no production flag, header or credential exception.

The fixture uses AlwaysAllow authorization to isolate compiled admission. It
proves exact guard-message Forbidden responses, not real IAM enforcement. Native
coverage includes schema-valid revocation, metadata PUT/merge/JSON patch/SSA,
DELETE/deletecollection, admin and self-impersonation attempts, unchanged grant
UID/resourceVersion/spec, CRD metadata/status/delete protection, Apps-group
APIService CREATE, and unrelated ConfigMap/CRD/APIService CRUD. Duplicate CRD
CREATE is counted only if native admission returns the exact reservation denial;
an existing-name AlreadyExists response is explicitly not guard evidence.

The family/version/subresource matrix and malformed typed/unstructured registry
records are unit coverage. Unserved grant status/version paths would return 404,
so they are not presented as real API proof. Immutable actor/envelope/digest edits
may be rejected by the CRD's CEL schema before validating admission and are not
counted as compiled-custody proof. APIService UPDATE/DELETE classification is unit
coverage; the real API test does not install an Apps APIService that would reroute
the grant endpoint. The fixture wire vector is bound to its actual Project UID,
but no actual App/request or grant authority is asserted.

## Verification

The existing CI `go test ./...` includes these untagged tests and embedded fixture
paths. The repository task includes the new fixture with the retained default
ownership, native status/finalizer, namespace and provenance regression tests:

```sh
KUBEBUILDER_ASSETS=/path/to/envtest/1.33.0 task test:apps-ownership-api
```

Use the platform's envtest assets. A discriminating negative control temporarily
removes only `reserveGrant` from `Validate`: schema-valid `revokeRequested: true`
then succeeds and the exact-denial assertion fails. Restore the hook before the
full regression run. Neither an API 404 nor a schema/RBAC denial is a passing
negative control.

Local verification on 2026-10-21 with envtest `1.33.0-darwin-arm64`:

- Focused native grant fixture with race detector: PASS, 19.435s.
- Restored full `task --yes test:apps-ownership-api`: PASS; HTTP 123.591s,
  ownership package 1.636s, provenance/filter package 1.602s.
- Affected packages `go vet` through the focused Taskfile: PASS.
- Negative control: expected failure, 18.267s, because the valid revocation
  returned success with only the reservation hook removed. The hook was restored
  byte-for-byte before the full task.

The first native run exposed an ordinary positive-control CRD update conflict
with the Kubernetes establishment controller. The fixture now rereads and retries
that unrelated update through the API; this does not change production behavior.
These local results do not establish deployed Project IAM or lifecycle gates.
