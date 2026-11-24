-- +goose Up
-- +goose StatementBegin

-- A rolled-back storage migration keeps passing after it fails (ADR-0051):
-- a request that read the target binding before the switch back may anchor
-- an upload to the target, or bind a collection to it, after the rollback
-- ended. The passes point such a collection at the source again and move
-- such an object back once its anchor settles.
--
-- settle_after: the least time the rollback's passes go on after it failed.
-- Set by the rollback's end; NULL on a migration that never rolled back.
-- settled_at: when the passes stopped — nothing held the target past
-- settle_after — or, on a migration that failed before anything was pointed
-- at its target, when it failed. A failed migration with no settled_at is
-- still passed by the worker.
ALTER TABLE tenant_storage_migrations ADD COLUMN settle_after timestamptz;
ALTER TABLE tenant_storage_migrations ADD COLUMN settled_at timestamptz;

-- Failures from before this migration are taken as settled: the worker
-- passed none of them, and a retry moves on from wherever their objects are.
UPDATE tenant_storage_migrations
   SET settled_at = updated_at
 WHERE state = 'failed';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tenant_storage_migrations DROP COLUMN settled_at;
ALTER TABLE tenant_storage_migrations DROP COLUMN settle_after;
-- +goose StatementEnd
