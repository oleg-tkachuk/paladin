-- +goose Up

-- after_json held what an action was asked to do (an RPC's request, an OAuth
-- endpoint's, a storage event), never the resource's state after it; its name
-- said otherwise. before_json was never written. Both are catalog
-- changes only.
ALTER TABLE audit_log RENAME COLUMN after_json TO request_json;
ALTER TABLE audit_log DROP COLUMN before_json;

-- +goose Down
ALTER TABLE audit_log ADD COLUMN before_json bytea;
ALTER TABLE audit_log RENAME COLUMN request_json TO after_json;
