-- +goose Up
-- +goose StatementBegin

-- A purge debt for the copy a storage migration made of an object, in the
-- migration's other bucket. While a tenant migrates, an object's bytes are
-- in its source and its target bucket alike; a purge deletes the row once, so
-- the transaction that deletes it records a debt for each bucket
-- (purgedebt.RecordMigrationCopies). The one for the bucket the collection
-- is bound to announces paladin.object.purged as before; this one is the
-- migration's own housekeeping and announces nothing, so a subscriber hears
-- of each purged object once.
--
-- A constant default: metadata only on Postgres 11 and later
-- (CONVENTIONS.md).
ALTER TABLE pending_purges ADD COLUMN migration_copy boolean NOT NULL DEFAULT false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE pending_purges DROP COLUMN migration_copy;
-- +goose StatementEnd
