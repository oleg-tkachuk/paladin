# Canonical Resource Names — A+B+C Plan

Status: accepted — ratified by [ADR-0014](../../docs/adr/0014-canonical-resource-names.md) (2026-07-01)
Owner: see ADR-0014
Last updated: 2026-07-01

> This doc is the detailed phase-by-phase reference; ADR-0014 is the
> authoritative decision + records two constraints found during ratification
> (canonical Cedar EUID needs backend+bucket threading; Phase 5 soft-deprecation
> is data-gated on ≥1 week of shape-distribution metric, not a deploy window).
> Phase 2 (central resolver) is DONE; Phases 1/3/4/5 remain.

## Goal

Unify three resource-name shapes for Collection-rooted resources behind
a single canonical form, with two ergonomic aliases that resolve to it
on the API edge.

| Shape | Example | Where |
|---|---|---|
| **A — canonical** | `storageBackends/{b}/buckets/{bk}/tenants/{tid}/collections/{ok}` | DB rows, audit log, event payloads, Cedar `resource ==` checks |
| **C — tenant-first alias** | `tenants/{tid}/collections/{ok}` | Operator UI, admin console, current API surface |
| **B — bare alias** | `collections/{ok}` or `{ok}` | CLI/SDK quick commands; uses `tenant_default_bindings` |

Internal storage layer is unchanged: physical S3 key remains
`<bucket>/<tenant_id>/<collection_path>/<user_key>` regardless of which
shape the request came in as. S3 presign keeps working for all three
because presign signs the physical `(bucket, key)`, not the API name.

## Invariants the plan must preserve

1. **One canonical form per resource.** All persisted refs (audit, RV,
   pagination tokens, idempotency keys, Cedar resource literals, event
   `resource_name`) MUST be canonical (A). Aliases never persist.
2. **Resolve before authorize.** Cedar evaluator sees only canonical.
   Resolution from B/C → A happens *before* the policy gate, never
   after.
3. **No mixed shapes.** Reject inputs that look like a hybrid (e.g.
   `storageBackends/x/buckets/y/collections/{ok}` without `tenants/...`
   in the middle). Such a 4th shape is where security bugs spawn.
4. **JWT-derived `tenant_id` is authoritative.** When an alias supplies
   a tenant scope (C carries it; B implies it from auth), the resolved
   canonical's `tid` MUST equal the JWT's `tenant_id`. Mismatch → 403.

## Phases

### Phase 1 — Canonical (A) everywhere internally

**Scope:** make A the single source of truth without yet exposing it.
Existing C-shaped API stays the public contract.

Changes:

- `backend/internal/api/v1/collection/`: domain layer learns to emit
  and parse canonical form. Add `Collection.CanonicalName()` helper.
- `backend/internal/audit/`: switch `resource_name` column writes to
  canonical. Backfill migration over existing rows (deterministic
  join on `collections.tenant_id + bucket_id`).
- `backend/internal/cedar/`: resource literals in policy templates
  switch to canonical; existing tenant-scoped policies regenerated.
  Cedar policy authoring docs updated.
- `backend/internal/eventingest/` + `backend/internal/eventbus/`:
  outbound event payload's `resource_name` field is canonical.
  Subscribers see new shape — breaking change, called out in changelog.
- DB migration `031_resource_name_canonical_backfill.sql`: rewrite
  `audit_log.resource_name`, `event_deliveries.resource_name`. No
  schema change; UPDATE + index rebuild.

Acceptance:
- `rtk go test ./...` green.
- New unit tests in `internal/api/v1/collection/canonical_test.go`
  covering all three shapes round-trip.
- Audit log for any resource event after deploy contains canonical.
- `cedarc lint` (existing CI step) passes against regenerated
  policy templates.
- No public API breakage: C-shaped `tenants/{tid}/collections/{ok}` in
  every existing RPC still works exactly as before.

Out of scope: any new RPC shape, any new alias.

### Phase 2 — C alias resolver on edge

**Scope:** introduce centralized resolver, refactor every `*_server.go`
in `connectshim/admin/` and `connectshim/data/` to call it. Behavior
unchanged from the client's perspective; internal plumbing now goes
through one entrypoint.

Changes:

- New file `backend/internal/api/connectshim/resolve/resolver.go`:
  ```go
  type CanonicalRef struct {
      Backend, Bucket string
      TenantID        uuid.UUID
      Collection      string  // multi-segment path, no slashes escaped
  }
  func ResolveCollectionName(ctx, name string) (CanonicalRef, error)
  ```
  Detects A | C | rejects B (B comes in Phase 3).
