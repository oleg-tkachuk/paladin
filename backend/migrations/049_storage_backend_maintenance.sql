-- +goose Up
-- +goose StatementBegin
-- 049_storage_backend_maintenance.sql
--
-- Operator-set "maintenance" flag for storage backends (BACKLOG: "Backend
-- states beyond enable/disable" → the manual half of maintenance/error).
--
-- Distinct from the two neighbouring concepts:
--   - `enabled` (037) / `read_only` (047) are operator GATES — they change
--     what the resolver permits.
--   - `health_*` (048) is DERIVED — TestBackend writes it.
--   - `maintenance` is OPERATOR-SET and ADVISORY: it is a label an operator
--     raises to signal "I'm working on this backend", surfaced in the UI. It
--     does NOT gate operations (an operator who wants to block writes also
--     drains/disables); it lives on storage_backends (not the health table) so
--     an automated TestBackend probe can never clobber it.
--
-- Operator-managed via SetBackendMaintenance (OCC-guarded, like enabled/
-- read_only) and NOT mirrored from static config, so it survives restarts.
-- DEFAULT false backfills every existing row; no data step.
ALTER TABLE storage_backends
    ADD COLUMN maintenance BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE storage_backends
    DROP COLUMN IF EXISTS maintenance;
-- +goose StatementEnd
