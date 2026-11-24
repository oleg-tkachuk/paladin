-- +goose Up
-- +goose StatementBegin

-- A storage migration's version, for the operator's compare-and-set
-- (CancelTenantStorageMigration): it moves with every change of state and
-- with a retry, never with a pass's progress, so a cancel read from a status
-- polled while objects move still applies, and one read before the worker
-- moved the migration on is refused.
ALTER TABLE tenant_storage_migrations
    ADD COLUMN resource_version bigint NOT NULL DEFAULT 1;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tenant_storage_migrations DROP COLUMN resource_version;
-- +goose StatementEnd
