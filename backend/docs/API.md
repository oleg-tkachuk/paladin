# API — Paladin

Everything here was checked against the tree it describes. The previous
version of this file was not, and by the time it was rewritten every
structural claim in it was false: it named a proto package that does not
exist, one port where there are three, a handler directory that had been
renamed, and an "admin key" auth scheme the server has never implemented. It
also said nothing about the two headers without which no `Create` call
succeeds — a gap that broke three of this project's OWN clients before it
broke anyone else's.

So: if you change the contract, change this file in the same commit. It is the
first thing an integrator reads, and a wrong map is worse than no map.

## Three planes, three ports, three audiences

Paladin is not one API. It is three Connect RPC surfaces, each on its own
listener, each accepting only tokens minted for it:

| plane | port | audience | package |
| --- | --- | --- | --- |
| data | 8080 | `paladin-data` | `paladin.data.v1` |
| iam | 8085 | `paladin-iam` | `paladin.iam.v1` |
| admin | 8090 | `paladin-admin` | `paladin.admin.v1` |

The audience is enforced, not advisory: each mux is wrapped with
`RequireAudience`, so a `paladin-data` token presented to the admin plane is
rejected before any handler runs. This is what stops a token handed to a
browser from reaching tenant administration.

- **Proto**: `backend/proto/paladin/{admin,data,iam,common}/v1/`
- **Protocol**: Connect, Connect-Web and gRPC over the same endpoints
- **Handlers**: `backend/internal/api/connectshim/`
- **Surface**: 142 RPCs across 27 services

## Two headers you cannot skip

These are where first integrations fail, so they come before the service list.

### `Idempotency-Key` on every `Create*` and `Issue*`

Any RPC whose method name's last segment starts with `Create` or `Issue`
**requires** an `Idempotency-Key` header. Missing it is `InvalidArgument`,
returned before the handler runs — not a warning, not a default.

Derive the key from the identity of the thing being created, not from a fresh
UUID per attempt. A random key on every retry re-enters the handler and leans
on the create colliding; a stable key replays the first response, which is
what idempotent means.

```
Idempotency-Key: my-service-tenant-<tenant-id>
```

The replay is cached per (tenant, method, key). Failures are not memoized — a
call that errored can be retried and succeed.

### `resource_version` on updates and deletes

Mutations are optimistic-concurrency guarded. Read the resource, send back the
`resource_version` you read, and a concurrent write makes yours fail with
`Aborted` rather than silently winning. There is deliberately no bypass on
`UpdateTenant` or `UpdateBackend`; `DeleteBucket` and `DeleteCollection` offer
`skip_version_check` for operator tooling that has no prior read.

A `FieldMask` on the wire is a comma-separated string of camelCase names
(`"displayName,endpoint"`), not the `{paths: []}` object it is in code — the
canonical protojson form. Some sub-messages are mask-gated as whole groups:
naming `events` writes every field of it from your message, so send the group
you read back, or omit the path entirely.

## Authentication

A bearer JWT on every call except `AuthService.Login` and the health probes:

```
Authorization: Bearer <jwt>
```

Claims that matter: `aud` (must match the plane), `tenant` (the tenant the
request acts on), `roles` (dot form — `platform.admin`, not `platform-admin`).

Three ways to get one:

- **`AuthService.Login`** (iam plane) — subject + password, returns an access
  and a refresh token. `requested_audience` selects the plane; omitted means
  `paladin-data`. `X-Tenant-Id` is a hint for a subject registered in several
  tenants, and only a hint — the password still gates the request and the
  JWT's `tenant` claim is what every other RPC trusts.
- **API tokens** (`APITokenService`, admin plane) — long-lived machine
  credentials, the right choice for a service integrating with Paladin.
- **Capability tokens** (`CapabilityService`) — narrow, short-lived grants for
  a specific action on a specific resource.

Authorization is Cedar policy plus role checks, evaluated per RPC. A tenant
carries an inherited policy; buckets and collections can layer their own.

## Services

**admin** (`:8090`) — APIToken, AuditLog, Billing, Backend, Bucket, CEL,
Capability, Collection, PlatformOperation, EventSubscription, Policy,
MCPInspect, Quota, TenantBudget, Tenant, System.

**data** (`:8080`) — Object, ObjectTag, Batch, MultipartUpload, Presign,
StorageBootstrap, Operation.

**iam** (`:8085`) — Auth, User, UserSettings, Health.

Paladin never proxies object bytes. Uploads and downloads are presigned URLs
the client uses directly against the storage backend; the data plane issues
them and records the result.

## Resource names

AIP-style, and they are the identifiers — not a display convenience:

```
tenants/{tenant_id}
storageBackends/{backend_id}
storageBackends/{backend_id}/buckets/{bucket_id}
tenants/{tenant_id}/collections/{collection}
```

`{tenant_id}` accepts a UUID or a slug.

## Errors

Connect codes, mapped centrally (ADR-0002) so the same condition answers the
same way everywhere. The ones worth branching on:

| code | means |
| --- | --- |
| `already_exists` | the resource is there; a create can stop retrying |
| `failed_precondition` | something else is unmet — a referenced row is missing, or the resource is held by another |
| `aborted` | `resource_version` was stale; re-read and retry |
| `resource_exhausted` | a quota or capability budget rejected it |
| `unauthenticated` | no token, wrong audience, or bad credentials |

`already_exists` and `failed_precondition` are deliberately distinct: a caller
retrying a create needs to tell "it exists" from "something else is wrong",
and the code is the only part of the answer it can branch on.

## Validation

Every message is validated against `buf.validate` annotations by an
interceptor, before the handler. Violations are `InvalidArgument` with the
offending field named.

## Compatibility

The wire contract is pinned to a published baseline tag (`api/v0.5.0` at time
of writing) and `buf breaking` runs against it in `verify-all`. Pre-1.0
the project still breaks compatibility deliberately — see
[docs/upgrading.md](../../docs/upgrading.md), which records every such change
and the procedure for making one. What is not possible is breaking it by
accident.

If you pin anything, pin a tag. `api/latest` moves.
