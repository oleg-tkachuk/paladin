# Phase 0 Research: Enable / Disable Storage Backends

All decisions below were resolved against the live codebase (paths +
line numbers cited). No open NEEDS CLARIFICATION items remain.

---

## D1 — Where to enforce the "disabled backend rejects all ops" gate

**Decision**: Push the check **into the backend-resolution step** of the
data plane and return a sentinel `ErrBackendDisabled`; handlers map it to
`connect.CodeFailedPrecondition`. The check piggybacks on the existing
resolution query (JOIN `storage_backends.enabled`) so it adds **zero**
extra round trips. `CreateBucket` (admin plane), which does not go
through the object resolver, gets its own explicit check.

**Why**: Every object operation (upload, download, head, copy, presign
GET/PUT, multipart) resolves the physical bucket via
`Repository.LookupBucket` or `LookupBucketMeta` before touching S3
(`backend/internal/api/v1/object/handler.go:142,146`; call sites at
lines 419, 552, 818, 966, 1141). Making the **resolver itself** refuse a
disabled backend is the single chokepoint that covers all current and
future ops automatically — no op can reach S3 without first resolving its
backend.

**Important shape note**: `LookupBucket(ctx, tenantID, objectKey)
(string, error)` today returns only the bucket-name string — NOT
backend_id or enabled. So we do **not** widen its return type. Instead:
  - The resolver SQL gains a JOIN to `storage_backends` and a
    `WHERE ... ` predicate / returned `enabled` column.
  - When the resolved backend is disabled, the repo returns
    `object.ErrBackendDisabled` (new sentinel, sibling to the existing
    not-found path).
  - Each of the ~5 object-handler call sites adds one error-mapping
    branch: `if errors.Is(err, object.ErrBackendDisabled) { return
    connect.NewError(connect.CodeFailedPrecondition, err) }` (a tiny
    shared helper `mapResolveErr(err)` keeps it DRY).

**Alternatives considered**:
  - *In-memory enabled cache on the storage client map* — rejected:
    introduces a staleness window where a just-disabled backend keeps
    serving until cache refresh, violating the strict guarantee (FR-002,
    SC-001).
  - *Widen `LookupBucket` return to a struct* — rejected: touches 5 call
    sites' signatures for no benefit over a sentinel error; the error
    path already exists at every site.
  - *Tear down the S3 client when disabled* — rejected: clients are
    pre-wired from config at boot (`app/build_deps.go`); disable is a
    control-plane state, not a config change, and must be reversible
    without re-wiring.

---

## D2 — Shape of the state-change operation

**Decision**: A dedicated admin RPC
`SetBackendEnabled(SetBackendEnabledRequest) returns (StorageBackend)`
on the existing `BackendService`
(`backend/proto/paladin/admin/v1/backend_service.proto:14`). Request carries
`backend_id`, `enabled`, `resource_version`.

**Why**: Per Constitution VII, a non-`Create*` mutation uses
`resource_version` OCC, not an idempotency key. A dedicated `Set*` verb
(vs reusing `UpdateBackend`+field-mask) gives: a clean audit action, a
single obvious UI verb, and a self-documenting contract. It is naturally
idempotent — setting the state a backend already has is a no-op success.
`Set*` is explicitly an OCC-bucket verb in Constitution VII.

**Reused infrastructure**:
  - Cedar: existing `ManageBackend` action
    (`backend/policies/schema.cedarschema:97`) — no new action needed.
  - Authz gate: `requireRole(ctx, rolePlatformAdmin)` +
    `h.authorize(ctx, actionManageBackend, backendID)`, mirroring
    `UpdateBackend` (`backendh/handler.go:140`).
  - OCC: new sqlc query `SetStorageBackendEnabled` with the same
    `WHERE id = $1 AND resource_version = $2` pattern as
    `UpdateStorageBackend` (`storage_backends_v2.sql:57`); 0 rows →
    `admindomain.ErrVersionMismatch` → `connect.CodeAborted`.

**Alternatives considered**:
  - *Reuse `UpdateBackend` + `update_mask=["enabled"]`* — rejected:
    state transitions get lost among generic field edits in audit/UI;
    muddier Cedar/intent story.
  - *Two verbs `EnableBackend`/`DisableBackend`* — rejected: +2 surface
    methods for no gain; a single bool arg is clearer and equally
    auditable.

