-- +goose Up
-- +goose StatementBegin

-- An object's placement and anchor become a schema guarantee (ADR-0051).
-- Every writer already sets them: a PENDING row is anchored to the bucket its
-- URL is signed for (upload_bucket_id) from its insert, and every promote
-- places the object where its bytes were confirmed (placement_bucket_id),
-- which a soft delete and a restore keep. Nothing reads a row placed nowhere
-- by its collection's binding any more.
--
-- The rows a binary from before 113 or 123 left without them hold their bytes
-- in their collection's bucket, as every row did then: that is where they are
-- placed, or anchored while PENDING. Both columns are Paladin's bookkeeping,
-- so the backfill keeps resource_version (129).
UPDATE objects o
   SET placement_bucket_id = c.bucket_id
  FROM collections c
 WHERE c.id = o.collection_id
   AND o.state <> 'PENDING'
   AND o.placement_bucket_id IS NULL;

UPDATE objects o
   SET upload_bucket_id = c.bucket_id
  FROM collections c
 WHERE c.id = o.collection_id
   AND o.state = 'PENDING'
   AND o.upload_bucket_id IS NULL;

-- NOT VALID: new and updated rows are checked from here, and 133 validates
-- the rest under a lock that does not block writes (CONVENTIONS.md). Added
-- after the backfill, so the scan above holds no table lock; a row inserted
-- in between comes from a binary that sets both.
ALTER TABLE objects ADD CONSTRAINT objects_placed_past_pending
    CHECK (state = 'PENDING' OR placement_bucket_id IS NOT NULL) NOT VALID;
ALTER TABLE objects ADD CONSTRAINT objects_pending_anchored
    CHECK (state <> 'PENDING' OR upload_bucket_id IS NOT NULL) NOT VALID;

-- A bucket an upload is anchored to holds that row as the placement does
-- (ON DELETE RESTRICT). SET NULL would now be refused by the CHECK for a
-- PENDING row, and on a promoted one would drop the settle of a URL that can
-- still write; DeleteBucket counts both columns before it deletes.
ALTER TABLE objects DROP CONSTRAINT objects_upload_bucket_fk;
ALTER TABLE objects ADD CONSTRAINT objects_upload_bucket_fk
    FOREIGN KEY (upload_bucket_id) REFERENCES buckets(id) ON DELETE RESTRICT NOT VALID;

-- +goose StatementEnd

-- +goose StatementBegin
-- Replication queues an object for the bucket it is placed in (126); every
-- AVAILABLE row is placed now, so the binding no longer stands in for it.
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
      JOIN buckets b ON b.id = NEW.placement_bucket_id
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

-- +goose Down

-- +goose StatementBegin
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
ALTER TABLE objects DROP CONSTRAINT objects_upload_bucket_fk;
ALTER TABLE objects ADD CONSTRAINT objects_upload_bucket_fk
    FOREIGN KEY (upload_bucket_id) REFERENCES buckets(id) ON DELETE SET NULL NOT VALID;
ALTER TABLE objects DROP CONSTRAINT objects_pending_anchored;
ALTER TABLE objects DROP CONSTRAINT objects_placed_past_pending;
-- +goose StatementEnd
