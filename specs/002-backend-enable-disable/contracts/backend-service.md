# Contract: BackendService.SetBackendEnabled + data-plane gate

## 1. Proto deltas

### `paladin/admin/v1/types.proto` — StorageBackend

Add field **16** (next free; 15 is `updated_at`):

```protobuf
message StorageBackend {
  // … fields 1–15 unchanged …
  google.protobuf.Timestamp updated_at = 15;
  bool enabled = 16;   // NEW — durable enable/disable state; true = serving
}
```

Additive, non-breaking (Constitution IV).

### `paladin/admin/v1/backend_service.proto` — new RPC + messages

```protobuf
service BackendService {
  // … existing RPCs unchanged …
  rpc SetBackendEnabled(SetBackendEnabledRequest) returns (StorageBackend);
}

message SetBackendEnabledRequest {
  string name             = 1 [(buf.validate.field).string.min_len = 1]; // "storageBackends/{backend_id}"
  bool   enabled          = 2;
  string resource_version = 3 [(buf.validate.field).string.min_len = 1]; // OCC, required
}
// Response is the updated StorageBackend (carries new enabled + bumped resource_version).
//
// Field-naming note: the request targets the resource by `name` (the
// canonical `storageBackends/{backend_id}` form) to match the existing
// BackendService convention used by Get/Update/Delete/Rotate/Test.
// The shim parses backend_id out of name before calling the handler.
```

`resource_version` is **required** (min_len=1) so the OCC check is never
accidentally bypassed from the wire. (The repo layer still treats an
internal `expected_version == 0` as "skip check" per the shared query,
but the RPC validation forbids an empty version on this surface.)

## 2. Handler contract — `backendh.SetBackendEnabled`

Mirrors `UpdateBackend` (`backendh/handler.go:140`). The handler is
constructed with the configured default-backend id (new constructor arg).

```
SetBackendEnabled(ctx, backendID string, enabled bool, expectedVersion int64) (*StorageBackend, error)

1. requireRole(ctx, rolePlatformAdmin)                    → CodePermissionDenied / CodeUnauthenticated
2. h.authorize(ctx, actionManageBackend, backendID)       → CodePermissionDenied   (reuses Cedar ManageBackend)
3. if !enabled && backendID == h.defaultBackendID:        → CodeFailedPrecondition  ("cannot disable the configured default backend")
4. h.repo.SetEnabled(ctx, backendID, enabled, expectedVersion)
      ├─ 0 rows (version mismatch) → ErrVersionMismatch   → CodeAborted
      ├─ not found                 → ErrNotFound           → CodeNotFound
      └─ ok
5. got := h.repo.Get(ctx, backendID); return &got         (carries new enabled + resource_version)
6. audit: writeBackendAudit(ctx, "backend.set_enabled", backendID, enabled)   (FR-013)
```

### Error mapping table

| Condition | Connect code | Notes |
|-----------|-------------|-------|
| No principal | `CodeUnauthenticated` | from `requireRole`/`authorize` |
| Not platform-admin / Cedar deny | `CodePermissionDenied` | FR-005 |
| Disabling the default backend | `CodeFailedPrecondition` | FR-006, SC-003 |
| `resource_version` mismatch | `CodeAborted` | FR-004, SC-004 |
| Backend id unknown | `CodeNotFound` | — |
| Success (incl. idempotent no-op) | OK | FR-003 |

## 3. Repository contract — `BackendRepoV2.SetEnabled`

```go
// returns ErrVersionMismatch on 0-rows, ErrNotFound if id absent
func (r *BackendRepoV2) SetEnabled(ctx, backendID string, enabled bool, expectedVersion int64) error
```

Backed by new sqlc query:

```sql
-- name: SetStorageBackendEnabled :execrows
UPDATE storage_backends
SET enabled = sqlc.arg('enabled')
WHERE id = sqlc.arg('id')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);
```

(`resource_version` is bumped by the existing row-version trigger/mechanism
that already serves `UpdateStorageBackend`; this query follows the same
WHERE-clause OCC pattern.)

## 4. Data-plane gate contract

**Guarantee (FR-002, SC-001)**: every Paladin-mediated op that resolves to a
disabled backend is refused with `CodeFailedPrecondition` **before** any
object-store call.

**Mechanism**: the object resolver returns a sentinel when the resolved
backend is disabled:

```go
// backend/internal/api/v1/object/handler.go (errors)
var ErrBackendDisabled = errors.New("object: storage backend is disabled")
```

The resolver SQL (`LookupBucket` / `LookupBucketMeta` in
`adapters/object.go`) JOINs `storage_backends.enabled` and returns
`ErrBackendDisabled` when false. Each object-handler call site maps it via
a shared helper:

```go
func mapResolveErr(err error) error {
    switch {
    case errors.Is(err, object.ErrBackendDisabled):
        return connect.NewError(connect.CodeFailedPrecondition, err)
    case errors.Is(err, <not-found>):
        return connect.NewError(connect.CodeNotFound, err)
    default:
        return connect.NewError(connect.CodeInternal, err)
    }
}
```

**Covered ops** (all resolve through the resolver before S3): UploadObject,
DownloadObject, HeadObject, CopyObject, presign GET, presign PUT,
multipart (initiate/upload-part/complete), list.

**CreateBucket** (admin plane, `bucketh/handler.go:152`) does NOT use the
object resolver, so it gets an explicit pre-check: after validating
`backend_id`, fetch the backend and reject with `CodeFailedPrecondition`
if disabled — before persisting the bucket row.

**Out of gate scope**: already-issued presigned URLs (D6) — they bypass
Paladin and expire on TTL. A comment at the presign gate documents this.

## 5. Required test coverage (Constitution I — ships with the code)

| Test | Asserts | Maps to |
|------|---------|---------|
| `SetBackendEnabled` happy | enable + disable flip persists, returns bumped rv | FR-003 |
| `SetBackendEnabled` idempotent | setting current state → OK, no error | FR-003 |
| `SetBackendEnabled` OCC | stale `resource_version` → `CodeAborted` | FR-004, SC-004 |
| `SetBackendEnabled` default guard | disable default id → `CodeFailedPrecondition` | FR-006, SC-003 |
| `SetBackendEnabled` authz | non-admin → `CodePermissionDenied` | FR-005 |
| data-plane gate | each op type vs disabled backend → `CodeFailedPrecondition`, **no S3 call** (mock storage asserts zero invocations) | FR-002, SC-001 |
| re-enable | op succeeds after re-enable; pre-existing object reads back identical | FR-007, SC-002 |
| bootstrap preserve | disabled row stays disabled + rv unchanged after `EnsureBackends` | FR-012, SC-005 |
| connectshim coverage/mapping | new RPC is gate-covered and field-mapped (incl. `enabled`) | Constitution VI/I |

## 6. Frontend contract

- `useBackends` (`frontend/src/hooks/useBackends.ts`): `StorageBackend`
  rows now expose `enabled`; add `setBackendEnabled(backendId, enabled,
  resourceVersion)` calling `BackendService.SetBackendEnabled`.
- `ScopePicker` (`components/layout/ScopePicker.tsx`): render a
  "Disabled" badge on disabled backends; make them non-selectable;
  downstream scope-dependent controls inactive (FR-009).
- `/storage-backends` page (`app/storage-backends/page.tsx`):
  enable/disable toggle per backend (platform-admin); on a
  `CodeFailedPrecondition` from a mid-session op, show a clear toast
  "Backend is disabled" rather than a raw error (FR-011).
