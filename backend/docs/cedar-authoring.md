# Cedar policy authoring

Paladin authorizes every RPC with [Cedar](https://www.cedarpolicy.com). This
page covers what a policy can refer to and how a policy reaches the engine.

The reference is [`policies/schema.cedarschema`](../policies/schema.cedarschema).
`internal/policy/cedar/schema_test.go` checks it against what the engine
emits — actions, entities, request context — and validates the built-in
policy, the default tenant policy and the examples against it.

---

## 1. Request

| Slot      | Value |
|-----------|-------|
| principal | Always a `User`, keyed by the token subject. API tokens and capabilities are `User`s too; `principal.kind` tells them apart. |
| action    | One of 46 actions (§3). |
| resource  | The most specific entity the request names: `Object`, `Collection`, `Bucket`, `StorageBackend`, `User` or `Tenant`. |
| context   | The same record for every action (§5). |

Handlers call `Engine.IsAuthorized`. A request is allowed when at least one
`permit` matches and no `forbid` does.

A policy that raises an evaluation error — reading an attribute the entity
does not have, for example — is skipped by Cedar. The engine then denies the
whole request and counts it, because a skipped `forbid` could otherwise turn
a deny into an allow. Guard optional reads with `has`.

## 2. Policy layers

Four layers are concatenated, in this order, and compiled together:

1. **Built-in** (`builtinPolicy` in `internal/policy/cedar/engine.go`), the
   same for every tenant:
   - `platform.admin` may do anything;
   - any principal may consent to an OAuth client (`AuthorizeOAuth`);
   - a member may read its own tenant (`ReadTenant`) and ensure its own
     storage (`EnsureTenantStorage`);
   - a machine principal (`kind` is `api_key`, `service_account` or
     `capability`) may delete and restore objects in its own tenant;
   - `platform.tenant-provisioner` may create tenants and their storage;
   - a principal with a non-empty scope set is confined to resources those
     scopes admit (a `forbid`).
2. **Tenant** — `tenants.inherited_cedar_policy`. A tenant created without one
   gets [`policies/examples/default.cedar`](../policies/examples/default.cedar),
   with `placeholder` replaced by the tenant's slug (its UUID when it has none).
3. **Bucket** — set with `BucketService.SetBucketPolicy`; applies to every
   request scoped to a collection bound to that bucket, from any tenant. It
   does not apply to requests on the bucket itself (`ManageBucket`,
   `Configure*`), so a bucket policy cannot block its own repair.
4. **Collection** — set with `CollectionService.SetCollectionPolicy`; applies
   to requests on that collection.

A `forbid` in any layer wins over every `permit`, so a tenant can narrow the
built-in grants but not widen past its own `forbid`s.

A stored layer that does not parse is replaced by a `forbid` on everything
except that layer's own repair (`ManageTenant`, `ConfigureBucketPolicy`,
`ManageCollection`), and the scope is reported as degraded in the log.

Compiled policies are cached per (tenant, collection). The cache is
invalidated through Postgres `LISTEN policy_changed`: a tenant or collection
change evicts that scope, a bucket change evicts every scope.
`cedar.policy_cache_ttl` (default `30s`) bounds how long a missed
notification can leave a stale policy in use.

## 3. Roles

Roles are strings in the token's `roles` claim, compared verbatim. There is no
role registry; a new role is a string a policy checks. The shipped policies
use:

| Role | Granted by | Reach |
|------|------------|-------|
| `platform.admin` | built-in | everything |
| `platform.tenant-provisioner` | built-in | create tenants, their buckets and collections |
| `tenant.admin` | default policy | users, quotas, audit, subscriptions, operations in its own tenant |
| `bucket.admin` | default policy | bucket configuration |
| `compliance.officer` | default policy | `ConfigureLock` |
| `secrets.rotator` | default policy | `RotateBackendCredentials` |
| `policy.author` | default policy | `InspectPolicy` |
| `collection:admin` | default policy | delete and restore objects |

