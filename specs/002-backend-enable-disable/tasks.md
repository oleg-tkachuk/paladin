---
description: "Tasks for the Enable/Disable Storage Backends feature"
---

# Tasks: Enable / Disable Storage Backends

**Input**: Design documents from `/specs/002-backend-enable-disable/`
**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/backend-service.md](contracts/backend-service.md), [quickstart.md](quickstart.md)

**Tests**: Per Constitution Principle I (Tests-First), every code change
ships its unit/integration tests in the SAME commit. Test tasks are
therefore inline within each phase, not deferred.

**Organization**: Tasks grouped by user story (US1–US5 from spec.md).
Setup/Foundational/Polish carry no story label.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallel-safe (different file, no dependency on an incomplete task).
- **[Story]**: maps to a spec.md user story (US1…US5).
- Every task names an explicit file path.

## Commit-scope sequence (Constitution II)

`feat(proto)` (P1) → `feat(store)` (P2) → `feat(admin)` (US1 handler) →
`feat(data)` (US1 gate) → `test(*)` folded into each → `feat(frontend)`
(US3) → `feat(admin)` (US4 guards) → `test(bootstrap)` (US5) →
`docs(backlog)` (Polish). One logical scope per commit.

---

## Phase 1: Setup (proto + codegen)

**Purpose**: The additive proto surface every downstream layer depends on.

- [X] T001 Add `bool enabled = 16;` to the `StorageBackend` message in `backend/proto/paladin/admin/v1/types.proto` (next free field number is 16; do not renumber existing fields).
- [X] T002 Add `rpc SetBackendEnabled(SetBackendEnabledRequest) returns (StorageBackend);` to `BackendService` and define `SetBackendEnabledRequest{ name, enabled, resource_version }` (with buf.validate min_len on `name` + `resource_version`) in `backend/proto/paladin/admin/v1/backend_service.proto` per [contracts/backend-service.md](contracts/backend-service.md) §1. The request targets the resource by `name` (form `storageBackends/{backend_id}`) to match the existing Update/Delete/Rotate/Test convention on BackendService.
- [X] T003 Run `buf generate` from `backend/`; verify regenerated Go stubs and `frontend/src/gen/paladin/admin/v1/backend_service_pb.ts` carry the new field + RPC. Commit codegen with T001/T002 as `feat(proto)`.

**Checkpoint**: Proto compiles; generated clients expose `enabled` + `setBackendEnabled`.

---

## Phase 2: Foundational (data layer — blocks all stories)

**Purpose**: The `enabled` column, queries, domain field, and repo plumbing every story needs.

- [X] T004 Create migration `backend/migrations/037_storage_backend_enabled.sql` with `-- +goose Up` `ALTER TABLE storage_backends ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT true;` and a working `-- +goose Down` `ALTER TABLE storage_backends DROP COLUMN enabled;` (Constitution IV).
- [X] T005 In `backend/internal/store/postgres/queries/storage_backends_v2.sql`: add `enabled` to the SELECT lists of `GetStorageBackendV2` and `ListStorageBackends`; add a new `SetStorageBackendEnabled :execrows` query (`UPDATE … SET enabled=$enabled WHERE id=$id AND (expected_version=0 OR resource_version=expected_version)`) per [contracts/backend-service.md](contracts/backend-service.md) §3. **Do NOT** add `enabled` to `UpsertStorageBackendV2` (bootstrap must not manage it — research D4).
- [X] T006 Run `sqlc generate` from `backend/`; verify the generated `enabled` column + `SetStorageBackendEnabled` method land in `backend/internal/store/postgres/sqlc/`.
- [X] T007 Add `Enabled bool` to the `admindomain.StorageBackend` struct in `backend/internal/api/admin/v1/admindomain/types.go`.
- [X] T008 In `backend/internal/store/postgres/adapters/admin_backend.go`: map `row.Enabled` in `Get` and `List`; add `SetEnabled(ctx, backendID string, enabled bool, expectedVersion int64) error` returning `ErrVersionMismatch` on 0 rows and `ErrNotFound` when the id is absent. Also map `Enabled` in the domain→proto conversion for the existing `GetBackend`/`ListBackends` responses (so `mapping_test` T027 sees field 16 populated), wherever `admindomain.StorageBackend` → `paladin.admin.v1.StorageBackend` is built (connectshim admin mapping) — FR-008.
- [X] T009 [P] Repo test in `backend/internal/store/postgres/adapters/admin_backend_test.go`: `SetEnabled` flips state and bumps `resource_version`; stale version → `ErrVersionMismatch`; `Get`/`List` round-trip `enabled`.

