# API — Paladin

The RPC surface as the proto and the listeners define it. A change to the
contract changes this file in the same commit.

## Three planes, three ports, three audiences

Paladin is not one API. It is three Connect RPC surfaces, each on its own
listener, each accepting only tokens minted for it:

| plane | port | audience | package |
| --- | --- | --- | --- |
| data | 8080 | `paladin-data` | `paladin.data.v1` |
| iam | 8085 | `paladin-iam` | `paladin.iam.v1` |
| admin | 8090 | `paladin-admin` | `paladin.admin.v1` |

A binary run on the host with the config defaults serves data on 8083
(`#LocalDataPort` in `backend/internal/config/schema.cue`); the chart and the
compose config pin 8080.

The audience is enforced, not advisory: each plane's JWT verifier expects
that plane's audience, and the data and admin muxes are also wrapped with
`RequireAudience`, so a `paladin-data` token presented to the admin plane is
rejected before any handler runs. This is what stops a token handed to a
browser from reaching tenant administration.

- **Proto**: `proto/paladin/{admin,data,iam,common}/v1/`
- **Protocol**: Connect, Connect-Web and gRPC over the same endpoints
- **Handlers**: `backend/internal/api/{admin,data,iam}/v1/`, adapted to the
  generated Connect interfaces in `backend/internal/api/connectshim/`
- **Surface**: 159 RPCs across 28 services (inventory below)

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

The replay is cached per (tenant, method, key), together with a fingerprint
of the request. The same key with the same request replays; the same key with
a different request to that method is `InvalidArgument` — a key names one
request, not a session. Failures are not memoized — a call that errored can be
retried and succeed.

Two exceptions to the replay (`backend/internal/middleware/idempotency_skip.go`):
`AuthService` `Login`, `RefreshToken`, `ExchangeAudience` and `SwitchTenant`
are never memoized, whatever key is sent; and a repeated key on an RPC whose
response carries a credential (`CapabilityService.Issue`/`Delegate`,
`APITokenService.Create`, `UserService.ResetPassword`) answers
`AlreadyExists` instead of replaying, because the credential was delivered
once and never stored.

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

A bearer token on every call except `AuthService.Login`, `RefreshToken` and
`ExchangeAudience`, which carry their credential in the request, and the HTTP
health probes ([health.md](../../docs/health.md)). `HealthService` is an
authenticated RPC like any other.

```
Authorization: Bearer <jwt>
```

An API token (`paladin_pat_…`) goes in the same header, or in
`X-Paladin-API-Token` behind a proxy that strips `Authorization`; a capability
token goes in `X-Paladin-Capability`, or in `Authorization` under the
`capability` scheme (`DPoP` for a key-bound one).

Claims that matter: `aud` (must match the plane), `tenant` (the tenant the
request acts on), `roles` (compared verbatim; platform roles use dot form —
`platform.admin`, not `platform-admin`).

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

Every service and RPC in `proto/`, by plane.

**admin** (`:8090`, `paladin.admin.v1`)