---

## D3 — Default-backend disable guard

**Decision**: `SetBackendEnabled(enabled=false)` on the backend whose id
equals `config.Storage.DefaultBackend` is refused with
`connect.CodeFailedPrecondition` and a message naming the constraint.
Disabling a non-default backend that merely *has* buckets is allowed.

**Why**: New buckets created without an explicit backend resolve to the
default; disabling it would break bucket creation platform-wide (FR-006).
A backend with existing buckets is the intended target of disable —
cutting access is the feature's purpose (US4 scenario 3). The default-id
is available to the handler via injected config (the handler will receive
the default-backend id at construction, alongside `repo` + `policy`).

**Alternatives considered**:
  - *Block disabling any backend with active buckets* — rejected by the
    user (operator clarification): defeats the emergency-stop use case.
  - *No guard* — rejected: foot-gun that silently breaks default bucket
    creation.

---

## D4 — Restart durability (bootstrap must not clobber `enabled`)

**Decision**: Leave `enabled` entirely out of the bootstrap config-mirror.
`EnsureBackends` (`backend/internal/bootstrap/backends.go:54`) maps YAML
→ domain via `domainBackendFromYAML` and skips writes when
`equalForBootstrap` matches. Neither function references `enabled`, and
YAML has no `enabled` key — so the mirror never reads or writes it. The
`UpsertStorageBackendV2` query (`storage_backends_v2.sql:3`) does not list
`enabled` in its `ON CONFLICT DO UPDATE SET`, so an upsert preserves the
stored value.

**Why**: FR-012 / SC-005 — a disabled backend must stay disabled across
restarts. The convergence-skip already avoids bumping `resource_version`
on every boot; we extend the same "config manages only config-sourced
fields" principle to the new column.

**Verification**: a bootstrap test seeds a backend, sets `enabled=false`
directly, runs `EnsureBackends` with matching YAML, and asserts the row
is still disabled and `resource_version` unchanged.

**Note on first-insert default**: a backend present only in YAML with no
prior row is inserted via `UpsertStorageBackendV2`; since the column
default is `true`, the new row is enabled (FR-001 / US5 scenario 2). No
code change needed — the DB default carries it.

---

## D5 — Error codes

**Decision**:
  - Disabled-backend op reject → `connect.CodeFailedPrecondition`
    ("backend %q is disabled").
  - Default-backend disable guard → `connect.CodeFailedPrecondition`
    ("cannot disable the configured default backend %q").
  - OCC version mismatch → `connect.CodeAborted` (mirrors
    `UpdateBackend`, `backendh/handler.go:148`).
  - Backend not found → `connect.CodeNotFound`.

**Why**: `FailedPrecondition` is the gRPC-canonical code for "resource
exists but is not in a state that permits the operation" — exactly the
disabled case. `Aborted` is the established OCC-conflict code in this
codebase. Consistency lets the frontend map codes to messages uniformly
(FR-011).

---

## D6 — Presigned-URL limitation (no engineering, just documentation)

**Decision**: Disabling blocks issuance of *new* presigned URLs (the
presign handlers resolve the backend and hit the same gate) and all
Paladin-mediated ops. Already-issued presigned URLs are honoured directly by
the object store, bypassing Paladin, and cannot be revoked here — they expire
on their existing short TTL (`config.limits.presign.*_ttl`, default 15m).

**Why**: A presigned URL is a self-contained, signed S3 request; Paladin is
not in its request path. Revocation would require rotating the backend's
S3 credentials (out of scope; `RotateCredentials` RPC exists separately)
or S3-side policy — neither is part of an enable/disable toggle. Captured
as an explicit Assumption in the spec; surfaced in `quickstart.md` and a
code comment at the presign gate.

---

## D7 — Out-of-scope, recorded for BACKLOG

The closing commit adds one BACKLOG.md entry (Constitution III) covering:
  - **Read-only drain mode** — a distinct backend state that blocks
    writes/presign-PUT but allows reads/presign-GET for data migration.
  - **Richer lifecycle states** — draining / maintenance / error
    (e.g. auto-set on `TestBackend` failure).
  - **Bulk enable/disable** — multi-backend toggle in one action.

Each with Status `Aspirational` / Reason / DoD / Blockers.
