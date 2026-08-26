-- +goose Up
-- +goose StatementBegin

-- Multipart sessions could disappear without anyone telling S3.
--
-- AbortMultipart is called from exactly two places: the Abort RPC and
-- MultipartReaper. Both work from the multipart_uploads row. But the row is
-- reachable by ON DELETE CASCADE from objects, and a cascade removes it
-- without calling anything: permanently deleting a PENDING object leaves the
-- S3-side session open forever, accruing part-storage charges, invisible to
-- the reaper that exists to close exactly that leak.
--
-- tenants cascades here too, though not independently — object_id is NOT NULL
-- and cascades as well, so clearing the objects that block a tenant always
-- fires this first. The trigger covers it regardless.
--
-- The bucket FK is RESTRICT, so the hazard was understood on that path. It
-- protects less than it looks: in the default `shared` storage layout a tenant
-- owns no buckets at all.
--
-- Same shape as pending_purges, for the same reason: a row that owes an
-- external side effect must not be deleted until the side effect happened.
-- The debt row records the obligation; MultipartAbortDrainer discharges it.

-- The debt row has to be self-sufficient. It is written by a trigger during a
-- cascade, and in the object-cascade case the parent object row is already
-- gone by the time the trigger fires — so a join for the key would come back
-- empty. Denormalise onto the session at initiation, where both are known.
ALTER TABLE multipart_uploads
    ADD COLUMN collection_name text NOT NULL DEFAULT '',
    ADD COLUMN path            text NOT NULL DEFAULT '';

UPDATE multipart_uploads m
SET    collection_name = k.name,
       path            = o.path
FROM   objects o
JOIN   collections k ON k.id = o.collection_id
WHERE  o.id = m.object_id
  AND  (m.collection_name = '' OR m.path = '');

CREATE TABLE pending_multipart_aborts (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- tenant_id / object_id carry no FK, deliberately and for the same reason
    -- pending_purges does not: the debt must outlive the rows whose deletion
    -- created it.
    tenant_id         uuid NOT NULL,
    object_id         uuid NOT NULL,
    -- bucket_id does carry one, RESTRICT: the drainer resolves the backend
    -- through it, so the bucket has to still be there when the debt is paid.
    bucket_id         uuid NOT NULL REFERENCES buckets(id) ON DELETE RESTRICT,
    collection_name   text NOT NULL,
    path              text NOT NULL,
    storage_upload_id text NOT NULL,
    attempts          integer NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz NOT NULL DEFAULT now(),
    last_error        text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    -- One debt per session. A cascade cannot fire twice for the same row, but
    -- a retry that re-inserts before the drainer commits would otherwise
    -- abort the same upload twice — harmless on S3, confusing in the table.
    UNIQUE (storage_upload_id)
);

CREATE INDEX pending_multipart_aborts_due_idx ON pending_multipart_aborts (next_attempt_at);

-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION multipart_uploads_record_abort_debt() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
DECLARE
    aborted text := current_setting('paladin.multipart_aborted', true);
BEGIN
    -- The two legitimate deleters — the Abort RPC and the reaper — call S3
    -- first and set this for their transaction, the same way the object-lock
    -- bypass is signalled. Everything else is a cascade, and a cascade owes
    -- an abort.
    IF COALESCE(aborted, '') = 'on' THEN
        RETURN OLD;
    END IF;

    INSERT INTO pending_multipart_aborts
        (tenant_id, object_id, bucket_id, collection_name, path, storage_upload_id)
    VALUES
        (OLD.tenant_id, OLD.object_id, OLD.bucket_id, OLD.collection_name, OLD.path, OLD.storage_upload_id)
    ON CONFLICT (storage_upload_id) DO NOTHING;

    RETURN OLD;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER multipart_uploads_abort_debt
    BEFORE DELETE ON multipart_uploads
    FOR EACH ROW EXECUTE FUNCTION multipart_uploads_record_abort_debt();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS multipart_uploads_abort_debt ON multipart_uploads;
DROP FUNCTION IF EXISTS multipart_uploads_record_abort_debt();
DROP TABLE IF EXISTS pending_multipart_aborts;
ALTER TABLE multipart_uploads DROP COLUMN IF EXISTS collection_name, DROP COLUMN IF EXISTS path;
-- +goose StatementEnd
