-- +goose Up
-- +goose StatementBegin

-- charges is the per-event audit trail of capability charges. Today
-- the running totals on capability_usage / tenant_budgets are the
-- only spend record; queries like "spend per day" or "spend by op"
-- can't be answered without scanning the audit_log. This ledger
-- gives the billing surface a direct source of truth without
-- changing the existing tables.
--
-- INSERTed by internal/capability/usage.go.Charge() in the same
-- transaction that bumps capability_usage.spent_usd and
-- tenant_budgets.spent_usd, so the three records are always
-- consistent.
--
-- numeric(14,6) matches capability_usage / tenant_budgets so a
-- ledger SUM round-trips against the running totals exactly.

CREATE TABLE charges (
    id              UUID         PRIMARY KEY,
    tenant_id       UUID         NOT NULL REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    capability_id   UUID         NOT NULL REFERENCES capability_records(id) ON DELETE CASCADE,
    occurred_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    amount          NUMERIC(14,6) NOT NULL,
    unit_code       TEXT         NOT NULL,
    op              TEXT         NOT NULL DEFAULT '',     -- the capability op (get / put / list / ...)
    actor_subject   TEXT         NOT NULL DEFAULT ''      -- agent / user subject from the request context
);

-- Time-bucketed scans by tenant — the dominant access pattern.
CREATE INDEX charges_tenant_time_idx ON charges (tenant_id, occurred_at DESC);
-- Per-capability rollup queries.
CREATE INDEX charges_cap_time_idx ON charges (capability_id, occurred_at DESC);

-- RLS — tenant isolation, same as every other tenant-scoped table.
ALTER TABLE charges ENABLE ROW LEVEL SECURITY;
ALTER TABLE charges FORCE ROW LEVEL SECURITY;
CREATE POLICY charges_tenant_isolation ON charges
    USING (tenant_id = paladin_session_tenant_id());

GRANT SELECT, INSERT ON charges TO paladin_app;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS charges_cap_time_idx;
DROP INDEX IF EXISTS charges_tenant_time_idx;
DROP TABLE IF EXISTS charges;
-- +goose StatementEnd
