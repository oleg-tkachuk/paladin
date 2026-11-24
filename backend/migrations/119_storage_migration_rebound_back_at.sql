-- +goose Up
-- +goose StatementBegin

-- When a storage migration's rollback rebound the tenant's collections back
-- to the source: the rebind back's transaction start, as rebound_at is the
-- rebind's (ADR-0015). An overwrite written to the target while the
-- migration rolled back, whose storage event is delivered after the rebind
-- back, is moved into the source when the object's row records no write
-- since this time. NULL before a rebind back, and again once a retry starts
-- the migration over.
ALTER TABLE tenant_storage_migrations ADD COLUMN rebound_back_at timestamptz;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE tenant_storage_migrations DROP COLUMN rebound_back_at;

-- +goose StatementEnd
