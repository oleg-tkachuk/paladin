-- +goose Up
-- +goose StatementBegin

-- multipart_parts was a journal of uploaded parts that nothing could write.
--
-- Parts are PUT straight to the object store through presigned URLs, so the
-- control plane never observes one. A journal here could only ever record what
-- Paladin authorised, never what the client managed to store — and those differ
-- in exactly the case the journal existed to serve: a client resuming an
-- interrupted upload. ListParts now asks the backend, which is the only party
-- that knows.
--
-- The table's writer (RecordPart) and both its queries are already gone. Its
-- RLS policy goes with it — the policy was correct, the table was not needed.

DROP TABLE IF EXISTS multipart_parts;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
-- Recreating the table would recreate a structure nothing writes. The
-- rollback is deliberately empty: this is a removal, not a toggle.
SELECT 1;
-- +goose StatementEnd
