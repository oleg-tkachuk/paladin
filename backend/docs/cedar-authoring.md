# Cedar policy authoring guide

PALADIN authorizes every RPC against a tenant-scoped Cedar policy. This guide
walks operators through the policy surface — entities, actions, attributes,
context — and shows the patterns that come up in real deployments.

The canonical schema is [`policies/schema.cedarschema`](../policies/schema.cedarschema).
This doc explains the **why**; the schema is the **what**.

---

## 1. Mental model

A Cedar decision answers: **may `principal` perform `action` on `resource`,
under `context`?** PALADIN fills the four slots like this:

| Slot       | Source                                                      |
|------------|-------------------------------------------------------------|
| principal  | The authenticated `User` (JWT subject + tenant + roles + scopes). |
| action     | One of ~30 named actions (see §3).                          |
| resource   | The thing being acted on — Object, Bucket, Tenant, …        |
| context    | Per-request attributes — size, content type, time, IP.      |

Every handler builds these from the request and calls `engine.IsAuthorized`.
A `permit` rule must match AND no `forbid` rule may match — otherwise the
RPC returns `PermissionDenied`.

**Default is deny.** A tenant with an empty `inherited_cedar_policy` cannot
do anything. The default policy seeded at tenant creation
([defaultpolicy.go](../internal/api/v1/tenant/defaultpolicy.go)) gives
tenant members read/write on their own objects and admins full reach.

---

## 2. The six default roles

Roles are dot-form strings carried in the JWT `roles` claim. The defaults
that ship in the example policies:

- `platform.admin` — cross-tenant operator. Full reach.
- `tenant.admin` — full reach **within** their tenant. Cedar enforces
  isolation via `principal.tenant_id == resource.tenant_id`.
- `iam.admin` — manages users and api-keys (subset of tenant-admin).
- `bucket.admin` — bucket lifecycle, versioning, replication.
- `compliance.officer` — narrow: ConfigureLock only (object-lock for
  regulatory holds).
- `secrets.rotator` — narrow: RotateBackendCredentials only (so a
  rotation service can run without ManageBackend rights).
- `policy.author` — InspectPolicy / SimulateAuthz (admin-UI roles).

Custom roles work the same way — pick a dot-form name, mint tokens with
that role, write a permit referencing it. There's no role registry —
strings are compared verbatim by Cedar.

---

## 3. Action reference

Actions are grouped by the resource type they apply to. **Always specify
the action by name** — Cedar has no wildcards by design.

### Object (data plane)

```
PutObject       PresignPut       GetObject       PresignGet
HeadObject      DeleteObject     RestoreObject   UpdateObject
CopyObject
```

Each of these takes an `Object` resource and exposes context attributes
`size_bytes`, `content_type`, `now`, `ip` for matching.

### ObjectKey / Bucket (admin plane)

```
ManageObjectKey       BindObjectKeyToBucket
ManageBucket          ReadBucket
ConfigureBucketPolicy ConfigureLifecycle      ConfigureLock
ConfigureVersioning   ConfigureReplication
```

`ConfigureLock` is split out so a `compliance.officer` can hold-only
without granting ConfigureReplication (data-residency risk).

### Tenant / IAM

```
ManageTenant     ReadTenant
ManageUser       ReadUser           ResetPassword     GrantScopes
ManageApiKey     ReadApiKey         RotateApiKey      MintScopedToken
ManageQuota      ReadQuota          ResetQuotaUsage
ReadAuditLog     ExportAuditLog
ManageSubscription ReadSubscription TestSubscription
ReadOperation    CancelOperation
ReadUserSettings ManageUserSettings
```

### Backend / introspection

```
ManageBackend          ReadBackend
RotateBackendCredentials
InspectPolicy
```

`InspectPolicy` gates `ValidatePolicy` / `SimulateAuthz` /
`GetEffectivePolicy` — these leak schema and policy text, so they aren't
open to any authenticated principal.

---

## 4. Resource attributes

Every resource entity exposes attributes that policies can match on. The
ones that come up often:

### `Tenant`

- `tenant_id: String` — UUID form, always present.
- `slug: String` — kebab-case handle (e.g. `acme`). The Tenant entity's
  Cedar UID is keyed on the slug when set, so operators read
  `Tenant::"acme"` instead of `Tenant::"550e8400-…"`.
- `display_name`, `labels` — present, currently zero-valued in v1
  (placeholder for future labels-based policies).

### `Object`

- `key: String` — S3 key.
- `state: String` — `PENDING` | `AVAILABLE` | `FAILED` | `DELETED`.
- `size_bytes: Long`, `content_type: String`.
- `tenant_id`, `object_key`, `bucket_name`, `backend_id` — anchor strings.
- `tags: Set<String>` — set of tag keys (values not exposed; intentional
  to keep policies portable across tenants).