**Checkpoint**: Data layer can read and OCC-update `enabled`. Ship as `feat(store)`.

---

## Phase 3: User Story 1 — Disabling stops all access (P1) 🎯 MVP

**Story goal**: A disabled backend rejects EVERY Paladin-mediated op before any S3 call.

**Independent test**: Disable a non-default backend that owns a bucket; every op type returns `CodeFailedPrecondition` and the mock storage records zero invocations (SC-001).

- [X] T010 [US1] Implement `SetBackendEnabled(ctx, backendID, enabled, expectedVersion)` in `backend/internal/api/admin/v1/backendh/handler.go`: `requireRole(rolePlatformAdmin)` → `h.authorize(ctx, actionManageBackend, backendID)` → `h.repo.SetEnabled(...)` → `h.repo.Get(...)`; map `ErrVersionMismatch`→`CodeAborted`, `ErrNotFound`→`CodeNotFound`; emit audit `backend.set_enabled` (FR-013). Mirrors `UpdateBackend` (handler.go:140). (Default-backend guard is added in US4/T023.)
- [X] T011 [US1] Wire the `SetBackendEnabled` shim method in the admin connectshim so the RPC is reachable (decode `SetBackendEnabledRequest`, call handler, encode `StorageBackend`) in `backend/internal/api/connectshim/` admin wiring.
- [X] T012 [US1] Add `ErrBackendDisabled` sentinel in `backend/internal/api/v1/object/handler.go` and JOIN `storage_backends.enabled` into the resolver SQL in `backend/internal/store/postgres/adapters/object.go` (`LookupBucket` and `LookupBucketMeta`); return `ErrBackendDisabled` when the resolved backend is disabled (research D1, data-model §resolution-join). Keep `LookupBucket`'s `(string, error)` signature.
- [X] T013 [US1] Add a shared `mapResolveErr(err)` helper in `backend/internal/api/v1/object/handler.go` mapping `ErrBackendDisabled`→`CodeFailedPrecondition`, not-found→`CodeNotFound`, else `CodeInternal`; apply it at every `LookupBucket`/`LookupBucketMeta` call site (lines ~419, 552, 818, 966, 1141 + presign + multipart paths). Before wiring, grep `backend/internal/api/v1/object/` and the presign/multipart handlers to CONFIRM every presign-GET, presign-PUT, and multipart-initiate/upload-part/complete path resolves the backend via `LookupBucket`/`LookupBucketMeta`; if any path resolves the bucket by another route, add the `ErrBackendDisabled` check there too — the FR-002/SC-001 "all ops" guarantee depends on no resolution path bypassing the gate. Add a comment at the presign path noting the already-issued-URL limitation (research D6).
- [X] T014 [US1] Add an explicit disabled-backend pre-check in `CreateBucket` (`backend/internal/api/admin/v1/bucketh/handler.go`, after backend_id validation ~line 160): fetch the backend, reject with `CodeFailedPrecondition` if disabled, before persisting the bucket row.
- [X] T015 [US1] Handler test in `backend/internal/api/admin/v1/backendh/handler_test.go`: `SetBackendEnabled` enable+disable persists and returns bumped `resource_version`; setting the current state is an idempotent OK no-op (FR-003).
- [X] T016 [US1] Gate test (object handler test + bucketh test) asserting a disabled backend rejects upload, download, head, copy, list, presign GET, presign PUT, multipart-initiate, and create-bucket — each with `CodeFailedPrecondition` AND a mock storage that records ZERO S3 invocations (FR-002, SC-001).

**Checkpoint**: Core safety guarantee is enforced and proven. Ship `feat(admin)` (handler) + `feat(data)` (gate). **Note**: ship together with US4 (default guard) before exposing in prod — disabling the default backend without the guard is a foot-gun.

---

## Phase 4: User Story 2 — Re-enabling restores access (P1)

**Story goal**: Disable is reversible with zero data impact.

**Independent test**: Disable, confirm reject, re-enable, repeat ops — all succeed against untouched data.

- [X] T017 [US2] Test in `backend/internal/api/v1/object/handler_test.go`: write an object, disable its backend (op now rejected), re-enable, then download the same object and assert byte-identical content; assert no data row was modified by the disable/enable cycle (FR-007, SC-002).

