# Phase 1 Data Model: Enable / Disable Storage Backends

## Entity: Storage backend (existing — one new field)

The `storage_backends` table and its domain/proto representations gain a
single field. Everything else is unchanged.

### New column

```sql
-- migration 037_storage_backend_enabled.sql
ALTER TABLE storage_backends
    ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT true;
```

- **Type**: `BOOLEAN NOT NULL DEFAULT true`
- **Backfill**: the `DEFAULT true` backfills all existing rows to enabled
  (FR-001). No data migration step.
- **Reversibility** (`-- +goose Down`): `ALTER TABLE storage_backends
  DROP COLUMN enabled;`

### Field propagation (one field, four layers)

| Layer | Location | Change |
|-------|----------|--------|
| SQL column | `storage_backends.enabled` | NEW (migration 037) |
| sqlc selects | `GetStorageBackendV2`, `ListStorageBackends` | add `enabled` to SELECT list |
| sqlc mutation | `SetStorageBackendEnabled` (NEW) | `UPDATE … SET enabled=$2 WHERE id=$1 AND resource_version=$3` |
| sqlc upsert | `UpsertStorageBackendV2` | **NO change** — must NOT manage `enabled` (D4) |
| Domain struct | `admindomain.StorageBackend` | add `Enabled bool` |
| Proto message | `paladin.admin.v1.StorageBackend` | add `bool enabled = 16;` |
| Resolver join | object resolver SQL | JOIN `storage_backends.enabled`; sentinel on disabled |

### Domain struct delta

```go
// backend/internal/api/admin/v1/admindomain/types.go
type StorageBackend struct {
    BackendID            string
    // … existing fields …
    Enabled              bool   // NEW — durable enable/disable state
    ResourceVersion      int64
    CreatedAt            time.Time
    UpdatedAt            time.Time
}
```

## State model

`enabled` is a two-value state. No richer lifecycle (draining /
maintenance / error) — those are BACKLOG (research D7).

```
            SetBackendEnabled(enabled=false)   [refused if backend == default]
   ┌──────────────────────────────────────────────────────┐
   │                                                        ▼
[ENABLED] ◄────────────────────────────────────────── [DISABLED]
   ▲          SetBackendEnabled(enabled=true)               │
   │                                                        │
   └─ default state for new rows (DB DEFAULT true)          │
                                                            │
   While DISABLED: every Paladin-mediated op resolving to this  │
   backend → CodeFailedPrecondition (before any S3 call).   │
   Stored data is untouched; re-enable fully restores it. ◄─┘
```

### Transition rules

| From | To | Operation | Guard | Result on violation |
|------|----|-----------|-------|---------------------|
| ENABLED | DISABLED | `SetBackendEnabled(false)` | backend is NOT the configured default (D3); `resource_version` matches (OCC) | `CodeFailedPrecondition` (default) / `CodeAborted` (OCC) |
| DISABLED | ENABLED | `SetBackendEnabled(true)` | `resource_version` matches (OCC) | `CodeAborted` (OCC) |
| X | X (same) | `SetBackendEnabled(X)` | `resource_version` matches | no-op success (idempotent, FR-003) |

- **Idempotency**: setting the current state succeeds as a no-op; the
  sqlc `UPDATE` still runs (touching `resource_version`/`updated_at`),
  but the observable state is unchanged. (Alternatively the handler may
  short-circuit when `existing.Enabled == requested` to avoid an rv bump
  — decided in tasks; either satisfies FR-003.)
- **OCC**: `resource_version` mismatch → 0 rows updated →
  `ErrVersionMismatch` → `CodeAborted` (FR-004, SC-004).
- **Reversibility**: DISABLED→ENABLED touches no stored object data
  (FR-007, SC-002).

## Resolution-join shape (data-plane gate)

The object resolver (`LookupBucket` / `LookupBucketMeta`) gains a JOIN so
the resolved binding knows its backend's `enabled` state in the same
query (zero extra round trip, D1):

```sql
SELECT b.bucket_name, sb.enabled
FROM object_keys b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE b.tenant_id = $1 AND b.object_key = $2;
-- if NOT sb.enabled → repo returns object.ErrBackendDisabled
```

`LookupBucket`'s public return type stays `(string, error)` — the
disabled signal travels as the `object.ErrBackendDisabled` sentinel, not
a widened struct (D1).

## Relationships (unchanged)

- `buckets.backend_id` → `storage_backends.id` (FK)
- `object_keys.backend_id` → `storage_backends.id` (FK)

Disabling a backend changes none of these rows — it only flips
`storage_backends.enabled`. Buckets and object keys remain intact and
become reachable again on re-enable.