`iam.admin` is checked in Go by the IAM handlers, not by Cedar.

## 4. Actions and resources

Each action applies to the resource types listed. An action on several types
is checked against the most specific one the request names — for example,
`ListCollections` checks `ManageCollection` against the `Tenant`.

| Actions | Resource |
|---------|----------|
| `PutObject` `GetObject` `DeleteObject` `RestoreObject` `UpdateObject` `CopyObject` | `Object`, `Collection` |
| `PresignPut` `PresignGet` `HeadObject` | `Object` |
| `SetObjectRetention` `SetObjectLegalHold` `ReadObjectLock` | `Object` |
| `ManageCollection` | `Collection`, `Tenant` |
| `BindCollectionToBucket` | `Collection` |
| `ManageBucket` `ConfigureBucketPolicy` `ConfigureLifecycle` `ConfigureLock` `ConfigureVersioning` `ConfigureReplication` | `Bucket` |
| `ReadBucket` | `Bucket`, `StorageBackend`, `Tenant` |
| `ManageBackend` `RotateBackendCredentials` | `StorageBackend` |
| `ReadBackend` | `StorageBackend`, `Tenant` |
| `ManageQuota` `ReadQuota` | `Tenant`, `Bucket` |
| `ManageTenant` `ReadTenant` `ResetQuotaUsage` `ReadAuditLog` `ExportAuditLog` `ManageSubscription` `ReadSubscription` `TestSubscription` `ReadOperation` `CancelOperation` `ReadBilling` `EnsureTenantStorage` `AuthorizeOAuth` | `Tenant` |
| `InspectPolicy` | `Tenant`, `Collection` |
| `ManageUser` `ResetPassword` `GrantScopes` `ManageUserSettings` | `User` |
| `ReadUser` `ReadUserSettings` | `User`, `Tenant` |

