-- +goose Up
-- +goose StatementBegin

-- Per-tenant aggregate spend cap.
--
-- Capability-level Budget (cfg.Caveats.MaxBudgetUSD on each issued
-- token) is fine-grained but doesn't aggregate: an attacker who can
-- mint many capabilities — or a sloppy operator who issues one big
-- caveat and reuses it — can outspend the tenant's intended budget.
-- This table is the umbrella: every capability charge under tenant T
-- also bumps tenant_budgets[T].spent_usd. When the aggregate exceeds
-- max_budget_usd, charges reject regardless of the per-capability
-- caveat budget.
--
-- Per-period semantics:
--
--   period_start / period_end frame the current accounting window.
--   Spend accumulates within the window and resets on the next sweep.
--   When max_budget_usd = 0, no cap is enforced (counter still
--   accumulates so admin tooling renders "current spend"); when the
--   row is missing, the tenant has no cap configured.
--
--   The reset is *operator-driven*: admin RPC SetTenantBudget can
--   roll the period forward (typical: monthly billing cycle). We
--   don't auto-roll on a cron because a missed roll is preferable
--   to an unwanted one — operators tie roll to their billing
--   close, not the DB clock.
--
-- numeric(14,6) matches capability_usage.spent_usd (migration 022).

CREATE TABLE tenant_budgets (
    tenant_id      UUID         PRIMARY KEY REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    max_budget_usd NUMERIC(14,6) NOT NULL DEFAULT 0,
    spent_usd      NUMERIC(14,6) NOT NULL DEFAULT 0,
    period_start   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    period_end     TIMESTAMPTZ,                         -- NULL = open-ended
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Sweep-by-period queries (admin tooling that lists "tenants near cap"):
CREATE INDEX idx_tenant_budgets_updated ON tenant_budgets (updated_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tenant_budgets_updated;
DROP TABLE IF EXISTS tenant_budgets;
-- +goose StatementEnd
