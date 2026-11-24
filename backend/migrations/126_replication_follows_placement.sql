-- +goose Up

-- +goose StatementBegin
-- Bucket replication copies the objects whose bytes are in a bucket
-- (objects.placement_bucket_id, 123, ADR-0051), not the objects of the
-- collections bound to it: the binding only decides where the next upload
-- goes. An object is queued for the bucket it is placed in, and queued again
-- when it moves to another; a row placed nowhere yet (PENDING) is not
-- queued. A row a binary from before 123 promoted has its bytes in its
-- collection's bucket.
CREATE OR REPLACE FUNCTION objects_queue_replica() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
DECLARE
    placed_bucket uuid;
    coll_name     text;
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

    SELECT b.id, c.name INTO placed_bucket, coll_name
      FROM collections c
      JOIN buckets b ON b.id = COALESCE(NEW.placement_bucket_id, c.bucket_id)
     WHERE c.id = NEW.collection_id
       AND b.replication_enabled;
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;

    INSERT INTO object_replicas
        (source_bucket_id, tenant_id, collection_name, path, object_id, next_attempt_at)
    VALUES (placed_bucket, NEW.tenant_id, coll_name, NEW.path, NEW.id, now())
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
-- A move to another bucket is a write the replica of that bucket lacks.
DROP TRIGGER objects_queue_replica_write ON objects;
CREATE TRIGGER objects_queue_replica_write
    AFTER INSERT OR UPDATE OF state, bytes_written_at, size_bytes, etag, content_type,
                              tags, metadata, external_ref, committed_at, placement_bucket_id
    ON objects
    FOR EACH ROW EXECUTE FUNCTION objects_queue_replica();
-- +goose StatementEnd

-- +goose StatementBegin
-- A collection rebound to another bucket moves no object: nothing is queued
-- for it, and the bucket is not seeded again.
DROP TRIGGER IF EXISTS collections_reseed_replication ON collections;
DROP FUNCTION IF EXISTS collections_reseed_replication();
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION collections_reseed_replication() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
BEGIN
    DELETE FROM replication_seeds WHERE bucket_id = NEW.bucket_id;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER collections_reseed_replication
    AFTER UPDATE OF bucket_id ON collections
    FOR EACH ROW
    WHEN (OLD.bucket_id IS DISTINCT FROM NEW.bucket_id)
    EXECUTE FUNCTION collections_reseed_replication();
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER objects_queue_replica_write ON objects;
CREATE TRIGGER objects_queue_replica_write
    AFTER INSERT OR UPDATE OF state, bytes_written_at, size_bytes, etag, content_type,
                              tags, metadata, external_ref, committed_at
    ON objects
    FOR EACH ROW EXECUTE FUNCTION objects_queue_replica();
-- +goose StatementEnd

-- +goose StatementBegin
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