### `User` (principal AND resource side)

The principal-User and resource-User share a type but use different UID
families so policies can write `principal != resource`.

Principal side:
- `subject: String` — JWT sub.
- `tenant_id: String`, `tenant_slug: String`.
- `roles: Set<String>`, `scopes: Set<String>`.

Resource side (the user being managed):
- `user_id: String`, `subject: String`, `tenant_id: String`.

### `ApiKey`

- `api_key_id: String`, `tenant_id: String`.

### `Bucket`

- `bucket_name`, `backend_id`, `owner_tenant_id` (empty = shared).
- `labels: Set<String>`.

---

## 5. Context attributes

Cedar `context` is the per-request slot:

- `size_bytes: Long` — for size caps.
- `content_type: String` — for MIME-type allowlists.
- `now: Long` — Unix seconds; gate by time-of-day or maintenance windows.
- `ip: String` — source IP (when the data plane is behind a trusted proxy).

```cedar
permit (principal, action == Action::"PutObject", resource)
when {
  context.size_bytes <= 5368709120 &&         // ≤ 5 GiB
  !(resource.key like "*.exe")
};
```

---

## 6. Patterns

### Self vs. cross-user

```cedar
permit (
    principal,
    action in [Action::"ReadUserSettings", Action::"ManageUserSettings"],
    resource
) when {
    principal == resource ||
    principal.roles.contains("platform.admin") ||
    (principal.roles.contains("tenant.admin") &&
     principal.tenant_id == resource.tenant_id)
};
```

### Tenant isolation for tenant-admins

```cedar
permit (
    principal,
    action in [Action::"ManageUser", Action::"ReadUser"],
    resource
) when {
    principal.roles.contains("platform.admin") ||
    (principal.roles.contains("tenant.admin") &&
     principal.tenant_id == resource.tenant_id)
};
```

The `tenant_id == resource.tenant_id` check is the load-bearing piece —
without it, a tenant.admin in tenant A could manage users in tenant B.

### Time-bounded permit (maintenance window)

```cedar
permit (principal, action == Action::"ManageBackend", resource)
when {
  principal.roles.contains("platform.admin") &&
  context.now >= 1700000000 &&
  context.now <= 1700003600
};
```

### Scope-gated delegation

When a service mints a scoped token (`MintScopedToken`), the resulting
JWT carries a `scopes` claim. Cedar exposes it as `principal.scopes`:

```cedar
permit (principal, action == Action::"GetObject", resource)
when {
  principal.scopes.contains("objects:read:" +
                            resource.tenant_id + "/" +
                            resource.object_key + "/*")
};
```

Mind the prefix shape — wire-form scopes are
`<resource>:<verb>:<tenant_id>/<object_key>/<key>`. Wildcards on the key
slot are matched verbatim by Cedar's `like`-free `String.contains` — this
is intentional (Cedar refuses regex by design).

### Forbid trumps permit

```cedar
forbid (principal, action == Action::"DeleteObject", resource)
when {
  resource.tags.contains("legal-hold")
};
```

A single matching `forbid` denies regardless of how many `permit`s match.
Use this for hard guards that must not be subject to permit-bloat
("operator stacks N permits trying to grant DeleteObject; forbid catches
the regression").

---

## 7. Common mistakes

- **String comparison on roles by mistake.** `principal.roles == "x"` is
  a type error — `roles` is `Set<String>`. Use
  `principal.roles.contains("x")`.

- **Forgetting the tenant guard.** `permit (principal, action ==
  Action::"ReadUser", resource)` with no when-clause grants cross-tenant.
  Add `principal.tenant_id == resource.tenant_id`.

- **Using the UUID when the slug is configured.** If your tenant has a
  slug, the Tenant UID is `Tenant::"acme"`. `Tenant::"550e8400-…"` won't
  match. Read the slug first via `GetTenant` if unsure.

- **Quoting Cedar identifiers.** Action names need quotes
  (`Action::"PutObject"`). Entity types do not (`User in [Tenant]`).

- **Empty default policy.** A tenant with no policy is deny-all. The
  default seeded at tenant creation is the floor — start there, narrow
  later.

---

## 8. Authoring workflow

1. Read the schema: `policies/schema.cedarschema`.
2. Copy an example: `policies/examples/default.cedar`.
3. Edit the policy text. Run validation client-side via `ValidatePolicy`
   RPC (gated by `InspectPolicy`).
4. Simulate before committing via `SimulateAuthz` — it returns the
   decision + the matching rules without persisting anything.
5. Commit via `UpdateTenant` (tenant-inherited policy) or
   `UpdateObjectKey` (per-objectKey overlay).

The engine compiles policies on first use and caches the result for 30
seconds; an update is observed by all workers within ~30s of the write
landing in `tenants.inherited_cedar_policy`.
