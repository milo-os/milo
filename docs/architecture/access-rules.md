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
| `iam.miloapis.com/owner-field` | Field path of the owner's user ID, e.g. `spec.userRef.name` | In the user context, lists, watches and collection deletes only cover the user's objects, and named requests for another user's object answer `NotFound`. |
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

## How it works

The filter runs inside authorization, at the innermost point of the handler
chain, so it only narrows what an authorized request returns.

- **Lists, watches and collection deletes.** Milo rewrites the request's field
  selector. It drops any term the client sent on the rule's field and adds its
  own: `spec.userRef.name=<user>` or `spec.hidden!=true`. Every other term
  keeps its operator.
- **Named requests** (`get`, `update`, `patch`, `delete` and their
  subresources). Milo reads the stored object through its loopback client and
  answers `NotFound` when it belongs to another user or is hidden. An object
  that doesn't exist passes through, so the handler answers as usual. This
  costs one extra read per named request to an annotated resource.
- **Creates** are not checked here: the request body hasn't been decoded yet.
  The owning service's admission webhook must set the owner field from the
  request's user and keep it immutable.

The rules come from a CRD informer. Milo isn't ready until the informer has
synced, and while it hasn't, user-context requests answer
`ServiceUnavailable` rather than risk serving a resource without its rule.

## Limits

- Rules apply to platform storage. Requests in a project context pass
  through unchanged.
- The hidden-field rule checks permission once per request with the caller's
  identity and parent context. A role that grants `get` on `<resource>/hidden`
  in the platform context doesn't carry into the user context.
