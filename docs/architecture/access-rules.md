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

## Invalid declarations are refused

A rule that is mistyped or can't be applied must never turn protection off
quietly. Milo treats a CRD's declaration as invalid when any
`iam.miloapis.com/` annotation on it:

- is blank;
- isn't one of the three above, such as a misspelt one;
- names a field that isn't a plain dotted path (`spec.userRef.name`, not
  `spec['userRef']`), or isn't a `selectableField` of every served version;
- puts the owner field under `status`, or where the `scale` subresource
  reads or writes it. Writes to subresources skip the owner check, so those
  paths would let a user change an object's owner;
- sets `hidden-subresource` without `hidden-field`.

Milo refuses an invalid declaration in three places:

1. **When the CRD is written.** The `AccessRuleOwnerEnforcement` admission
   plugin rejects a CRD create or update that introduces a problem, with an
   `Invalid` error naming the annotation. An update only fails for problems
   the stored CRD didn't already have, so a CRD stored before this check can
   still be fixed or updated.
2. **When the resource is used.** A CRD that is invalid anyway (one stored
   before the check) has every request for its resource refused with
   `ServiceUnavailable`, naming the CRD and the problem, rather than served
   without its rule. Callers in `system:masters`, including Milo's loopback
   client, are still served, so operators can read and repair the resource.
3. **In monitoring.** The `milo_access_rule_invalid{group,resource,reason}`
   gauge is 1 for each problem, the `MiloAccessRuleInvalid` alert fires on
   it, and the API server logs the problem when it first sees it.

## Who the owner is

The owner rule limits a request to one user's objects:

- **User context** (`/apis/iam.miloapis.com/v1alpha1/users/{user}/control-plane`):
  the user in the path. Authorization has already decided the caller may act
  as that user.
- **Organization context**: the caller's user ID. User-owned objects share one
  collection for the whole platform, so a role in an organization that grants
  the resource still only shows the caller's own objects. A caller with no
  user ID is refused with `Forbidden`. That includes a service account
  without a user ID, so a controller that works on users' objects must do so
  in the platform context; in an organization context a service account only
  sees objects whose owner field holds its own ID.
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
  answers `NotFound` when it belongs to another user or is hidden. It reads
  the hidden field the way storage selects lists: a missing field shows the
  object, `true` hides it, and a value it can't read, such as a path through
  a list, hides it. An object that doesn't exist passes through, so the
  handler answers as usual. This costs one extra read per named request to
  an annotated resource.

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

## When rules take effect

Milo reads a resource's rule at request time from the API server's
CustomResourceDefinition cache: the same cache the CRD handler serves custom
resources from. A custom resource is only served once its CRD is in that
cache, and then its rule is too, so creating a CRD with its annotations
leaves no window in which its objects are served without the rule.

While the rules can't be trusted, Milo fails closed. Callers outside
`system:masters` are refused with `ServiceUnavailable`:

- **Before the cache has synced**, for every resource request outside a
  project, since any resource may turn out to have a rule.
- **While a CRD's cached copy lags storage**, for that CRD's resources
  only. Every 30 seconds Milo compares the resource version of each CRD in
  storage with the cache. If a CRD still differs 10 seconds later and its
  cached copy hasn't moved, that CRD is stale until a later check finds it
  caught up. Other resources, built-in and custom, are served as usual. The
  `milo_access_rules_stale` gauge counts the stale CRDs, and the
  `MiloAccessRulesStale` alert fires when any stay stale for 5 minutes.

Only the first state fails the `milo-access-rules` readiness check, which
keeps traffic away from an API server that is still starting. Staleness
doesn't: a watch problem shared by every replica would otherwise take them
all out of rotation at once, and with them every resource Milo serves.

The `system:masters` exemption lets Milo's own startup hooks, which use the
loopback client, and operators work while the rules are unavailable.

## Limits

- Rules apply to platform storage. Requests in a project context pass
  through unchanged.
- Adding or changing a rule on a CRD that is already served takes effect on
  each API server when its CRD cache sees the change, normally within a
  second; Milo warns the client that made the change. Until then that API
  server applies the previous declaration, so a request it serves in that
  window, including a write, isn't checked against the new rule. Objects
  stored before a rule took effect aren't re-checked either. Add the
  annotations in the same change that installs the CRD, before any objects
  exist. A staleness check that takes up to 40 seconds to notice a stuck
  cache bounds the window when the cache stops following storage.
- In the platform context the owner rule doesn't apply, so a platform role
  that grants an owned resource shows every user's objects. Only grant those
  to staff and services.
- The hidden-field rule asks the authorizer once per request, with the
  caller's identity and parent context, like any other authorization check.
  Grant `get` on `<resource>/hidden` in each context where staff should see
  hidden objects.