**Checkpoint**: Reversibility proven.

---

## Phase 5: User Story 3 — Operator sees & controls state in UI (P2)

**Story goal**: Disabled backends are visibly badged, non-selectable, with inactive downstream controls; operator can toggle.

**Independent test**: With one enabled + one disabled backend seeded, the `/storage-backends` page and scope picker show the badge + inactive controls; toggling reflects the change.

- [X] T018 [US3] In `frontend/src/hooks/useBackends.ts`: surface `enabled` on returned rows; add `setBackendEnabled(backendId, enabled, resourceVersion)` calling `BackendService.SetBackendEnabled`, with refresh after success.
- [X] T019 [US3] In `frontend/src/components/layout/ScopePicker.tsx`: render a "Disabled" badge on disabled backends, make them non-selectable, and render scope-dependent downstream controls inactive when the selected/hovered backend is disabled (FR-009).
- [X] T020 [US3] In `frontend/src/app/storage-backends/page.tsx`: add an enable/disable toggle per backend (platform-admin), passing the current `resource_version`; on a `CodeFailedPrecondition` from any mid-session op, show a clear "Backend is disabled" toast instead of a raw error (FR-010, FR-011).
- [X] T021a [P] [US3] Add a component/Playwright test under `frontend/tests/` asserting: a seeded disabled backend renders the "Disabled" badge, is non-selectable in `ScopePicker`, and downstream scope-dependent controls are inactive; and that toggling enable/disable updates the displayed state (SC-006, SC-007).
- [X] T021 [US3] Run `pnpm run lint` and `pnpm exec tsc --noEmit` from `frontend/`; both exit zero (FR-007 conventions). Ship `feat(frontend)`.

**Checkpoint**: Operator surface complete.

---

## Phase 6: User Story 4 — Guard rails (P2)

**Story goal**: Cannot disable the default backend; concurrent state changes don't silently clobber.

**Independent test**: Disabling the configured default is refused with a clear reason; a stale `resource_version` is refused as a conflict.

- [X] T022 [US4] Wire the configured default-backend id into the `backendh.Handler` constructor (`NewHandler`) and its construction site in `backend/internal/app/build_deps.go` (pass `config.Storage.DefaultBackend`).
- [X] T023 [US4] Add the default-backend guard branch to `SetBackendEnabled` in `backend/internal/api/admin/v1/backendh/handler.go`: if `!enabled && backendID == h.defaultBackendID` → `CodeFailedPrecondition` ("cannot disable the configured default backend") before calling the repo (FR-006).
- [X] T024 [US4] Handler test: disabling the default backend → `CodeFailedPrecondition` with a constraint-naming message; backend stays enabled (SC-003).
- [X] T025 [US4] Handler test: `SetBackendEnabled` with a stale `resource_version` → `CodeAborted`, no overwrite (FR-004, SC-004).
- [X] T026 [US4] Handler test: a non-platform-admin caller → `CodePermissionDenied` (FR-005).
- [X] T027 [US4] Ensure `backend/internal/api/connectshim/coverage_test.go` and `mapping_test.go` pass for `SetBackendEnabled` (gate markers present; `enabled` field mapped or justified in `skipFields`). Run `go test ./internal/api/connectshim/...`.

**Checkpoint**: Guards enforced. Ship `feat(admin)` (guards).

---

## Phase 7: User Story 5 — Disabled state survives restart (P3)

**Story goal**: A disabled backend stays disabled after a restart; bootstrap never re-enables it.

**Independent test**: Disable a backend, run `EnsureBackends`, assert still disabled and `resource_version` unchanged.

- [X] T028 [US5] Verify (and comment-guard) that `EnsureBackends`, `domainBackendFromYAML`, and `equalForBootstrap` in `backend/internal/bootstrap/backends.go` do NOT reference `enabled` (research D4). Add a one-line comment noting `enabled` is intentionally operator-managed, not config-mirrored. No behavioral code change expected.
- [X] T029 [US5] Bootstrap test in `backend/internal/bootstrap/backends_test.go`: seed a backend, set `enabled=false` directly, run `EnsureBackends` with matching YAML, assert the row is still disabled and `resource_version` is unchanged (FR-012, SC-005). Ship `test(bootstrap)`.

**Checkpoint**: Restart durability proven.

---

## Phase 8: Polish & Cross-Cutting

