-- +goose Up
-- +goose StatementBegin

-- The key a capability is bound to (RFC 9449 DPoP): the RFC 7638 thumbprint
-- its token carries as cnf.jkt. Kept on the record because the admin plane
-- delegates from the stored parent, not from a token: a parent read back
-- without its binding would let it delegate an unbound child, and the
-- binding would be lost one step down the tree. NULL is an unbound
-- capability. A nullable column without a default: a metadata-only change.
ALTER TABLE capability_records ADD COLUMN confirmation_jkt text;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE capability_records DROP COLUMN IF EXISTS confirmation_jkt;
-- +goose StatementEnd