- Every existing `objectKeyParts(name)` call site swaps to
  `ResolveCollectionName`. The split between `tenantUUIDFromParent`
  and `objectKeyParts` collapses — resolver handles both.
- `frontend/src/lib/paladin/names.ts` mirror: same shape detection, same
  invariants, used by SDK callers if any client-side normalization
  needed.
- Add input-shape metric `paladin_resource_name_shape_total{shape="…"}`
  so we can see real-world distribution before unlocking B.

Acceptance:
- All existing E2E tests green without modification.
- New table-driven test in `connectshim/resolve/resolver_test.go`
  with 30+ cases (valid A, valid C, malformed, cross-tenant attack,
  embedded slash in `ok`, max-length, etc.).
- Cross-tenant attack test: send C-shape with `tid` ≠ JWT's tenant →
  expect `PERMISSION_DENIED`, not `NOT_FOUND`.
- Metric scraping confirms `shape="A"` is 0% (no public client knows
  about A yet — only internal callers).

Out of scope: B alias, default-binding table, WhoAmI changes.

### Phase 3 — B alias + tenant_default_bindings

**Scope:** unlock bare-name (`collections/{ok}` or `{ok}`) for ergonomic
CLI/SDK use. Requires a per-tenant default route.

DB migration `032_tenant_default_bindings.sql`:
```sql
CREATE TABLE tenant_default_bindings (
    tenant_id   uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    backend_id  text NOT NULL,
    bucket_name text NOT NULL,
    set_at      timestamptz NOT NULL DEFAULT now(),
    set_by      text NOT NULL,
    FOREIGN KEY (backend_id, bucket_name)
        REFERENCES buckets(backend_id, bucket_name) ON DELETE RESTRICT
);
```

`ON DELETE RESTRICT` for the bucket FK prevents orphaning a default
binding via bucket deletion; admin must explicitly rebind first.

Proto additions (`tenant_service.proto`):
```
rpc GetTenantDefaultBinding(GetTenantDefaultBindingRequest) returns (TenantDefaultBinding);
rpc SetTenantDefaultBinding(SetTenantDefaultBindingRequest) returns (TenantDefaultBinding);
rpc ClearTenantDefaultBinding(ClearTenantDefaultBindingRequest) returns (google.protobuf.Empty);
```

Resolver extension: when input matches `collections/{ok}` or bare `{ok}`
shape, resolver pulls `tenant_id` from JWT, joins with
`tenant_default_bindings`, and emits canonical. If no default binding
exists → `FAILED_PRECONDITION` with structured detail
`reason: NO_DEFAULT_BINDING`.

UI: new tab under `/tenants/[id]/settings/default-binding` with
a single dropdown of `(backend, bucket)` pairs the tenant has
Collections on, and a "Clear" button.

Acceptance:
- Migration up/down clean on `task migrate-test`.
- Resolver test suite extended: B-shape with binding, B-shape without
  binding, B-shape after binding cleared mid-session.
- CLI smoke test: `paladin put invoices/q1/jan.pdf <(echo hi)` works
  after `paladin tenants set-default <backend> <bucket>`.
- Revoking the default binding does NOT break in-flight presigned
  URLs (they were issued against canonical, lifecycle-independent).
- Cedar gate test: B-shape with default binding pointing at bucket Y,
  but tenant has no `s3:PutObject` permission on Y → 403, not 5xx.

### Phase 4 — WhoAmI surfaces all three forms

**Scope:** clients learn their routes in one shot, never construct
canonical themselves.

`WhoAmIResponse` extension:
```protobuf
message CollectionRoute {
  string canonical    = 1;  // A
  string tenant_path  = 2;  // C
  string bare_alias   = 3;  // B (empty if no default binding)
  string backend      = 4;
  string bucket       = 5;
}
repeated CollectionRoute routes = N;
```

SDK behavior: after WhoAmI, the SDK has the full route table. Helper
methods accept any of the three; SDK normalizes to canonical before
sending. End user writes whichever shape feels natural; wire format
to the server is canonical (A) once SDK adoption rolls out.

Acceptance:
- WhoAmI returns ≥1 route per Collection the caller can read.
- TS SDK test: `paladin.objectKey("invoices/q1").presignPut(...)` works
  after a single `whoAmI()` call.
- Stale-cache scenario: admin rebinds an Collection to a different
  bucket; client cache TTL = 60s; after expiry next call uses new
  route without restart.

### Phase 5 — Soft deprecation of C on the wire (optional, distant)

