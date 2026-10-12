-- +goose Up
-- +goose StatementBegin

-- A storage migration whose physical verify fails now rolls the tenant back
-- to its source bucket (state 'rolling_back', ADR-0015) instead of leaving it
-- 'failed' on a dedicated bucket that is missing objects.
--
-- rebound_at is when the rebind to the dedicated bucket committed — set in
-- the rebind transaction itself. Objects written from then on landed in the
-- dedicated bucket only, and are what the rollback copies back to the source
-- before rebinding the tenant there.
--
-- rollback_cursor_at / rollback_cursor_id are the rollback's resume cursor:
-- the (objects.updated_at, objects.id) of the last object it copied back.
--
-- Nullable columns with no default: metadata-only changes (CONVENTIONS.md).
-- A migration rebound before this file has no rebound_at; the stuck-tenant
-- path that rolls one back derives it (adapters.TenantRepo says how).
ALTER TABLE tenant_storage_migrations ADD COLUMN rebound_at timestamptz;
ALTER TABLE tenant_storage_migrations ADD COLUMN rollback_cursor_at timestamptz;
ALTER TABLE tenant_storage_migrations ADD COLUMN rollback_cursor_id uuid;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tenant_storage_migrations DROP COLUMN rollback_cursor_id;
ALTER TABLE tenant_storage_migrations DROP COLUMN rollback_cursor_at;
ALTER TABLE tenant_storage_migrations DROP COLUMN rebound_at;
-- +goose StatementEnd
