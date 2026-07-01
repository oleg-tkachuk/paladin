-- +goose Up
-- +goose StatementBegin
-- 047_storage_backend_read_only.sql
--
-- Read-only "drain" state for storage backends (BACKLOG: "Backend states
-- beyond enable/disable"). Layered on 037's `enabled` master switch:
--
--   enabled=false                 → reject ALL ops (feature 002, unchanged).
--   enabled=true, read_only=true  → DRAIN: reads / presign-GET / HEAD / list
--                                   still resolve, mutations (PUT / POST /
--                                   multipart-init / copy-dest / update /
--                                   delete / version writes) are refused, so
--                                   an operator can migrate data off a backend
--                                   before fully disabling it.
--   enabled=true, read_only=false → normal.
--
-- The object-resolution chokepoint (LookupBucket / LookupBucketMeta) enforces
-- the split by operation class. `read_only` is operator-managed via the
-- SetBackendReadOnly RPC and, like `enabled`, is intentionally NOT mirrored
-- from static config (bootstrap EnsureBackends never writes it), so a
-- drain survives restarts.
--
-- DEFAULT false backfills every existing row to writable; no data step.
ALTER TABLE storage_backends
    ADD COLUMN read_only BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE storage_backends
    DROP COLUMN IF EXISTS read_only;
-- +goose StatementEnd
