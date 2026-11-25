-- +goose Up
-- +goose StatementBegin

-- A finished storage migration no longer holds its buckets (ADR-0051).
--
-- Both bucket references were ON DELETE RESTRICT, in every state, and the row
-- goes only with its tenant (ON DELETE CASCADE). A tenant that migrated owns
-- its dedicated bucket, so it could not be purged until that bucket was
-- deleted, and the bucket could not be deleted while the migration row named
-- it: a cycle. The shared source bucket stayed pinned by every tenant that
-- ever migrated off it.
--
-- Now a reference goes NULL with its bucket, and only a migration the worker
-- still drives refuses the delete: provisioning, moving, completed and
-- rolling_back (migstate.HoldsBuckets). A failed or cleaned one is done with
-- its buckets; anything a failed one still passes back is an object placed,
-- an upload anchored or a collection bound there, each of which holds the
-- bucket itself.
ALTER TABLE tenant_storage_migrations
    ALTER COLUMN source_bucket_id DROP NOT NULL,
    ALTER COLUMN target_bucket_id DROP NOT NULL,
    DROP CONSTRAINT tenant_storage_migrations_source_bucket_id_fkey,
    DROP CONSTRAINT tenant_storage_migrations_target_bucket_id_fkey,
    ADD CONSTRAINT tenant_storage_migrations_source_bucket_id_fkey
        FOREIGN KEY (source_bucket_id) REFERENCES buckets(id) ON DELETE SET NULL,
    ADD CONSTRAINT tenant_storage_migrations_target_bucket_id_fkey
        FOREIGN KEY (target_bucket_id) REFERENCES buckets(id) ON DELETE SET NULL;

-- The refusal for a migration the worker still drives, raised as the foreign
-- key's own SQLSTATE so a delete that races the handler's reference count is
-- mapped as any RESTRICT refusal is. tenant_storage_migrations is under FORCE
-- ROW LEVEL SECURITY and a bucket delete runs scoped to no tenant or another,
-- so the function runs as its owner, which bypasses it (as 061's guard does);
-- it only reads, with the search_path pinned.
CREATE OR REPLACE FUNCTION enforce_buckets_storage_migration_guard() RETURNS trigger
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
DECLARE
    tenant uuid;
    state  text;
BEGIN
    SELECT m.tenant_id, m.state INTO tenant, state
      FROM public.tenant_storage_migrations m
     WHERE (m.source_bucket_id = OLD.id OR m.target_bucket_id = OLD.id)
       AND m.state IN ('provisioning', 'moving', 'completed', 'rolling_back')
     LIMIT 1;
    IF FOUND THEN
        RAISE EXCEPTION
            'bucket % is used by the storage migration of tenant %, which is %',
            OLD.id, tenant, state
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    RETURN OLD;
END
$$;

REVOKE ALL ON FUNCTION enforce_buckets_storage_migration_guard() FROM PUBLIC;

DROP TRIGGER IF EXISTS buckets_storage_migration_guard ON buckets;
CREATE TRIGGER buckets_storage_migration_guard
    BEFORE DELETE ON buckets
    FOR EACH ROW EXECUTE FUNCTION enforce_buckets_storage_migration_guard();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS buckets_storage_migration_guard ON buckets;
DROP FUNCTION IF EXISTS enforce_buckets_storage_migration_guard();
DELETE FROM tenant_storage_migrations WHERE source_bucket_id IS NULL OR target_bucket_id IS NULL;
ALTER TABLE tenant_storage_migrations
    DROP CONSTRAINT tenant_storage_migrations_source_bucket_id_fkey,
    DROP CONSTRAINT tenant_storage_migrations_target_bucket_id_fkey,
    ADD CONSTRAINT tenant_storage_migrations_source_bucket_id_fkey
        FOREIGN KEY (source_bucket_id) REFERENCES buckets(id) ON DELETE RESTRICT,
    ADD CONSTRAINT tenant_storage_migrations_target_bucket_id_fkey
        FOREIGN KEY (target_bucket_id) REFERENCES buckets(id) ON DELETE RESTRICT,
    ALTER COLUMN source_bucket_id SET NOT NULL,
    ALTER COLUMN target_bucket_id SET NOT NULL;
-- +goose StatementEnd
