# Owner and hidden-field access rules

Milo keeps resources a user owns in one collection for the whole platform:
only projects get their own storage. Authorization decides whether a user may
list a resource in their own context, but not which objects come back. Without
help, a user listing votes in their user context would get every user's votes.

Milo has handled this with one hard-coded filter per resource (contacts,
invitations, organization memberships). Access rules do the same job for any
CRD that asks for it with annotations, so a service doesn't need a Milo change
for each resource it adds.

## Annotations

| Annotation | Value | Effect |
| --- | --- | --- |
| `iam.miloapis.com/owner-field` | Field path of the owner's user ID, e.g. `spec.userRef.name` | In the user and organization contexts, lists, watches and collection deletes only cover the user's objects, named requests for another user's object answer `NotFound`, and writes can't set or change the owner to anyone else. |
| `iam.miloapis.com/hidden-field` | Field path of a boolean, e.g. `spec.hidden` | Lists, watches and collection deletes leave out objects whose field is `true`, and named requests for them answer `NotFound`, unless the caller is allowed `get` on the hidden subresource. Applies in every context except projects. |
| `iam.miloapis.com/hidden-subresource` | Subresource name, default `hidden` | The subresource whose `get` permission lets a caller see hidden objects. It doesn't need to be served; Milo only asks the authorizer about it. |

Both fields must be `selectableFields` of every served version of the CRD, so
storage can filter on them:

```go
// +kubebuilder:metadata:annotations="iam.miloapis.com/owner-field=spec.userRef.name"
// +kubebuilder:selectablefield:JSONPath=`.spec.userRef.name`
type Vote struct { ... }
```

If a field isn't selectable, Milo logs an error and lists of the resource fail
with a field selector error, which keeps the data hidden.

Milo also logs an error for a CRD whose rule annotation is blank, and for any
other `iam.miloapis.com/` annotation on a CRD, such as a misspelt one. Neither
makes a rule, so the CRD's objects aren't restricted.

## Who the owner is

The owner rule limits a request to one user's objects:

- **User context** (`/apis/iam.miloapis.com/v1alpha1/users/{user}/control-plane`):
  the user in the path. Authorization has already decided the caller may act
  as that user.
- **Organization context**: the caller's user ID. User-owned objects share one
  collection for the whole platform, so a role in an organization that grants
  the resource still only shows the caller's own objects. A caller with no
  user ID is refused with `Forbidden`.
- **Platform context**: no owner rule. Staff and services work here, and
  services create objects on users' behalf.
- **Project context**: no rules at all; projects have their own storage.

## How it works

The filter runs inside authorization, at the innermost point of the handler
chain, so it only narrows what an authorized request returns.

- **Lists, watches and collection deletes.** Milo rewrites the request's field
  selector. It drops any term the client sent on the rule's field and adds its
  own: `spec.userRef.name=<user>` or `spec.hidden!=true`. Every other term
  keeps its operator. Table and metadata-only responses are built from the
  narrowed list, so they're narrowed too.
- **Named requests** (`get`, `update`, `patch`, `delete` and their
  subresources). Milo reads the stored object through its loopback client and
  answers `NotFound` when it belongs to another user or is hidden. An object
  that doesn't exist passes through, so the handler answers as usual. This
  costs one extra read per named request to an annotated resource.

  The loopback client's identity is in `system:masters`, which the server's
  privileged-groups authorizer allows, and its requests are in the platform
  context. So its read isn't narrowed by the owner rule, sees hidden objects,
  and only answers `NotFound` when the object really doesn't exist.
- **Writes.** The filter can't check a write's body: it runs before the body
  is decoded. The `AccessRuleOwnerEnforcement` admission plugin does, in the
  user and organization contexts. A create, including a server-side apply
  that creates, must set the owner field to the request's owner, and an
  update or patch can't change it. The plugin validates and doesn't mutate,
  so it runs after mutating webhooks: a service's webhook may fill the owner
  field in from the request, and the plugin checks the result.

The rules come from a CRD informer. Milo isn't ready until the informer has
synced (the `milo-access-rules` readiness check), and while it hasn't, user-
and organization-context requests answer `ServiceUnavailable` rather than risk
serving a resource without its rule.

## Limits

- Rules apply to platform storage. Requests in a project context pass
  through unchanged.
- A rule takes effect when the informer sees the CRD change, a moment after
  the CRD is written. Add the annotations in the same change that installs
  the CRD, before any objects exist.
- In the platform context the owner rule doesn't apply, so a platform role
  that grants an owned resource shows every user's objects. Only grant those
  to staff and services.
- The hidden-field rule asks the authorizer once per request, with the
  caller's identity and parent context, like any other authorization check.
  Grant `get` on `<resource>/hidden` in each context where staff should see
  hidden objects.
