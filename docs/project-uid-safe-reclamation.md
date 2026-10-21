# UID-safe ProjectControlPlane reclamation

This change hardens the existing Project cleanup controller. It introduces no
Apps retention hook, admission privilege, enrollment, direct etcd client, or
backend transaction. It preserves the purge behavior from #854 and the
fail-closed API-read handling from #858 (base `483d44c3`).

After project purge reports completion, the controller must successfully write
its existing `ResourceCleanup=False/CleanupComplete` condition before requesting PCP
deletion. On later reconciles that condition resumes only infrastructure
reclamation: the project API can already have stopped. This is a recovery
checkpoint, not evidence that concurrent project writes have been fenced.
Project status writers are trusted to maintain this controller-owned condition;
this patch does not prevent another authorized status writer from forging it.

Reclamation reads the PCP with the infrastructure APIReader, verifies its
`resourcemanager.miloapis.com/project-uid` label against the terminating root
Project UID, and requires nonempty PCP UID and resourceVersion. DELETE includes
both UID and resourceVersion preconditions. API failures, mismatched labels,
concurrent changes, and an already-terminating PCP retain the root finalizer.
After a successful DELETE the controller waits for a later uncached GET to
observe NotFound. It does not treat successful DELETE as physical disappearance.

Before PCP reclamation and again before root finalizer removal, the root
APIReader must still observe `ResourceCleanup=False/CleanupComplete`. A cached
condition or a condition subsequently changed/removed cannot authorize progress.
Root finalizer removal uses that same fresh snapshot, requires the original Project
UID and a still-terminating object, and patches only its own finalizer with
optimistic resourceVersion locking. Other finalizers remain. Missing APIReaders
fail closed; the existing nil infrastructure-client deployment mode is retained.
`SetupWithManager` installs both APIReaders from the actual root and infrastructure
clusters.

There is no cross-object transaction: a separately privileged actor can create a
PCP after the absence read. The controller also cannot fence in-flight resource
writes after the cleanup checkpoint. This is not complete Apps/project teardown,
does not protect against a caller authorized to remove root finalizers, and does
not define service history retention. Legacy PCPs missing the Project UID label
will block rather than be adopted or silently deleted. Operators must investigate
and repair their binding through ordinary authorized APIs.

## Tests

`TestProjectReclamationAPI` uses controller-runtime clients against an isolated
envtest Kubernetes API server with the repository Project and PCP CRDs. Every
fixture and race mutation uses Kubernetes APIs. The purger's project-endpoint
response is a local HTTP fixture reporting an empty NamespaceList; these tests do
not claim a full multi-control-plane deployment or exercise infrastructure
provisioning. No fixture connects to etcd or reads/writes backend keys.

Coverage includes full Reconcile waiting for PCP disappearance, continuing when
the project endpoint is unavailable after cleanup, missing/wrong Project binding,
GET/DELETE failures, cleanup-status conflict before deletion, PCP replacement
between GET and DELETE, changed PCP labels/RV, root replacement before GET or
PATCH, optimistic root patch conflict, preservation of unrelated finalizers,
stale cached NotFound, missing APIReaders, and changed/removed authoritative
completion conditions both before PCP reclamation and after PCP absence is read.

The tests run under the repository's `task test:unit` when `KUBEBUILDER_ASSETS` is
set. Without assets they skip locally and fail in CI. Focused local verification
used a temporary Taskfile wrapper containing:

```yaml
version: '3'
tasks:
  test:
    cmds:
      - go test -race ./internal/controllers/resourcemanager ./internal/controllers/projectpurge -count=1 -timeout=180s
  vet:
    cmds:
      - go vet ./internal/controllers/resourcemanager ./internal/controllers/projectpurge
```

Assets: `/Users/scotwells/Library/Application Support/io.kubebuilder.envtest/k8s/1.33.0-darwin-arm64`.

Independent lead verification on 2026-10-20 passed the focused API/race suites
in 9.149 seconds (resource manager) and 1.587 seconds (purger), plus vet, through
that Taskfile wrapper. An independent read-only review found no actionable
issue within this bounded reclamation path. A temporary negative-control edit
removed the PCP resourceVersion delete precondition; the intervening-update
test failed as expected, and the original source was restored. No live Milo
cluster was mutated, and no full Apps lifecycle gate is established.
