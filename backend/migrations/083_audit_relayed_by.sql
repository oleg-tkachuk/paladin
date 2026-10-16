-- +goose Up

-- The OAuth client that relayed an audited call on its actor's behalf: the
-- MCP server, which exchanges each agent's token for the plane it calls and
-- names itself in the exchanged token's `act` claim (ADR-0036). The actor is
-- still the user; this says the call came through the MCP server. NULL for a
-- call made directly.
--
-- Nullable with no default, so this is a catalog change only (CONVENTIONS.md).
ALTER TABLE audit_log ADD COLUMN relayed_by text;

-- +goose Down
ALTER TABLE audit_log DROP COLUMN IF EXISTS relayed_by;
