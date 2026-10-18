-- +goose Up

-- Who started an operation: the subject of the token that submitted it, which
-- for a person is their user id. The console's operations drawer shows the
-- signed-in user only their own, filtered here rather than after the fetch.
--
-- Operations from before this column were never attributed; they keep '' and
-- belong to no one's own list.
ALTER TABLE operations ADD COLUMN initiator_subject text NOT NULL DEFAULT '';

-- One user's operations, newest first, within their tenant.
CREATE INDEX idx_operations_tenant_initiator ON operations (tenant_id, initiator_subject, id);

-- +goose Down
DROP INDEX IF EXISTS idx_operations_tenant_initiator;
ALTER TABLE operations DROP COLUMN IF EXISTS initiator_subject;
