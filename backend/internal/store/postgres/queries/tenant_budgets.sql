-- Tenant aggregate budget queries.
--
-- Naming dichotomy: SQL columns retain `_usd` suffixes for historical
-- reasons; unit_code (the schema baseline (001_initial_schema.sql)) is the source of truth for the
-- currency interpretation. Go domain types use Amount + UnitCode.

-- name: SetTenantBudget :one
-- Upserts the cap and rolls the period. Operators call this from
-- admin tooling on every billing cycle; spent_usd is reset to 0
-- when reset_spend = true (idiomatic monthly close), preserved
-- otherwise (mid-cycle adjustment that just changes the cap).
--
-- unit_code is written verbatim on insert; on conflict, it's
-- updated only when the caller passes a non-empty value (an empty
-- arg keeps the existing currency unchanged — operators editing
-- the cap shouldn't accidentally reinterpret an EUR budget as USD).
--
-- OCC on update, no guard on insert. The DO UPDATE's WHERE is the
-- concurrency check: it fires only when the stored resource_version
-- matches what the caller read. A mismatch — including a caller that
-- passed 0 believing no row existed — updates nothing and returns no
-- row, which the adapter maps to a version conflict. Without it two
-- operators editing the same cap silently overwrote each other.
INSERT INTO tenant_budgets (tenant_id, max_budget_usd, spent_usd, unit_code, period_start, period_end, updated_at)
VALUES ($1, sqlc.arg('max_budget_usd')::numeric, 0, COALESCE(NULLIF(sqlc.arg('unit_code')::text, ''), 'USD'), now(), sqlc.narg('period_end')::timestamptz, now())
ON CONFLICT (tenant_id) DO UPDATE
SET max_budget_usd = EXCLUDED.max_budget_usd,
    spent_usd      = CASE WHEN sqlc.arg('reset_spend')::boolean THEN 0 ELSE tenant_budgets.spent_usd END,
    period_start   = CASE WHEN sqlc.arg('reset_spend')::boolean THEN now() ELSE tenant_budgets.period_start END,
    period_end     = COALESCE(sqlc.narg('period_end')::timestamptz, tenant_budgets.period_end),
    unit_code      = COALESCE(NULLIF(sqlc.arg('unit_code')::text, ''), tenant_budgets.unit_code),
    resource_version = tenant_budgets.resource_version + 1,
    updated_at     = now()
WHERE tenant_budgets.resource_version = sqlc.arg('expected_version')::bigint
RETURNING tenant_id, max_budget_usd, spent_usd, unit_code, period_start, period_end, updated_at, resource_version;

-- name: GetTenantBudget :one
SELECT tenant_id, max_budget_usd, spent_usd, unit_code, period_start, period_end, updated_at, resource_version
FROM tenant_budgets
WHERE tenant_id = $1;

-- name: ChargeTenantBudget :one
-- Atomic UPSERT-and-check, same shape as ChargeCapability. When the
-- row is missing, treats max as 0 (no cap enforced) and inserts a
-- fresh accumulator row with the supplied unit_code (defaults to
-- 'USD' when empty). Returns the new spent_usd; pgx.ErrNoRows
-- means "would exceed cap" — caller maps to ErrTenantBudgetExceeded.
INSERT INTO tenant_budgets (tenant_id, max_budget_usd, spent_usd, unit_code, period_start, updated_at)
VALUES ($1, 0, sqlc.arg('amount_usd')::numeric, COALESCE(NULLIF(sqlc.arg('unit_code')::text, ''), 'USD'), now(), now())
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

-- name: ListTenantBudgetSummaries :many
-- Cross-tenant join of tenant_budgets ⨝ tenants. Returns slug +
-- display_name so the dashboard's BudgetAlerts widget doesn't need a
-- follow-up read.
--
-- Predicate semantics:
--   unlimited_only=true  → return only rows with max_budget_usd = 0
--   unlimited_only=false → return rows whose utilisation ≥
--                          threshold_pct (threshold_pct = 0 includes
--                          everything).
--   exclude_inactive=true → join filters tenants.deleted_at IS NULL.
--
-- Ordered by utilisation DESC so at-risk tenants surface first.
SELECT
    tb.tenant_id,
    t.slug,
    t.display_name,
    tb.max_budget_usd,
    tb.spent_usd,
    tb.unit_code,
    tb.period_start,
    tb.period_end,
    tb.updated_at,
    (CASE
      WHEN tb.max_budget_usd = 0 THEN 0::numeric
      ELSE LEAST(100::numeric, (tb.spent_usd / tb.max_budget_usd) * 100)
    END)::numeric AS utilisation_pct
  FROM tenant_budgets AS tb
  JOIN tenants AS t ON t.id = tb.tenant_id
 WHERE (NOT sqlc.arg('exclude_inactive')::bool OR t.deleted_at IS NULL)
   AND (
     (sqlc.arg('unlimited_only')::bool AND tb.max_budget_usd = 0)
     OR (NOT sqlc.arg('unlimited_only')::bool
         AND (
           sqlc.arg('threshold_pct')::numeric = 0
           OR (tb.max_budget_usd > 0
               AND (tb.spent_usd / tb.max_budget_usd) * 100 >= sqlc.arg('threshold_pct')::numeric)
         ))
   )
 ORDER BY
   CASE WHEN tb.max_budget_usd > 0
        THEN (tb.spent_usd / tb.max_budget_usd) * 100
        ELSE 0
   END DESC,
   t.slug ASC
 LIMIT sqlc.arg('row_limit')::int;
