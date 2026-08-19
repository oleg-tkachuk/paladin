-- +goose Up
-- +goose StatementBegin
-- 037_storage_backend_enabled.sql
--
-- Adds the durable enable/disable state to storage backends. A disabled
-- backend (`enabled = false`) processes NO Paladin-mediated requests — the
-- object resolver and CreateBucket reject any operation that resolves to
-- it before contacting the object store. `enabled` is operator-managed
-- via the SetBackendEnabled RPC and is intentionally NOT mirrored from
-- static config (bootstrap EnsureBackends never writes it), so an
-- operator-set disable survives restarts.
--
-- DEFAULT true backfills every existing row to enabled; no data step.
ALTER TABLE storage_backends
    ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE storage_backends
    DROP COLUMN IF EXISTS enabled;
-- +goose StatementEnd
