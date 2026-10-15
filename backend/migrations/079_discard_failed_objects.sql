-- +goose Up

-- A failed object is deleted when it fails (statemachine.DiscardFailed) rather
-- than kept as FAILED: the row told nobody anything, and it held its key, so a
-- retry at the same path was refused. This removes the rows kept before.
--
-- No bytes go with them. Every path that failed an object either deleted the
-- bytes first (a size or checksum mismatch) or never had any (a presign that
-- expired with nothing at its path, a failed copy, an aborted multipart
-- upload). A multipart session still attached cascades and records an abort
-- debt, which MultipartAbortDrainer pays.
DELETE FROM objects WHERE state = 'FAILED';

-- +goose Down
-- Nothing to restore: the rows are gone, and no code writes FAILED any more.
SELECT 1;
