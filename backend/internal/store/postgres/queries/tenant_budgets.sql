-- Tenant aggregate budget queries.

-- name: SetTenantBudget :one
-- Upserts the cap and rolls the period. Operators call this from
-- admin tooling on every billing cycle; spent_usd is reset to 0
-- when reset_spend = true (idiomatic monthly close), preserved
-- otherwise (mid-cycle adjustment that just changes the cap).
INSERT INTO tenant_budgets (tenant_id, max_budget_usd, spent_usd, period_start, period_end, updated_at)
VALUES ($1, sqlc.arg('max_budget_usd')::numeric, 0, now(), sqlc.narg('period_end')::timestamptz, now())
ON CONFLICT (tenant_id) DO UPDATE
SET max_budget_usd = EXCLUDED.max_budget_usd,
    spent_usd      = CASE WHEN sqlc.arg('reset_spend')::boolean THEN 0 ELSE tenant_budgets.spent_usd END,
    period_start   = CASE WHEN sqlc.arg('reset_spend')::boolean THEN now() ELSE tenant_budgets.period_start END,
    period_end     = COALESCE(sqlc.narg('period_end')::timestamptz, tenant_budgets.period_end),
    updated_at     = now()
RETURNING tenant_id, max_budget_usd, spent_usd, period_start, period_end, updated_at;

-- name: GetTenantBudget :one
SELECT tenant_id, max_budget_usd, spent_usd, period_start, period_end, updated_at
FROM tenant_budgets
WHERE tenant_id = $1;

-- name: ChargeTenantBudget :one
-- Atomic UPSERT-and-check, same shape as ChargeCapability. When the
-- row is missing, treats max as 0 (no cap enforced) and inserts a
-- fresh accumulator row. Returns the new spent_usd; pgx.ErrNoRows
-- means "would exceed cap" — caller maps to ErrTenantBudgetExceeded.
INSERT INTO tenant_budgets (tenant_id, max_budget_usd, spent_usd, period_start, updated_at)
VALUES ($1, 0, sqlc.arg('amount_usd')::numeric, now(), now())
ON CONFLICT (tenant_id) DO UPDATE
SET spent_usd  = tenant_budgets.spent_usd + sqlc.arg('amount_usd')::numeric,
    updated_at = now()
WHERE
    tenant_budgets.max_budget_usd = 0
    OR tenant_budgets.spent_usd + sqlc.arg('amount_usd')::numeric <= tenant_budgets.max_budget_usd
RETURNING spent_usd;

-- name: RefundTenantBudget :exec
-- Subtracts amount; floors at 0 so a refund larger than current
-- spend doesn't go negative (which would silently grant the
-- difference back as future budget).
UPDATE tenant_budgets
SET spent_usd  = GREATEST(0, spent_usd - sqlc.arg('amount_usd')::numeric),
    updated_at = now()
WHERE tenant_id = $1;

-- name: RefundCapabilityUsage :exec
-- Symmetric refund on the per-capability counter. Same floor rule.
UPDATE capability_usage
SET spent_usd  = GREATEST(0, spent_usd - sqlc.arg('amount_usd')::numeric),
    updated_at = now()
WHERE capability_id = $1;
