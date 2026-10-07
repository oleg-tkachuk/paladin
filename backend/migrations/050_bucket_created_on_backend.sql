-- +goose Up
-- +goose StatementBegin

-- Whether Paladin creates the bucket on its backend (provision_on_backend), as
-- opposed to taking one that already existed under its management. Only such a
-- bucket may be deleted on the backend: an adopted one holds data Paladin never
-- wrote. Set when the row is written — a bucket is provisioned only after the
-- backend was seen not to hold it — and never changed. Existing rows start
-- false, since nothing recorded how they came to be.
ALTER TABLE buckets
    ADD COLUMN created_on_backend boolean NOT NULL DEFAULT false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE buckets DROP COLUMN created_on_backend;
-- +goose StatementEnd
