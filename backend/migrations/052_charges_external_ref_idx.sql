-- +goose NO TRANSACTION
-- +goose Up

-- One charge per (capability, external_ref): the backstop behind the advisory
-- lock Charge takes on the same pair. Partial, since most charges carry no
-- name. Concurrently, in a file of its own (CONVENTIONS.md).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS charges_capability_external_ref_key
    ON charges (capability_id, external_ref)
    WHERE external_ref IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS charges_capability_external_ref_key;
