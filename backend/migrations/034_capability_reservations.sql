-- +goose Up
-- +goose StatementBegin

-- Budget reservations: a hold placed before a cost is known, settled with the
-- actual cost afterwards, and released on its own if never settled.
--
-- Holds are counted in reserved_usd beside spent_usd, on the capability's
-- usage row, on each ancestor's, and on the tenant's budget row, and every
-- ceiling check adds the two. Counters rather than a SUM over open
-- reservations, because the counter moves under the same row lock the
-- ceiling check takes: a SUM subquery is read from the statement's snapshot,
-- so two concurrent reservations could each miss the other's hold.
--
-- Both columns take a constant default, which Postgres ≥ 11 applies as a
-- metadata change, without rewriting the table (CONVENTIONS.md).
ALTER TABLE capability_usage ADD COLUMN reserved_usd numeric(14,6) NOT NULL DEFAULT 0;
ALTER TABLE tenant_budgets   ADD COLUMN reserved_usd numeric(14,6) NOT NULL DEFAULT 0;

-- One row per open reservation. Settling, releasing or expiring deletes the
-- row and subtracts its amount from every counter it was added to; the
-- ancestors are re-derived from capability_records, which never changes a
-- parent_id after insert. A new, empty table, so its index is built in the
-- same transaction.
CREATE TABLE capability_reservations (
    id            uuid PRIMARY KEY,
    tenant_id     uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    capability_id uuid NOT NULL REFERENCES capability_records(id) ON DELETE CASCADE,
    amount        numeric(14,6) NOT NULL CHECK (amount >= 0),
    unit_code     text NOT NULL,
    op            text NOT NULL DEFAULT '',
    actor_subject text NOT NULL DEFAULT '',
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX capability_reservations_expiry_idx ON capability_reservations (expires_at);

-- The standard tenant isolation (002's procedure body, spelled out because
-- 002 drops the procedure): the reaper reads across tenants under the
-- cross-tenant flag, and every write stays pinned to one tenant.
ALTER TABLE capability_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE capability_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON capability_reservations FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS capability_reservations;
ALTER TABLE tenant_budgets   DROP COLUMN IF EXISTS reserved_usd;
ALTER TABLE capability_usage DROP COLUMN IF EXISTS reserved_usd;
-- +goose StatementEnd
