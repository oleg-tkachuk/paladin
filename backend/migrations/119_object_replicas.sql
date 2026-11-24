-- +goose Up
-- +goose StatementBegin

-- Bucket replication keeps state per object instead of a per-bucket
-- watermark (ADR-0050). The watermark was a committed_at timestamp: one object
-- that failed for good held it and made every later pass copy the objects
-- after it again, every pass read every object of the bucket's collections,
-- a byte write under an AVAILABLE row kept its old stamp and was never
-- copied, and a promote whose transaction outlasted the settle delay could be
-- passed over.
--
-- One row per replicated location: the source bucket and the tenant,
-- collection and key the replica lands at. A row is the work queue and the
-- record at once. A write that the replica may not reflect — a promote, a
-- change to a field the bucket's filter reads, a restore, the object's purge —
-- marks the row due (next_attempt_at) in the same transaction as the write,
-- through the triggers below, so no clock and no window decide what the
-- worker sees. The worker clears it once the destination matches the source.
--
-- Keyed by location, not by object: a purged object and a new one uploaded at
-- its key share the row, so the purge's replica delete and the new object's
-- copy are one queue entry, taken by one worker at a time, and a late delete
-- can never remove the new object's replica.
--
-- tenant_id, collection_name and path are denormalised and object_id carries
-- no foreign key, as in pending_purges: the row must outlive the object whose
-- purge it records.
CREATE TABLE object_replicas (
    source_bucket_id uuid        NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
    tenant_id        uuid        NOT NULL,
    collection_name  text        NOT NULL,
    path             text        NOT NULL,
    -- The object last written at this location: the one to copy, or the
    -- purged one whose replica is deleted.
    object_id        uuid        NOT NULL,
    -- Bumped by every write that marks the row due. The worker settles a row
    -- only at the generation it read, so a write during a copy is copied by
    -- a later pass rather than cleared by this one.
    generation       bigint      NOT NULL DEFAULT 1,
    -- When the row is next due; NULL when the destination matches the
    -- source as far as the worker knows.
    next_attempt_at  timestamptz,
    -- A worker's claim: no other worker takes the row before this.
    claimed_until    timestamptz,
    -- Failed attempts since the last write or success, and the last failure.
    attempts         integer     NOT NULL DEFAULT 0,
    last_error       text,
    -- What the destination holds, as the worker wrote it: the destination
    -- bucket, the object copied, and the byte write it copied
    -- (COALESCE(bytes_written_at, committed_at, created_at) on the object
    -- row). NULL when replication wrote no replica here.
    replica_bucket_id  uuid REFERENCES buckets(id) ON DELETE SET NULL,
    replica_object_id  uuid,
    replica_written_at timestamptz,
    replica_size_bytes bigint,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_bucket_id, tenant_id, collection_name, path)
);

-- What the worker claims: a bucket's due rows, oldest first. Partial, so a
-- settled bucket costs nothing.
CREATE INDEX object_replicas_due_idx
    ON object_replicas (source_bucket_id, next_attempt_at)
    WHERE next_attempt_at IS NOT NULL;

-- What a purge marks: the rows its object owns.
CREATE INDEX object_replicas_object_idx ON object_replicas (object_id);

ALTER TABLE object_replicas ENABLE ROW LEVEL SECURITY;
ALTER TABLE object_replicas FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON object_replicas FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- The buckets whose existing objects the worker has queued. A bucket's
-- objects are queued once, when the worker first meets it with replication
-- enabled; from then on the triggers queue every write. A change to its
-- replication settings drops the row, so the worker queues the whole bucket
-- again: a new destination holds none of it, and a new filter may select
-- objects the old one passed over.
CREATE TABLE replication_seeds (
    bucket_id  uuid        PRIMARY KEY REFERENCES buckets(id) ON DELETE CASCADE,
    seeded_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose StatementBegin
-- Marks an object's replica location due. An INSERT or UPDATE that leaves
-- the row AVAILABLE in a bucket with replication enabled queues it, owned by
-- this object; a DELETE marks only the rows this object still owns, so a
-- purge never claims a location a newer object took over. A move to the
-- trash does neither: the source still holds the bytes, and so does the
-- replica.
CREATE OR REPLACE FUNCTION objects_queue_replica() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
DECLARE
    bound_bucket uuid;
    coll_name    text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        UPDATE object_replicas
           SET generation      = generation + 1,
               next_attempt_at = now(),
               attempts        = 0,
               last_error      = NULL,
               updated_at      = now()
         WHERE object_id = OLD.id;
        RETURN OLD;
    END IF;

    IF NEW.state <> 'AVAILABLE' THEN
        RETURN NEW;
    END IF;

    SELECT c.bucket_id, c.name INTO bound_bucket, coll_name
      FROM collections c
      JOIN buckets b ON b.id = c.bucket_id
     WHERE c.id = NEW.collection_id
       AND b.replication_enabled;
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;

    INSERT INTO object_replicas
        (source_bucket_id, tenant_id, collection_name, path, object_id, next_attempt_at)
    VALUES (bound_bucket, NEW.tenant_id, coll_name, NEW.path, NEW.id, now())
    ON CONFLICT (source_bucket_id, tenant_id, collection_name, path) DO UPDATE
       SET object_id       = EXCLUDED.object_id,
           generation      = object_replicas.generation + 1,
           next_attempt_at = now(),
           attempts        = 0,
           last_error      = NULL,
           updated_at      = now();
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The columns a replica or the bucket's filter depends on: the state, the
-- bytes (bytes_written_at moves with every promote, statemachine promote),
-- and every field a replication filter reads (cel.ObjectVars). An update of
-- anything else — a lock, a taint, a public URL — leaves the replica as it
-- is.
CREATE TRIGGER objects_queue_replica_write
    AFTER INSERT OR UPDATE OF state, bytes_written_at, size_bytes, etag, content_type,
                              tags, metadata, external_ref, committed_at
    ON objects
    FOR EACH ROW EXECUTE FUNCTION objects_queue_replica();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER objects_queue_replica_delete
    AFTER DELETE ON objects
    FOR EACH ROW EXECUTE FUNCTION objects_queue_replica();
-- +goose StatementEnd

-- +goose StatementBegin
-- A change to a bucket's replication settings has the worker queue its
-- objects again (replication_seeds).
CREATE OR REPLACE FUNCTION buckets_reseed_replication() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
BEGIN
    DELETE FROM replication_seeds WHERE bucket_id = NEW.id;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER buckets_reseed_replication
    AFTER UPDATE OF replication_enabled, replication_destination, replication_filter
    ON buckets
    FOR EACH ROW
    WHEN (OLD.replication_enabled IS DISTINCT FROM NEW.replication_enabled
          OR OLD.replication_destination IS DISTINCT FROM NEW.replication_destination
          OR OLD.replication_filter IS DISTINCT FROM NEW.replication_filter)
    EXECUTE FUNCTION buckets_reseed_replication();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS buckets_reseed_replication ON buckets;
DROP FUNCTION IF EXISTS buckets_reseed_replication();
DROP TRIGGER IF EXISTS objects_queue_replica_delete ON objects;
DROP TRIGGER IF EXISTS objects_queue_replica_write ON objects;
DROP FUNCTION IF EXISTS objects_queue_replica();
DROP TABLE IF EXISTS replication_seeds;
DROP TABLE IF EXISTS object_replicas;
-- +goose StatementEnd