Once SDK adoption is broad and metrics show `shape="A"` dominates,
emit a deprecation header on C-shaped requests. Never drop C from the
server — operator UI relies on it indefinitely. This phase exists only
to nudge SDK upgrades.

## Cross-cutting

### Cedar policy stack

All three layers (tenant, bucket, objectKey) keep working. Policy
authors write tenant-scoped policies in C-shape (familiar); compiler
expands to canonical at policy install time. See
`backend/docs/cedar-authoring.md` Section "Resource literals" for
the rewrite rule we'll add.

### Budget / Quotas

Both index by `(tenant_id, collection_id)` foreign keys, not by
resource name string. Untouched by this work.

### M2M tokens

Audience and tenant claims unchanged. Token *scopes* may reference
Collections; switch scope storage to canonical (Phase 1 backfill
covers this).

### Event subscriptions

Subscribers filter by `resource_name` prefix today. After Phase 1
backfill, prefix filters change shape. Migration plan: emit BOTH old
(C) and new (A) `resource_name` fields on outbound events for one
release; subscribers update filters; remove old field one release
later. Tracked as a separate BACKLOG entry.

### MCP bridge

Tool argument schemas stay in C-shape — that's what LLMs find
natural. Bridge layer translates to canonical via the same resolver
on its way into the API.

### Eventingest path parsing (already in BACKLOG)

The longest-prefix-match work for parsing `<bucket>/<tid>/<okPath>/<userKey>`
back into components becomes simpler under canonical: events arrive
with explicit `(backend, bucket, tenant_id, collection)` from S3
notification config, no parsing needed. Closes that BACKLOG item as
a side effect of Phase 1.

## Phase 0 — Tenant identity hardening (prerequisite)

**Scope:** before any of the canonical-name work lands, lock down tenant
identity so that canonical refs never silently break. Three fields:
`tenant_id` (UUID, immutable), `slug` (text, immutable, unique, required
at creation), `display_name` (text, editable, unique, defaults to `slug`).

### Current state

- `tenant_id` — UUID PK, naturally immutable.
- `slug` — already `NOT NULL UNIQUE` with kebab-case CHECK
  (migration 009). **But** API allows creating a tenant without a slug
  (auto-`t-<hex>` backfill leaks into create path), and there is no
  DB-level prevention of `UPDATE tenants SET slug = …`. Slug rename is
  BACKLOG'd, so for now slug must be hard-immutable.
- `display_name` — nullable TEXT, no UNIQUE, no default from slug.

### Target state

| Field | Required at create | Editable | Unique | Format |
|---|---|---|---|---|
| `tenant_id` | optional, client may supply UUID; else server generates | no | yes (PK) | RFC 4122 uuid, any version |
| `slug` | yes, client-supplied | no | yes | `^[a-z]([a-z0-9-]{1,61}[a-z0-9])?$` |
| `display_name` | no (defaults to slug) | yes | yes | TEXT 1–255 chars, no leading/trailing whitespace |

### Changes

DB migration `029a_tenant_identity_hardening.sql` (slot before 030 in a
fresh branch — **for already-deployed envs use a new number**, e.g.
`033_tenant_identity_hardening.sql`):

```sql
-- +goose Up
-- +goose StatementBegin

-- 1. display_name: backfill NULLs with slug, enforce NOT NULL + UNIQUE.
UPDATE tenants
   SET display_name = slug
 WHERE display_name IS NULL OR btrim(display_name) = '';

ALTER TABLE tenants
    ALTER COLUMN display_name SET NOT NULL,
    ADD CONSTRAINT tenants_display_name_format
        CHECK (char_length(display_name) BETWEEN 1 AND 255
               AND display_name = btrim(display_name)),
    ADD CONSTRAINT tenants_display_name_unique UNIQUE (display_name);

-- 2. Slug + tenant_id immutability via trigger (defense in depth;
--    API layer also rejects, but DB is the last line).
CREATE OR REPLACE FUNCTION tenants_block_immutable_columns()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id THEN
        RAISE EXCEPTION 'tenant_id is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.slug IS DISTINCT FROM OLD.slug THEN
        RAISE EXCEPTION 'slug is immutable; use the slug-rename RPC'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tenants_immutable_columns
    BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION tenants_block_immutable_columns();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS tenants_immutable_columns ON tenants;
DROP FUNCTION IF EXISTS tenants_block_immutable_columns();
ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_display_name_unique,
    DROP CONSTRAINT IF EXISTS tenants_display_name_format,
    ALTER COLUMN display_name DROP NOT NULL;
-- +goose StatementEnd
```

