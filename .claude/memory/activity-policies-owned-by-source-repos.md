---
name: activity-policies-owned-by-source-repos
description: ActivityPolicy CRs are authored in the repo of the service that owns the resource (milo owns its own under config/services/activity/policies/), shipped to the control plane via an OCI bundle
metadata:
  type: project
---

`ActivityPolicy` CRs (`activity.miloapis.com`) are authored in the repo of the
service that **owns the resource**, not in a central deploy repo and not in the
activity service repo. The deploy repo only holds Flux Kustomizations that apply
each service's OCI bundle to the milo control plane, all `dependsOn` the activity
aggregated apiserver being ready (it serves the `activity.miloapis.com` CRDs).

milo owns the policies for its own platform resources (Project, Organization,
IAM, identity, notes, notification):
- Authored in `config/services/activity/policies/`.
- Shipped via the `milo-kustomize-bundles` OCIRepository, path `./services/activity`
  (Flux Kustomization `milo-activity-policies`), `targetNamespace: milo-system`.

Other services (billing, network-services-operator) ship their own policies the
same way from their own repos.

To change a milo policy in a live control plane: edit the policy file here,
merge, cut a release; Flux re-applies the CR (overwriting the live object) — no
manual kubectl.

**Guard `responseObject` access.** A rejected request carries no
`responseObject`, so a template that reads it unguarded fails and the event
lands in the activity processor DLQ. The existing iam and resourcemanager
create policies already wrap it in `has(audit.responseObject.metadata.name) ?
... : ...` fallbacks; follow that shape in any new or edited policy.
