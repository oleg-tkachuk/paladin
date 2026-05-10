-- +goose Up
-- +goose StatementBegin
-- 033_tenant_identity_hardening.sql
--
-- Phase 0 of canonical-resource-names plan: lock down tenant identity.
--
-- Three fields, three policies:
--   tenant_id    UUID, immutable post-create (PK already enforces unique)
--   slug         text, immutable post-create, UNIQUE, NOT NULL (already
--                from migration 009 — this migration adds the immutability
--                trigger + a session-local escape hatch for the future
--                slug-rename RPC)
--   display_name text, EDITABLE, UNIQUE (NEW), NOT NULL (NEW), defaults
--                to slug at create time when empty (handled in API layer)
--
-- Rationale lives in backend/docs/canonical-resource-names.md "Phase 0".
--
-- The display_name backfill copies slug into any NULL/empty cells before
-- enforcing NOT NULL. The slug-rename RPC (BACKLOG) will set
-- `paladin.allow_slug_rename = on` at the start of its tx so the trigger lets
-- it through; default GUC is off so any other UPDATE to slug raises.

-- 1. display_name: backfill, then enforce NOT NULL + UNIQUE + format.
UPDATE tenants
   SET display_name = slug
 WHERE display_name IS NULL OR btrim(display_name) = '';

ALTER TABLE tenants
    ALTER COLUMN display_name SET NOT NULL;

ALTER TABLE tenants
    ADD CONSTRAINT tenants_display_name_format
        CHECK (char_length(display_name) BETWEEN 1 AND 255
               AND display_name = btrim(display_name));

ALTER TABLE tenants
    ADD CONSTRAINT tenants_display_name_unique UNIQUE (display_name);

-- 2. tenant_id + slug immutability trigger. PK already prevents
-- changing tenant_id via uniqueness, but the trigger makes the
-- intent explicit and gives a clear error message. The slug guard
-- is the real motivation: slug is UNIQUE but mutable at the SQL
-- level today, and Phase 1 onward starts persisting `tenant_id` in
-- canonical resource names — we don't want operators rotating slug
-- via raw SQL while audit log entries reference the old form.
--
-- Escape hatch: the future slug-rename RPC will run inside a tx
-- that does `SET LOCAL paladin.allow_slug_rename = on`. The trigger
-- reads that GUC; when on, slug changes are permitted (but the RPC
-- still has to do the cross-table policy rewrites in the same tx).
CREATE OR REPLACE FUNCTION tenants_block_immutable_columns()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    allow_rename text := current_setting('paladin.allow_slug_rename', true);
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id THEN
        RAISE EXCEPTION 'tenant_id is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.slug IS DISTINCT FROM OLD.slug THEN
        IF allow_rename IS DISTINCT FROM 'on' THEN
            RAISE EXCEPTION
                'slug is immutable; use RenameTenantSlug RPC'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tenants_immutable_columns
    BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION tenants_block_immutable_columns();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS tenants_immutable_columns ON tenants;
DROP FUNCTION IF EXISTS tenants_block_immutable_columns();

ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_display_name_unique;

ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_display_name_format;

ALTER TABLE tenants
    ALTER COLUMN display_name DROP NOT NULL;
-- +goose StatementEnd