Proto changes (`tenant_service.proto`):

- `CreateTenantRequest`: `tenant_id` stays optional (string, validated
  as UUID when non-empty); `slug` becomes
  `[(buf.validate.field).required = true]`; `display_name` stays
  optional.
- `UpdateTenantRequest`: drop `slug` from any update mask the server
  honors. If client sends `slug` in the field mask → return
  `INVALID_ARGUMENT` with `reason: SLUG_IMMUTABLE`. Same for
  `tenant_id` (already not in any mask, formalize the rejection).

API handler changes (`backend/internal/api/v1/tenant/`):

```go
func (h *Handler) CreateTenant(ctx, args CreateTenantArgs) (*Tenant, error) {
    // tenant_id: client-supplied or server-generated.
    var tid uuid.UUID
    if args.TenantID == "" {
        tid = uuid.Must(uuid.NewRandom())   // v4
    } else {
        parsed, err := uuid.Parse(args.TenantID)
        if err != nil {
            return nil, status.Error(codes.InvalidArgument,
                "tenant_id must be a valid UUID")
        }
        if parsed == uuid.Nil {
            return nil, status.Error(codes.InvalidArgument,
                "tenant_id must not be the zero UUID")
        }
        tid = parsed
    }

    if args.Slug == "" {
        return nil, status.Error(codes.InvalidArgument, "slug is required")
    }
    if !slugRE.MatchString(args.Slug) {
        return nil, status.Error(codes.InvalidArgument, "slug format invalid")
    }
    if args.DisplayName == "" {
        args.DisplayName = args.Slug   // default
    }
    // INSERT … RETURNING. pg 23505 mapped by constraint name:
    //   tenants_pkey                    → ALREADY_EXISTS field=tenant_id
    //   tenants_slug_unique             → ALREADY_EXISTS field=slug
    //   tenants_display_name_unique     → ALREADY_EXISTS field=display_name
}

func (h *Handler) UpdateTenant(ctx, args UpdateTenantArgs) (*Tenant, error) {
    if slices.Contains(args.Mask, "slug") {
        return nil, status.Error(codes.InvalidArgument, "slug is immutable")
    }
    if slices.Contains(args.Mask, "tenant_id") {
        return nil, status.Error(codes.InvalidArgument, "tenant_id is immutable")
    }
    // display_name is editable, validated against UNIQUE.
}
```

Frontend changes:

- `frontend/src/app/tenants/new/page.tsx` (create form):
  - `slug` input: required, live-validated against the regex,
    placeholder "platform", helper "kebab-case, immutable after create".
  - `display_name` input: optional, placeholder "Defaults to slug",
    helper "Unique. Editable later."
- `frontend/src/app/tenants/[id]/page.tsx` (overview):
  - `slug` rendered as read-only with a lock icon + tooltip
    "Immutable. Use slug-rename RPC (BACKLOG) when available."
  - `display_name` rendered as editable inline field with
    optimistic update + 409 handling on collision.

### Acceptance

- Create with no `tenant_id` → server returns row with a fresh v4 UUID.
- Create with client-supplied valid UUID → row uses that exact UUID.
- Create with malformed `tenant_id` (e.g. `"abc"`) → `INVALID_ARGUMENT`.
- Create with `tenant_id = "00000000-0000-0000-0000-000000000000"` →
  `INVALID_ARGUMENT` (zero UUID is reserved as `uuid.Nil` sentinel).
- Create with duplicate `tenant_id` → `ALREADY_EXISTS` field=`tenant_id`.
- Create with empty slug → `INVALID_ARGUMENT`.
- Create with empty `display_name` → row has `display_name = slug`.
- Create with duplicate slug → `ALREADY_EXISTS` with `field: slug`.
- Create with duplicate `display_name` → `ALREADY_EXISTS` with
  `field: display_name`.
- Update with `slug` in mask → `INVALID_ARGUMENT` at API; if anyone
  bypasses API and runs raw SQL, trigger raises.
- Update with `display_name` change → succeeds; collision returns
  `ALREADY_EXISTS`.
- Existing rows post-migration: every tenant has non-null
  `display_name`; rows that had NULL get `display_name = slug`.
- DB trigger test: `psql -c "UPDATE tenants SET slug='x' WHERE …"` →
  raises with the expected message.

### Why a separate phase

