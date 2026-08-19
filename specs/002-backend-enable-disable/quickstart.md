# Quickstart: Enable / Disable Storage Backends

One-page operator + developer manual for the feature.

## What it does

Each storage backend has a durable `enabled` flag. A **disabled** backend
processes **no** Paladin requests of any kind — upload, download, head, copy,
list, presign (GET/PUT), multipart, and bucket-create are all refused
with `FailedPrecondition` before Paladin ever contacts the object store.
Disabling is reversible (data is untouched) and survives restarts.

## Operator: toggle a backend

**UI**: `/storage-backends` → pick a backend → toggle Enable/Disable
(platform-admin only). Disabled backends show a "Disabled" badge and are
not selectable in the scope picker; downstream actions on them are greyed.

**API** (admin plane, `:8090`):

```
SetBackendEnabled { backend_id: "primary", enabled: false, resource_version: "<current>" }
```

- Get the current `resource_version` from `GetBackend`/`ListBackends`.
- Disabling the configured `storage.default_backend` is refused
  (`FailedPrecondition`).
- A stale `resource_version` is refused (`Aborted`) — re-read and retry.
- Setting the state it already has is a successful no-op.

## Verify the guarantee (the regression-coverage promise)

1. **Disable stops everything**: disable a non-default backend that owns a
   bucket; attempt upload / download / presign / bucket-create against it.
   Each MUST fail with `FailedPrecondition` ("backend is disabled") and
   the object store MUST NOT be contacted. (Automated: the gate test uses
   a mock storage and asserts zero S3 invocations — SC-001.)
2. **Re-enable restores**: re-enable; the same ops succeed and a
   pre-existing object reads back byte-identical (SC-002).
3. **Default guard**: try to disable the default backend → refused with a
   message naming the constraint (SC-003).
4. **OCC**: submit a disable with a stale `resource_version` → `Aborted`,
   no silent overwrite (SC-004).
5. **Restart durability**: disable a backend, restart the platform, check
   it is still disabled and still rejecting ops (SC-005).
6. **UI**: with a disabled backend seeded, the `/storage-backends` page
   and scope picker show the badge + inactive controls (SC-006); toggling
   reflects within 2s (SC-007).

## Known limitation — presigned URLs

Disabling stops Paladin from issuing **new** presigned URLs and rejects all
Paladin-mediated ops. It does **not** revoke presigned URLs already handed
out — those hit the object store directly, bypassing Paladin, and expire on
their own TTL (default 15 min, `config.limits.presign.*_ttl`). To kill
existing URLs immediately, rotate the backend's S3 credentials
(`RotateCredentials`) — that is a separate operation.

## Developer: where the pieces live

| Piece | Path |
|-------|------|
| Migration | `backend/migrations/037_storage_backend_enabled.sql` |
| Proto | `backend/proto/paladin/admin/v1/{types,backend_service}.proto` |
| sqlc query | `backend/internal/store/postgres/queries/storage_backends_v2.sql` (`SetStorageBackendEnabled`) |
| Admin handler | `backend/internal/api/admin/v1/backendh/handler.go` (`SetBackendEnabled`) |
| Repo | `backend/internal/store/postgres/adapters/admin_backend.go` (`SetEnabled`) |
| Data-plane gate | `backend/internal/api/v1/object/handler.go` (`ErrBackendDisabled` + `mapResolveErr`) + `adapters/object.go` (resolver JOIN) |
| CreateBucket gate | `backend/internal/api/admin/v1/bucketh/handler.go` |
| Bootstrap (must NOT manage `enabled`) | `backend/internal/bootstrap/backends.go` |
| Frontend | `frontend/src/hooks/useBackends.ts`, `components/layout/ScopePicker.tsx`, `app/storage-backends/page.tsx` |

Regenerate after proto edits: `buf generate` (backend stubs + `frontend/src/gen`).
Regenerate after query edits: `sqlc generate`.