- [X] T030 [P] Add a BACKLOG.md entry (Status `Aspirational` / Reason / DoD / Blockers) covering the deferred scope: read-only **drain** mode, richer lifecycle states (draining/maintenance/error), and bulk enable/disable (Constitution III, research D7). Ship as `docs(backlog)`.
- [X] T031 [P] Backend gates: `go vet ./...` and `go test ./...` from `backend/` exit zero.
- [X] T032 Verified against the live e2e stack (locally-built Paladin images + local Garage v2.3 at :3900): `backend-disabled.spec.ts` passes 2/2 on Chromium — disabled-backend "Disabled" badge + re-enable toggle (SC-006/SC-007) and the non-selectable scope-picker row. Bringing the stack up surfaced four never-before-run stack bugs, all fixed: (1) `bootstrap.admin` set both password + password_secret (config validation reject); (2) api/admin healthchecks used `wget` absent from the distroless image; (3) the UI BFF read `PALADIN_{DATA,IAM,ADMIN}_URL` but compose set `PALADIN_BACKEND_URLS_*`; (4) the e2e seed transport didn't inject an `Idempotency-Key` for `Create*` RPCs. Combined with 501 unit tests + 7 integration tests, the full SC-001…SC-007 set is now verified.

**Checkpoint**: Feature signed off.

---

## Dependencies

```
T001,T002 ─► T003 ─► T004 ─► T005 ─► T006 ─► T007 ─► T008 ─► T009 [P]
                                                              │
                                          ┌───────────────────┤
                                          ▼                   │
   US1: T010 ─► T011 ─► T012 ─► T013 ─► T014 ─► T015 ─► T016   │
                                          │                   │
   US2: ───────────────────────────────► T017                │
                                          │                   │
   US3: T018 ─► T019 ─► T020 ─► T021  (needs T003 codegen)     │
                                          │                   │
   US4: T022 ─► T023 ─► T024 ─► T025 ─► T026 ─► T027  (extends T010 handler)
                                          │
   US5: T028 ─► T029
                                          ▼
   Polish: T030 [P], T031 [P], T032
```

**Story-level dependencies**:
- Phase 1 (T001–T003) and Phase 2 (T004–T009) complete before any user story.
- US1 (T010–T016) is the MVP and the prerequisite the others layer onto.
- US2 (T017) depends on US1's gate + handler.
- US3 (T018–T021) depends only on Phase 1 codegen (T003) + the handler RPC (T010) for the toggle to call.
- US4 (T022–T027) extends the US1 handler (T010) with the default guard + adds guard tests.
- US5 (T028–T029) depends only on Phase 2 (the column).
- Polish runs last.

**MVP cut-line**: US1 + US4 together (core gate + default-backend safety guard) is the smallest safe shippable slice. US1 alone proves the mechanism but should not be exposed in prod without the default guard.

---

## Parallel Execution Opportunities

- T009 (repo test) is `[P]` after T008.
- US3 frontend (T018–T021) can proceed in parallel with US4 backend (T022–T027) once T010 lands — different trees (frontend vs backend).
- Polish T030 (BACKLOG) and T031 (go gates) are `[P]`.

---

## Task Count Summary

| Phase | Tasks | Story | Parallel-eligible |
|---|---|---|---|
| 1. Setup (proto) | T001–T003 (3) | — | none |
| 2. Foundational (store) | T004–T009 (6) | — | T009 |
| 3. US1 (P1) MVP | T010–T016 (7) | US1 | none |
| 4. US2 (P1) | T017 (1) | US2 | none |
| 5. US3 (P2) | T018–T021a (5) | US3 | T021a (vs US4) |
| 6. US4 (P2) | T022–T027 (6) | US4 | (vs US3) |
| 7. US5 (P3) | T028–T029 (2) | US5 | none |
| 8. Polish | T030–T032 (3) | — | T030, T031 |
| **Total** | **33 tasks** | 5 stories | ~6 [P] |

**Independent test criteria per story** (matches SC-001…SC-007):

| Story | Independent test |
|---|---|
| US1 | Disabled backend rejects every op type with `FailedPrecondition` + zero S3 calls (SC-001). |
| US2 | Re-enable → ops succeed; pre-existing object reads back identical (SC-002). |
| US3 | Disabled backend shows badge + non-selectable + inactive controls; toggle reflects (SC-006/007). |
| US4 | Disable default → `FailedPrecondition` (SC-003); stale rv → `Aborted` (SC-004). |
| US5 | Disabled backend still disabled + rv unchanged after `EnsureBackends` (SC-005). |
