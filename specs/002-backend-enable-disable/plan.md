# Implementation Plan: Enable / Disable Storage Backends

**Branch**: `002-backend-enable-disable` | **Date**: 2026-05-28 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/002-backend-enable-disable/spec.md`

## Summary

Add a durable `enabled` flag to each storage backend and enforce it as a
hard server-side gate: any PALADIN-mediated operation that resolves to a
disabled backend is refused with `CodeFailedPrecondition` **before** any
object-store call. State is flipped through a single new admin RPC
`SetBackendEnabled(backend_id, enabled, resource_version)` —
OCC-guarded, idempotent, gated by the existing `ManageBackend` Cedar
action + `platform.admin` role, and audited. Disabling the configured
`storage.default_backend` is refused. The bootstrap config-mirror
(`EnsureBackends`) is left untouched for `enabled` so an operator-set
disable survives restarts. Frontend surfaces the state (badge,
non-selectable scope, inactive downstream controls, graceful
mid-session rejection).

## Technical Context

**Language/Version**: Go 1.26 (backend), TypeScript 6.x / Next.js 16
App Router (frontend) — matches existing repo toolchain.

**Primary Dependencies**: Connect-RPC + buf (proto), sqlc + pgx
(Postgres access), Cedar (authz), goose (migrations), audit.AsyncWriter
(audit), `@connectrpc/connect-web` + generated `src/gen` stubs
(frontend).

**Storage**: Postgres — `storage_backends` table gains one column
`enabled BOOLEAN NOT NULL DEFAULT true`. No data migration; default
backfills existing rows to enabled.

**Testing**: Go `go test` (handler + repo + bootstrap unit/integration,
connectshim coverage/mapping); frontend Playwright E2E reuse pattern is
out of scope for this feature's tests but UI unit/interaction covered by
existing frontend test setup.

**Target Platform**: Linux server (multi-plane PALADIN binary); admin plane
listener `:8090`, data plane `:8080`.

**Project Type**: web-application (existing `backend/` + `frontend/`
monorepo). No new top-level module.

**Performance Goals**: The disabled-check MUST add no extra DB round trip
on the object hot path — it piggybacks on the existing bucket-resolution
query by adding `storage_backends.enabled` to the join.

**Constraints**:
  - Gate runs before any S3 call (FR-002); rejection is
    `CodeFailedPrecondition`.
  - `SetBackendEnabled` is OCC-guarded (FR-004) → `CodeAborted` on
    version mismatch, mirroring `UpdateBackend`.
  - `EnsureBackends` MUST NOT manage `enabled` (FR-012) — leave it out of
    `domainBackendFromYAML` and `equalForBootstrap`.
  - Proto change is additive (new field #16, new RPC) — non-breaking.
  - Migration MUST have a working `-- +goose Down`.

**Scale/Scope**: ~1 migration, 1 proto field + 1 RPC + 2 messages,
~1 new handler method, 1 new sqlc query (`SetStorageBackendEnabled`),
gate injection at 2–3 data-plane resolution points + CreateBucket,
bootstrap left-as-is verification test, frontend (`useBackends` +
ScopePicker + `/storage-backends` page toggle). Backends are few (single
digits) — no pagination/perf concern for the admin surface.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| # | Principle | Gate | Status |
|---|---|---|---|
| I | Tests-First Guard Rails | New tests ship in the SAME commits: handler test for `SetBackendEnabled` (happy, idempotent no-op, OCC conflict→Aborted, default-backend guard→FailedPrecondition, authz deny); repo test for the new query + OCC; data-plane gate test asserting disabled backend rejects every op type with NO S3 call (SC-001); bootstrap test asserting restart preserves `enabled` (SC-005). Store/middleware-adjacent layers explicitly covered. | ✅ Pass |
| II | Single-Scope Conventional Commits | Commit sequence (tasks.md): `feat(proto)` → `feat(store)` migration+sqlc → `feat(admin)` SetBackendEnabled handler/repo → `feat(data)` resolution gate → `feat(frontend)` UI → `docs(backlog)` drain-mode entry. One scope each. | ✅ Pass |
| III | BACKLOG Source of Truth | Out-of-scope cuts (read-only **drain** mode; richer lifecycle states draining/maintenance/error; bulk enable/disable) land as a BACKLOG.md entry in the closing commit. No closed entry to delete (new feature). | ✅ Pass |
| IV | Pre-1.0 Breaking Allowed | Proto change is additive (field #16 + new RPC) — non-breaking. SQL migration 037 adds a nullable-defaulted column with a working `-- +goose Down` (`DROP COLUMN enabled`). No dependent BACKLOG entries. | ✅ Pass |
| V | Local-Dev Parity Through Overlays | No chart/helm value added; `enabled` defaults true in-schema. No overlay change, no local-only convenience. | ✅ N/A |
| VI | Security Floor (Cedar + JWT + Capability) | `SetBackendEnabled` is on the admin plane (audience `paladin-admin`, existing `auth.Interceptor`), gated by `requireRole(rolePlatformAdmin)` + `h.authorize(ctx, actionManageBackend, backendID)` — reuses the existing `ManageBackend` Cedar action (already in `schema.cedarschema`). No new action, no pre-auth allowlist change. connectshim coverage_test sees the gate markers. | ✅ Pass |
| VII | Idempotency for Mutations | `SetBackendEnabled` is a `Set*` mutation, NOT `Create*` → no idempotency key; uses `resource_version` OCC (maps mismatch → `CodeAborted`). Naturally idempotent (setting the current state is a no-op success). | ✅ Pass |

**Re-check after Phase 1 design**: see "Post-Design Re-Check" below.

## Project Structure

### Documentation (this feature)

```text
specs/002-backend-enable-disable/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/
│   └── backend-service.md   # SetBackendEnabled RPC + gate contract + proto deltas
├── checklists/
│   └── requirements.md  # Spec quality gate (already produced)
└── tasks.md             # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
backend/
├── proto/paladin/admin/v1/
│   ├── types.proto                  # MODIFIED — add `bool enabled = 16;` to StorageBackend
│   └── backend_service.proto        # MODIFIED — add SetBackendEnabled RPC + Set*Request/Response
├── migrations/
│   └── 037_storage_backend_enabled.sql   # NEW — ADD COLUMN enabled + goose Down
├── internal/store/postgres/queries/
│   └── storage_backends_v2.sql      # MODIFIED — add SetStorageBackendEnabled query;
│                                    #            add `enabled` to Get/List/Upsert selects
├── internal/store/postgres/adapters/
│   ├── admin_backend.go             # MODIFIED — map `enabled`; add SetEnabled repo method
│   └── object.go                    # MODIFIED — LookupBucket(Meta) join returns backend enabled
├── internal/api/admin/v1/
│   ├── admindomain/types.go         # MODIFIED — add Enabled bool to StorageBackend
│   └── backendh/handler.go          # MODIFIED — add SetBackendEnabled handler (+ default guard)
├── internal/api/v1/object/handler.go    # MODIFIED — reject disabled backend after resolution
├── internal/api/admin/v1/bucketh/handler.go  # MODIFIED — reject CreateBucket on disabled backend
├── internal/bootstrap/backends.go   # UNCHANGED for enabled (verified by test) — leaves enabled alone
└── internal/api/connectshim/        # auto-enrolls new RPC; mapping/coverage tests must pass