An object action reaches Cedar on the `Collection` when no key is known at
authorization time: batch operations (a batch copy checks `PutObject` on the
destination collection), listing, counting. `ConfigureLock` (the bucket's default lock) is separate
from `SetObjectRetention` (one object's lock), so each can be granted alone.

## 5. Attributes

### Resource entities

| Entity | Attributes |
|--------|------------|
| `Tenant` | `tenant_id`, `slug`, `display_name`, `labels`, `scope_keys`? |
| `StorageBackend` | `backend_id`, `scope_keys` |
| `Bucket` | `bucket_name`, `backend_id`, `owner_tenant_id` (empty for a shared bucket), `labels`, `scope_keys` |
| `Collection` | `collection`, `tenant_id`, `bucket_name`, `backend_id`, `scope_keys` |
| `Object` | `key`, `state`, `size_bytes`, `content_type`, `tenant_id`, `collection`, `bucket_name`, `backend_id`, `tags`, `tag_values`, `scope_keys` |
| `User` | `tenant_id`, `subject`, `tenant_slug`, `kind`, `roles`, `scopes`, `user_id`, `scope_keys`? |

`?` marks an attribute that is not always present; test it with `has`.

- `Bucket` has no `tenant_id`. A rule shared by tenant and bucket actions
  must guard `resource has tenant_id`.
- `Object.tags` is the set of tag names. `tag_values` maps name to value; the
  schema cannot declare it, so validation does not see it. Index it only after
  `has`: `resource.tag_values has "class" && resource.tag_values["class"] == "pii"`.
- `scope_keys` is the set of scope strings that admit the resource:
  `tenant:<uuid>`, `backend:<id>`, `bucket:<name>`,
  `collection:<bucket>/<collection>`. The built-in scope `forbid` intersects it
  with `principal.scopes`.
- The principal and a `User` resource share one type. The principal's
  `user_id` is empty; a resource user's `tenant_slug`, `kind`, `roles` and
  `scopes` are empty.

### Hierarchy

`Collection` is in its `Tenant` and, when the bucket is known, its `Bucket`;
`Object` is in its `Collection`; `Bucket` is in its `StorageBackend`. The
principal is in the resource's `Tenant` only when the token's tenant UUID
equals the resource's, so `principal in Tenant::"acme"` means the caller
belongs to `acme`.

### Entity UIDs

`Tenant::"<slug>"` (UUID when the tenant has no slug). `Collection::"<tenant
uuid>/<collection>"`, or with `cedar.canonical_collection_euid: true` and the
bucket known, `Collection::"storageBackends/<b>/buckets/<bucket>/tenants/<tenant
uuid>/collections/<collection>"` ([ADR-0014](../../docs/adr/0014-canonical-resource-names.md)).

Write conditions on attributes and parents, not on resource UID literals
(`resource == Collection::"…"`): the literal depends on that flag and on
whether the bucket was resolved.

## 6. Context

| Key | Type | Set for |
|-----|------|---------|
| `size_bytes` | Long | uploads: the declared size |
| `content_type` | String | uploads |
| `now` | Long | every request: Unix seconds |
| `ip` | String | nothing yet: always empty |
| `oauth_client_id` | String | `AuthorizeOAuth` |
| `oauth_scopes` | Set&lt;String&gt; | `AuthorizeOAuth` |

Keys that do not apply are zero-valued, never absent.

## 7. Patterns

### Tenant admin within its own tenant

```cedar
permit (
    principal,
    action in [Action::"ManageUser", Action::"ReadUser"],
    resource
) when {
    principal.roles.contains("tenant.admin") &&
    principal.tenant_id == resource.tenant_id
};
```

Without the `tenant_id` comparison, a tenant admin in one tenant would manage
users in every tenant.

### Upload limits

```cedar
forbid (principal, action in [Action::"PutObject", Action::"PresignPut"], resource)
when {
    context.size_bytes > 5368709120 ||
    (resource has key && resource.key like "*.exe")
};
```

### Maintenance window

```cedar
forbid (principal, action == Action::"ManageBackend", resource)
unless { context.now >= 1767225600 && context.now < 1767229200 };
```

### Hold on a tag

```cedar
forbid (principal, action == Action::"DeleteObject", resource)
when { resource has tags && resource.tags.contains("legal-hold") };
```

### Narrowing a scoped token

Scope confinement is built in (§2); a tenant policy does not need to repeat
it. To refuse scoped tokens an action outright:

```cedar
forbid (principal, action == Action::"ExportAuditLog", resource)
when { !principal.scopes.isEmpty() };
```

Cedar has no string concatenation, so a scope string cannot be assembled
inside a policy; compare against `resource.scope_keys` instead.

## 8. Common mistakes

- `principal.roles == "x"` — `roles` is a set; use `.contains("x")`.
- Reading `resource.key`, `resource.tags` or `resource.tenant_id` on an action
  that also applies to an entity without them (§4, §5) without `has`.
- `Tenant::"<uuid>"` for a tenant that has a slug — the UID is the slug.
- `principal == resource` to mean "the caller's own user" — the two UIDs never
  match; compare `principal.subject == resource.subject`.
- An empty tenant policy: everything outside the built-in grants is denied.

## 9. Workflow

1. Start from [`default.cedar`](../policies/examples/default.cedar) or another
   file in [`policies/examples/`](../policies/examples/).
2. `PolicyService.Validate` (requires `InspectPolicy`) compiles the text and
   type-checks it against the schema. A compile failure is an error. A schema
   finding — an unknown action, an attribute the entity lacks, a read that
   needs `has` — is a warning: the policy can still be saved, because the
   schema cannot declare `tag_values`.
3. `PolicyService.SimulateAuthz` evaluates a request against a policy without
   storing it; `PolicyService.GetEffectivePolicy` returns the merged layers for
   a tenant or collection.
4. Store it with `TenantService.SetInheritedPolicy` or
   `CollectionService.SetCollectionPolicy`.
5. Running replicas pick it up on the `policy_changed` notification.
