-- +goose Up
-- +goose StatementBegin

-- Add capability_id to audit_log so every audit row carries the
-- capability token (if any) that authorised the action. The agentic-
-- plane positioning calls for "every MCP / Connect call gets attributed
-- to the cap_id"; without this column that attribution requires a
-- request_id-keyed join across audit_log + capability_records, which is
-- expensive and fragile (request_id is optional in the existing schema).
--
-- NULL when the call was JWT- or API-token-authenticated (no capability
-- presented). middleware.Audit reads auth.CapabilityFromContext and
-- threads the ID through.
--
-- No FK to capability_records on purpose: capability rows are subject
-- to retention/purge cycles independent of audit retention. Audit must
-- survive even if the originating capability has been swept.

ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS capability_id uuid;

-- Per-capability audit rollup query lives on this index. Partial-on-
-- not-null keeps it small (most rows are JWT-authenticated and have
-- NULL capability_id).
CREATE INDEX IF NOT EXISTS idx_audit_log_capability
    ON audit_log (capability_id, at DESC)
    WHERE capability_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_audit_log_capability;
ALTER TABLE audit_log DROP COLUMN IF EXISTS capability_id;
-- +goose StatementEnd