frontend/
├── src/gen/paladin/admin/v1/            # REGENERATED — buf generate (enabled field + SetBackendEnabled)
├── src/hooks/useBackends.ts         # MODIFIED — expose `enabled`; add setBackendEnabled()
├── src/components/layout/ScopePicker.tsx  # MODIFIED — badge + non-selectable disabled backends
└── src/app/storage-backends/page.tsx      # MODIFIED — enable/disable toggle + graceful error
```

**Structure Decision**: Web application — existing `backend/` +
`frontend/` split. The feature threads one new column + RPC through the
established admin-plane handler/repo/sqlc layering and injects a single
gate on the data-plane resolution path. No new packages or services.

## Phase 0: Outline & Research

See [research.md](research.md). Key resolved decisions:
  - **Gate placement**: piggyback `storage_backends.enabled` onto the
    existing bucket-resolution join (`LookupBucket`/`LookupBucketMeta`)
    so the check costs zero extra round trips; reject in the handler
    with `CodeFailedPrecondition`. Rejected: in-memory backend cache
    (staleness window lets a just-disabled backend keep serving).
  - **State op shape**: dedicated `SetBackendEnabled` over reusing
    `UpdateBackend`+mask — clearer audit, cleaner Cedar story, single
    obvious UI verb. OCC via `resource_version`.
  - **Bootstrap preservation**: leave `enabled` out of
    `domainBackendFromYAML` + `equalForBootstrap`; YAML has no `enabled`
    field, so the config-mirror never touches it.
  - **Error code**: `CodeFailedPrecondition` for both the disabled-op
    reject and the default-backend-disable guard; `CodeAborted` for OCC
    mismatch (mirrors `UpdateBackend`).
  - **Presigned-URL limitation**: documented, not engineered around —
    disabling blocks *new* presign issuance; already-issued URLs expire
    on TTL.

## Phase 1: Design & Contracts

Produced:
  - [data-model.md](data-model.md) — the `enabled` field, its state
    transitions, and the resolution-join shape.
  - [contracts/backend-service.md](contracts/backend-service.md) — the
    proto deltas, the `SetBackendEnabled` request/response + error
    mapping, and the data-plane gate contract.
  - [quickstart.md](quickstart.md) — how to exercise enable/disable end
    to end and verify the guarantee.

### Agent context update

`CLAUDE.md` updated between the `<!-- SPECKIT START -->` /
`<!-- SPECKIT END -->` markers to point at this plan.

## Post-Design Re-Check

Re-running the 7 gates against the produced design:

| # | Principle | Verdict |
|---|---|---|
| I | Tests-First | Contract enumerates every test that ships with each commit (gate test asserts no-S3-call; bootstrap restart test). ✅ |
| II | Single-Scope Commits | proto / store / admin / data / frontend / docs(backlog) — one scope each. ✅ |
| III | BACKLOG | Drain mode + richer states + bulk ops recorded as a single BACKLOG entry in the closing commit. ✅ |
| IV | Pre-1.0 Breaking | Additive proto + reversible migration 037. ✅ |
| V | Local-Dev Parity | No overlay/chart change. ✅ N/A |
| VI | Security Floor | Reuses ManageBackend gate; admin-plane audience; no allowlist change. ✅ |
| VII | Idempotency | Set* + OCC, naturally idempotent, no key required. ✅ |

**No violations. No complexity-tracking entries needed.**

## Complexity Tracking

> Empty — Constitution Check passes cleanly pre- and post-design.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| _(none)_ | — | — |
