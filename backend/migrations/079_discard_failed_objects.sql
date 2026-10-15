-- +goose Up

-- A failed object is deleted when it fails (statemachine.DiscardFailed) rather
-- than kept as FAILED: the row told nobody anything, and it held its key, so a
-- retry at the same path was refused. This removes the rows kept before and
-- drops FAILED from object_state.
--
-- No bytes go with them. Every path that failed an object either deleted the
-- bytes first (a size or checksum mismatch) or never had any (a presign that
-- expired with nothing at its path, a failed copy, an aborted multipart
-- upload). A multipart session still attached cascades and records an abort
-- debt, which MultipartAbortDrainer pays.
--
-- Postgres cannot drop an enum label, so the type is replaced: objects.state
-- is cast to a new object_state, which rewrites the table under ACCESS
-- EXCLUSIVE. A deliberate exception to CONVENTIONS.md's rule against rewrites
-- in a migration: the contract is pre-1.0 and every deployment is small enough
-- to take it. The two partial indexes compare state with a label of the old
-- type, so they are dropped first and rebuilt on the new one. search_object_ids
-- names the type only inside its body (078), resolved when it runs.
DELETE FROM objects WHERE state = 'FAILED';

DROP INDEX idx_objects_hard_deletable;
DROP INDEX objects_presign_expiry_idx;

ALTER TYPE object_state RENAME TO object_state_with_failed;
CREATE TYPE object_state AS ENUM ('PENDING', 'AVAILABLE', 'DELETED');
ALTER TABLE objects
    ALTER COLUMN state TYPE object_state USING state::text::object_state;
DROP TYPE object_state_with_failed;

CREATE INDEX idx_objects_hard_deletable
    ON objects (terminated_at) WHERE state = 'DELETED';
CREATE INDEX objects_presign_expiry_idx
    ON objects (presign_expires_at) WHERE state = 'PENDING';

-- +goose Down
DROP INDEX idx_objects_hard_deletable;
DROP INDEX objects_presign_expiry_idx;

ALTER TYPE object_state RENAME TO object_state_without_failed;
CREATE TYPE object_state AS ENUM ('PENDING', 'AVAILABLE', 'FAILED', 'DELETED');
ALTER TABLE objects
    ALTER COLUMN state TYPE object_state USING state::text::object_state;
DROP TYPE object_state_without_failed;

CREATE INDEX idx_objects_hard_deletable
    ON objects (terminated_at) WHERE state = 'DELETED';
CREATE INDEX objects_presign_expiry_idx
    ON objects (presign_expires_at) WHERE state = 'PENDING';
