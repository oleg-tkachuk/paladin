-- +goose Up
-- +goose StatementBegin

-- A tenant has one storage migration. Nothing enforced it: tenant_id carried
-- an ordinary index, and before StartStorageMigration took the tenant lock a
-- start naming another target backend inserted a second row. Every reader
-- since takes the latest (GetStorageMigration, LatestStorageMigration), but
-- the worker drives every non-terminal row, so an older duplicate could still
-- be copied, rebound and cleaned up behind the one the API reports.
--
-- Keep the latest row per tenant — the one the API and a retry already treat
-- as the tenant's migration — and delete the rest, so 063 can make tenant_id
-- unique. The id breaks a tie on created_at. Deletes only; the table is small
-- (one row per tenant that ever migrated), so this runs in one statement.
DELETE FROM tenant_storage_migrations m
USING (
    SELECT id,
           row_number() OVER (PARTITION BY tenant_id
                              ORDER BY created_at DESC, id DESC) AS rn
      FROM tenant_storage_migrations
) ranked
WHERE m.id = ranked.id AND ranked.rn > 1;

-- +goose StatementEnd

-- +goose Down
-- The deleted duplicates are not restored: they were never the tenant's
-- migration as any reader saw it.
SELECT 1;