| service | rpcs | methods |
| --- | --- | --- |
| `APITokenService` | 6 | `Create`, `Revoke`, `List`, `Get`, `GetSelf`, `GetUsage` |
| `AuditLogService` | 3 | `ListAuditLog`, `GetAuditLogEntry`, `ExportAuditLog` |
| `BackendService` | 10 | `CreateBackend`, `GetBackend`, `UpdateBackend`, `DeleteBackend`, `ListBackends`, `RotateCredentials`, `TestBackend`, `SetBackendEnabled`, `SetBackendReadOnly`, `SetBackendMaintenance` |
| `BillingService` | 4 | `GetTenantSummary`, `GetTenantTimeSeries`, `ListTenantSpend`, `GetBillingSettings` |
| `BucketService` | 14 | `CreateBucket`, `GetBucket`, `UpdateBucket`, `DeleteBucket`, `ListBuckets`, `SetBucketPolicy`, `SetLifecycleRules`, `SetObjectLock`, `SetVersioning`, `SetReplication`, `GrantBucket`, `RevokeBucketGrant`, `ListBucketGrants`, `ListAccessibleBuckets` |
| `CELService` | 1 | `Validate` |
| `CapabilityService` | 8 | `Issue`, `Delegate`, `Revoke`, `RevokeBiscuit`, `GetBiscuitUsage`, `Get`, `List`, `GetUsage` |
| `CollectionService` | 7 | `CreateCollection`, `GetCollection`, `UpdateCollection`, `DeleteCollection`, `ListCollections`, `SetCollectionPolicy`, `BindCollectionToBucket` |
| `EventSubscriptionService` | 7 | `CreateSubscription`, `GetSubscription`, `UpdateSubscription`, `DeleteSubscription`, `ListSubscriptions`, `TestSubscription`, `RedriveFailedDeliveries` — deliveries are CloudEvents 1.0 envelopes ([event-delivery-dedup.md](../../docs/event-delivery-dedup.md)) |
| `MCPInspectService` | 3 | `Inspect`, `ListSessions`, `GetBridgeStatus` |
| `OAuthClientService` | 1 | `DeleteOAuthClient` |
| `PlatformOperationService` | 4 | `GetOperation`, `ListOperations`, `CancelOperation`, `DismissOperations` |
| `PolicyService` | 3 | `Validate`, `SimulateAuthz`, `GetEffectivePolicy` |
| `QuotaService` | 3 | `GetQuota`, `SetQuota`, `ResetUsage` |
| `SystemService` | 4 | `GetConfig`, `GetDispatcherStats`, `GetPlatformStats`, `ListPlatformStatsTenants` |
| `TenantBudgetService` | 3 | `Get`, `Set`, `Summarize` |
| `TenantService` | 15 | `CreateTenant`, `GetTenant`, `UpdateTenant`, `DeleteTenant`, `ListTenants`, `SetInheritedPolicy`, `RestoreTenant`, `PurgeTenant`, `RenameTenantSlug`, `MigrateTenantStorageLayout`, `GetTenantStorageMigration`, `ResolveRenamedSlug`, `GetTenantDefaultBinding`, `SetTenantDefaultBinding`, `ClearTenantDefaultBinding` |

**data** (`:8080`, `paladin.data.v1`)

| service | rpcs | methods |
| --- | --- | --- |
| `BatchService` | 4 | `BatchDeleteObjects`, `BatchCopyObjects`, `BatchRestoreObjects`, `BatchUpdateTags` |
| `MultipartUploadService` | 5 | `InitiateMultipartUpload`, `PresignPart`, `CompleteMultipartUpload`, `AbortMultipartUpload`, `ListParts` |
| `ObjectService` | 18 | `UploadObject`, `DownloadObject`, `GetObject`, `LookupObject`, `UpdateObject`, `CompleteObject`, `DeleteObject`, `RestoreObject`, `CopyObject`, `ListObjects`, `CountObjects`, `ListObjectVersions`, `GetObjectVersion`, `RestoreObjectVersion`, `SetObjectRetention`, `SetObjectLegalHold`, `GetObjectLock`, `SetObjectTaint` |
| `ObjectTagService` | 4 | `GetObjectTags`, `PutObjectTags`, `DeleteObjectTags`, `ListDistinctTags` |
| `OperationService` | 4 | `GetOperation`, `ListOperations`, `CancelOperation`, `DismissOperations` |
| `PresignService` | 2 | `RegenerateUploadUrl`, `PresignDownload` |
| `StorageBootstrapService` | 1 | `EnsureTenantStorage` |

**iam** (`:8085`, `paladin.iam.v1`)

| service | rpcs | methods |
| --- | --- | --- |
| `AuthService` | 8 | `Login`, `RefreshToken`, `Revoke`, `WhoAmI`, `ChangePassword`, `ExchangeAudience`, `ListMyMemberships`, `SwitchTenant` |
| `HealthService` | 2 | `GetVersion`, `GetHealth` |
| `UserService` | 10 | `CreateUser`, `GetUser`, `UpdateUser`, `DeleteUser`, `ListUsers`, `GrantScopes`, `RevokeScopes`, `ResetPassword`, `ListOAuthGrants`, `RevokeOAuthGrant` |
| `UserSettingsService` | 5 | `GetMine`, `UpdateMine`, `GetForUser`, `ListByTenant`, `DeleteForUser` |

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

