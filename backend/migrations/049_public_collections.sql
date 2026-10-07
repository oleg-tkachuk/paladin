-- +goose Up
-- +goose StatementBegin

-- Public collections (ADR-0027). A public bucket serves every object it holds
-- to unsigned GETs; a public collection is one bound to such a bucket. Both
-- flags are set at creation and never change, and a collection's must equal
-- its bucket's: the bucket boundary is the isolation, so a private collection
-- must never land in a public bucket, nor a public one move out of it.
--
-- The triggers name the rule they enforce as the error's constraint, so the
-- application tells them apart without reading the message.
--
-- public_base_url is the address a CDN serves the bucket at; '' means the
-- backend's public_endpoint. cache_control is what every object of a public
-- collection is stored with; a private collection has none.
ALTER TABLE buckets
    ADD COLUMN public_read     boolean NOT NULL DEFAULT false,
    ADD COLUMN public_base_url text    NOT NULL DEFAULT '';

ALTER TABLE collections
    ADD COLUMN public_read   boolean NOT NULL DEFAULT false,
    ADD COLUMN cache_control text    NOT NULL DEFAULT '',
    ADD CONSTRAINT collections_cache_control_public_only
        CHECK (public_read OR cache_control = '');

-- An object's public URL, built once when the object is created in a public
-- collection and fixed for its life: it is what consumers store. '' for every
-- other object.
ALTER TABLE objects ADD COLUMN public_url text NOT NULL DEFAULT '';

CREATE OR REPLACE FUNCTION buckets_public_read_immutable() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
BEGIN
    IF NEW.public_read IS DISTINCT FROM OLD.public_read
       OR NEW.public_base_url IS DISTINCT FROM OLD.public_base_url THEN
        RAISE EXCEPTION 'bucket %: public_read and public_base_url are fixed at creation', OLD.id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'buckets_public_read_fixed';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER buckets_public_read_immutable
    BEFORE UPDATE OF public_read, public_base_url ON buckets
    FOR EACH ROW EXECUTE FUNCTION buckets_public_read_immutable();

CREATE OR REPLACE FUNCTION collections_enforce_bucket_visibility() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
DECLARE
    bucket_public boolean;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.public_read IS DISTINCT FROM OLD.public_read
           OR NEW.cache_control IS DISTINCT FROM OLD.cache_control THEN
            RAISE EXCEPTION 'collection %: public_read and cache_control are fixed at creation', OLD.name
                USING ERRCODE = 'check_violation', CONSTRAINT = 'collections_public_read_fixed';
        END IF;
        -- Its objects' URLs name the bucket.
        IF OLD.public_read AND NEW.bucket_id IS DISTINCT FROM OLD.bucket_id THEN
            RAISE EXCEPTION 'public collection % may not move to another bucket', OLD.name
                USING ERRCODE = 'check_violation', CONSTRAINT = 'collections_public_bucket_fixed';
        END IF;
    END IF;
    SELECT public_read INTO bucket_public FROM buckets WHERE id = NEW.bucket_id;
    IF NOT FOUND THEN
        -- No such bucket: the NOT NULL or the foreign key says so, in its own
        -- words, after this trigger.
        RETURN NEW;
    END IF;
    IF bucket_public <> NEW.public_read THEN
        RAISE EXCEPTION 'collection % (public_read=%) may not bind to bucket % (public_read=%)',
            NEW.name, NEW.public_read, NEW.bucket_id, bucket_public
            USING ERRCODE = 'check_violation', CONSTRAINT = 'collections_bucket_visibility';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER collections_enforce_bucket_visibility
    BEFORE INSERT OR UPDATE OF bucket_id, public_read, cache_control ON collections
    FOR EACH ROW EXECUTE FUNCTION collections_enforce_bucket_visibility();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE objects DROP COLUMN public_url;
DROP TRIGGER collections_enforce_bucket_visibility ON collections;
DROP FUNCTION collections_enforce_bucket_visibility();
DROP TRIGGER buckets_public_read_immutable ON buckets;
DROP FUNCTION buckets_public_read_immutable();
ALTER TABLE collections
    DROP CONSTRAINT collections_cache_control_public_only,
    DROP COLUMN cache_control,
    DROP COLUMN public_read;
ALTER TABLE buckets
    DROP COLUMN public_base_url,
    DROP COLUMN public_read;
-- +goose StatementEnd