`canonical-resource-names` work uses `tenant_id` (UUID) in the
canonical form, not slug — so canonical isn't *functionally* dependent
on slug immutability. But the C-shape alias `tenants/{tid}/...` and
the human-pasteable `tenants/{slug}/...` URL convention both rely on
the operator's mental model that "tenant identity is fixed". If slug
silently mutates while canonical refs accumulate in audit log and
event payloads, operators lose the ability to map an audit row back
to a current tenant. Hardening identity *before* canonical lands
keeps that mapping clean from day one.

The slug-rename RPC remains BACKLOG'd. When it ships, it will be the
*only* way to change slug, will write a row to `tenant_slug_history`,
and will be exempt from the immutability trigger via session-level
`SET LOCAL paladin.allow_slug_rename = on` that the trigger checks.

## File-level change inventory (rough)

Phase 0 (~8 files):
- `backend/migrations/033_tenant_identity_hardening.sql` (new)
- `proto/paladin/admin/v1/tenant_service.proto` (validate rules)
- `backend/internal/api/v1/tenant/handler.go` (create + update guards,
  default display_name = slug, pg 23505 mapping)
- `backend/internal/api/v1/tenant/handler_test.go`
- `frontend/src/app/tenants/new/page.tsx` (required slug, optional dn)
- `frontend/src/app/tenants/[id]/page.tsx` (read-only slug, editable dn)
- `frontend/src/lib/paladin/tenants.ts` (form schema, 409 mapping)
- regenerated proto/sqlc

Phase 1 (~15 files):
- `backend/internal/api/v1/collection/canonical.go` (new)
- `backend/internal/api/v1/collection/canonical_test.go` (new)
- `backend/internal/audit/recorder.go`
- `backend/internal/cedar/templates/*.cedar`
- `backend/internal/eventbus/publisher.go`
- `backend/migrations/031_resource_name_canonical_backfill.sql` (new)
- regenerated proto/sqlc artifacts

Phase 2 (~10 files):
- `backend/internal/api/connectshim/resolve/resolver.go` (new)
- `backend/internal/api/connectshim/resolve/resolver_test.go` (new)
- every `*_server.go` under `connectshim/admin/` and `connectshim/data/`
  that calls `objectKeyParts` (≈8 files) — swap call sites
- `frontend/src/lib/paladin/names.ts` (new) — TS mirror

Phase 3 (~12 files):
- `proto/paladin/admin/v1/tenant_service.proto`
- `backend/internal/api/v1/tenant/default_binding.go` (new)
- `backend/internal/db/queries/tenant_default_binding.sql` (new)
- `backend/migrations/032_tenant_default_bindings.sql` (new)
- `backend/internal/api/connectshim/admin/tenant_default_binding_server.go` (new)
- `frontend/src/app/tenants/[id]/settings/default-binding/page.tsx` (new)
- regenerated proto/sqlc

Phase 4 (~6 files):
- `proto/paladin/iam/v1/whoami.proto` (extend)
- `backend/internal/api/v1/whoami/handler.go`
- `frontend/src/lib/paladin/sdk.ts`
- `frontend/src/lib/auth/whoami.ts`

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Phase 1 backfill deadlocks on hot `audit_log` | run in batches of 10k rows with `LOCK TABLE … IN SHARE UPDATE EXCLUSIVE`; or use a maintenance window |
| Phase 2 resolver becomes a hot path | resolver is pure on inputs once `(tid, ok) → bucket` is cached; LRU 10k entries, TTL 5 min |
| Phase 3 default binding deletion races with in-flight CLI | resolver caches the binding for the request's lifetime only; presign URLs are bucket-anchored already, so no race |
| Phase 4 stale SDK route cache | TTL 60s; rebind UX warns "clients may take up to 60s to converge" |
| Cross-tenant attack via C-shape `tid` ≠ JWT | resolver compares; mismatch → `PERMISSION_DENIED`; covered by mandatory test in Phase 2 acceptance |

## Open questions

1. Should canonical use `tenants/{slug}` or `tenants/{uuid}`? — UUID
   for stability (slug can be renamed). Renames don't invalidate
   canonical refs in audit log. **Decision: UUID.**
2. Do we want a 4th alias `slugs/{tenant_slug}/collections/{ok}` for
   human-pasteable URLs? — Out of scope; can add later behind same
   resolver without schema changes.
3. Phase 5 deprecation timing — defer; revisit after Phase 4 ships.

## BACKLOG entries to add

- Phase 1 audit-log backfill (after merge of Phase 1 PR)
- Phase 2 metric-based readiness gate before unlocking Phase 3
- Phase 3 default-binding UI tab
- Phase 4 SDK route-cache TTL knob
- Phase 5 evaluation (post-Phase 4)
