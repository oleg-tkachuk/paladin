-- +goose Up
-- +goose StatementBegin

-- Refunds against recorded charges. A refund used to be two bare counter
-- decrements, tied to nothing: calling it twice took the spend down twice, and
-- the ledger never learned that money came back. Now a refund names the charge
-- it returns, and the total refunded from a charge can never exceed the charge.
--
-- A table of its own rather than a column on charges, because charges is the
-- billing ledger and is append-only by policy (002: SELECT and INSERT, no
-- UPDATE). Refunds are appended the same way.
--
-- The table is new and empty, so its index is created in the same transaction;
-- CONCURRENTLY (CONVENTIONS.md) is for indexes on tables that already carry
-- traffic.
CREATE TABLE charge_refunds (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    charge_id   uuid NOT NULL REFERENCES charges(id) ON DELETE CASCADE,
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    amount      numeric(14,6) NOT NULL CHECK (amount > 0),
    refunded_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX charge_refunds_charge_idx ON charge_refunds (charge_id);

-- Same shape as charges: a tenant reads and appends its own, nobody rewrites.
ALTER TABLE charge_refunds ENABLE ROW LEVEL SECURITY;
ALTER TABLE charge_refunds FORCE ROW LEVEL SECURITY;
CREATE POLICY charge_refunds_tenant_isolation ON charge_refunds FOR SELECT
    USING (tenant_id = paladin_session_tenant_id());
CREATE POLICY charge_refunds_tenant_insert ON charge_refunds FOR INSERT
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS charge_refunds;
-- +goose StatementEnd