`tenants/{tenant_id}` on `TenantService` accepts a UUID or a slug. Under a
tenant (`tenants/{tenant_id}/collections/…`, `…/objects/{object}`) the
`{tenant_id}` is the UUID. Collection names on `CollectionService` also accept
the canonical and bare shapes; see
[canonical-resource-names.md](canonical-resource-names.md).

## List filters

The `filter` field on list RPCs is a CEL expression evaluated against each
row. Part of it is also
pushed into the SQL query so the filter selects from the table, not from
whichever page the cursor landed on. The pushed part only narrows the
candidate set: the full CEL program still runs over every fetched row, so a
predicate that is not pushed costs a wider scan, never a wrong answer. A page
whose rows all fail the filter can come back empty with a `next_page_token`;
keep paging.

`ListTenants`, `ListBuckets`, `ListBackends`, `ListCollections`, `ListUsers`
and `ListOperations` share one walker (`backend/internal/filter/cel/pushdown.go`).
It reads the top-level `&&` chain and pushes:

- string equality, either operand order, and `field in ["a", "b"]` — a
  value set, sent as `col = ANY($n::text[])`; an empty list is not pushed;
- a `||` of such equalities or `in` lists on the **same** field, as the union
  of their values;
- `field != "x"`, over `coalesce(col, '')`;
- booleans: `field == true`, `!field`, a bare `field`;
- `field.startsWith("x")` and `field.contains("x")`;
- `created_at >= timestamp(…)` / `<=`; strict `>` and `<` are widened to the
  inclusive bound, and the CEL pass drops the boundary row.

Not pushed, evaluated only in memory: a `||` across fields or over anything
but equalities, `labels[…]`, other functions, and `updated_at`. A filter that
does not parse pushes nothing. Objects and the audit log have their own
walkers (`objectpushdown.go`, `auditpushdown.go`) under the same contract.

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
retrying a create needs to tell "it exists" from "something else is wrong".

Within a code, the reason says which condition it was: an error mapped from a
domain condition carries a `google.rpc.ErrorInfo` with `domain` `paladin` and
`reason` set to a `paladin.common.v1.ErrorReason` value name
(`proto/paladin/common/v1/error_reason.proto`) — `VERSION_CONFLICT`,
`COLLECTION_NOT_EMPTY`, `BUCKET_NOT_FOUND`, the `CAPABILITY_*` refusals.
Branch on the reason, never on the message; a reason a client does not know
counts as the code alone. Some reasons carry `ErrorInfo.metadata`:
`ERROR_REASON_COLLECTION_NOT_EMPTY` (`failed_precondition`, from
`DeleteCollection`) says what holds the collection, as base-10 integers under
`live_objects` and `trashed_objects` — a trashed object holds it as much as a
live one.

## Pagination

List RPCs take a `paladin.common.v1.PageRequest`: `page_size` from 0 to 1000,
0 meaning the server default, and each RPC caps it at its own maximum;
`page_token` is the opaque `next_page_token` of the previous response. An
empty `next_page_token` is the last page. `total_size` is set only where an
RPC documents it.

## Validation

Every message is validated against `buf.validate` annotations by an
interceptor, before the handler. Violations are `InvalidArgument` with the
offending field named.

## Compatibility

The wire contract is pinned to a published baseline tag (`API_BASELINE_TAG` in
`backend/scripts/proto-breaking.sh`, `api/v0.20.0` at time of writing) and
`buf breaking` runs against it in `verify-all`. Pre-1.0
the project still breaks compatibility deliberately — see
[docs/upgrading.md](../../docs/upgrading.md), which records every such change
and the procedure for making one. What is not possible is breaking it by
accident.

If you pin anything, pin a tag. `api/latest` moves.
