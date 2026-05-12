-- +goose Up
-- +goose StatementBegin
-- 036_soft_delete_tenants.sql
--
-- Soft-delete + trash recovery for tenants. Adds `deleted_at` column
-- (NULL = active, non-NULL = trashed at that timestamp). All Cedar /
-- audit / list / read queries continue against a partial-index'd
-- view of the active set; trashed rows live separately and are
-- recoverable for the retention window.
--
-- Why tenants first: this is the operator's biggest "oh shit, I
-- meant to delete the OTHER one" risk. Buckets and ObjectKeys have
-- physical S3 state that's harder to truly restore from soft-delete
-- semantics, so they stay hard-delete for now (BACKLOG'd).
--
-- The hard-delete TTL reaper lives in worker/housekeeping.go (already
-- has a clock for similar TTL tasks); a follow-up wires it to purge
-- tenants where `deleted_at < now() - <retention>` after the policy
-- is signed off. Default retention deferred to BACKLOG; rows live
-- forever in the trash until the reaper config lands.

ALTER TABLE tenants
    ADD COLUMN deleted_at timestamptz;

-- Partial index keeps the active-set scans cheap; trashed rows skip.
CREATE INDEX idx_tenants_active
    ON tenants (tenant_id)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_tenants_trashed
    ON tenants (deleted_at)
    WHERE deleted_at IS NOT NULL;

-- The display_name + slug UNIQUE constraints from migration 009/033
-- continue to apply across BOTH active and trashed sets — restoring
-- a trashed tenant whose slug was reused since deletion would trip
-- the unique constraint. That's intentional: forces the operator to
-- rename one side before restore. Alternative (partial UNIQUE) is
-- BACKLOG.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tenants_trashed;
DROP INDEX IF EXISTS idx_tenants_active;
ALTER TABLE tenants
    DROP COLUMN IF EXISTS deleted_at;
-- +goose StatementEnd
