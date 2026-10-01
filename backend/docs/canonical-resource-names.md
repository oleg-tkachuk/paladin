# Resource names

How a Collection (and anything under it) is named on the wire, and how the
three accepted forms resolve. The decision and the alternatives considered are
in [ADR-0014](../../docs/adr/0014-canonical-resource-names.md).

## The three shapes

| Shape | Form | Typical caller |
|---|---|---|
| **A — canonical** | `storageBackends/{b}/buckets/{bk}/tenants/{tid}/collections/{ok}` | stored rows, events, SDKs that normalise |
| **C — tenant-first** | `tenants/{tid}/collections/{ok}` | the console, most API calls |
| **B — bare** | `{ok}` | quick commands; needs a tenant default binding |

`{ok}` may contain `/`. `{tid}` is the tenant UUID.

The physical object key does not depend on the shape a request used: inside the
bucket it is `<tenant_id>/<collection>/<key>`
(`internal/storage/s3adapter/s3.go`). Presigned URLs sign that physical
`(bucket, key)`, so they work for every shape.

## Resolution

Every connectshim handler that needs the `(tenant, collection)` pair calls
`resolve.ResolveCollectionName` (`internal/api/connectshim/resolve`):

- **A** carries the backend and bucket; they are used as given.
- **C** carries the tenant; the backend and bucket are looked up from the
  Collection row when a handler needs them.
- **B** takes the tenant from the caller's credential and the backend and
  bucket from the tenant's default binding (`tenant_default_bindings`, set on
  the console's *Default Route* tab or with `TenantService.SetTenantDefaultBinding`).
  With no binding the call fails with `FailedPrecondition`, reason
  `NO_DEFAULT_BINDING`.

Each resolution increments `paladin_resource_name_shape_total{shape}`, the
measurement a later deprecation of a shape depends on.

## Where the canonical form appears

- **Route table.** `AuthService.WhoAmI` returns `routes`: one page of the
  Collections the caller can read, each with `canonical`, `tenant_path`,
  `backend` and `bucket`, plus `bare_alias` for Collections on the tenant's
  default route (the only ones a bare name resolves to). Clients normalise to A from this
  table instead of building names themselves.
- **Events.** Ingested storage events carry the canonical name
  (`internal/eventingest`).
- **Cedar.** With `cedar.canonical_collection_euid: true` (the default) the
  engine builds the A-shape entity UID when the backend and bucket are known.
  No shipped policy matches an entity UID literally, so the switch changes no
  decision; turn it off only if a policy pins a literal UID.

## Not implemented

Deprecating shape C on the wire. It is gated on the shape distribution above,
not on a date ([ADR-0014](../../docs/adr/0014-canonical-resource-names.md)).
