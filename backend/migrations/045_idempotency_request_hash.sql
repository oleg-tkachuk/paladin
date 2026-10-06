-- +goose Up
-- +goose StatementBegin

-- The fingerprint of the request each memoised response answered.
--
-- A response was cached per (tenant, method, key) and replayed for any later
-- call with that key — whatever that call asked. A client reusing one key
-- across several requests to the same method (the SDKs' context key does,
-- inside a download or multipart helper) got the first request's answer to
-- every later one: one object's download URL under every name, part 1's
-- presigned PUT for every part. Storing the fingerprint lets the interceptor
-- refuse a key reused for a different request instead of replaying.
--
-- Nullable: a row written before this column existed lives out its TTL
-- (24h) and is trusted as before. ADD COLUMN on the partitioned parent
-- reaches every partition.
ALTER TABLE idempotency_keys ADD COLUMN request_hash bytea;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE idempotency_keys DROP COLUMN IF EXISTS request_hash;

-- +goose StatementEnd
