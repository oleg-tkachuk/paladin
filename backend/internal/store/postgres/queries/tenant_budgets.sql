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
-- A NULL max_budget_usd is unlimited; 0 freezes the tenant's spend.
-- period_end is written as given: NULL leaves the window open-ended. now is
-- the store's clock, which a period starts by.
--
-- unit_code is written verbatim on insert — the platform's currency
-- (default_unit_code) when the caller names none; on conflict, it's
-- updated only when the caller passes a non-empty value (an empty
-- arg keeps the existing currency unchanged — operators editing
-- the cap shouldn't accidentally reinterpret an EUR budget as USD).
--
-- OCC on update, no guard on insert. Version 0 means no ceiling has been
-- set: the runtime created the row by counting a charge or a hold, and the
-- first set moves it to 1, as creating the row does. The DO UPDATE's WHERE is
-- the concurrency check: it fires only when the stored resource_version
-- matches what the caller read. A mismatch — including a caller that passed 0
-- for a ceiling another set — updates nothing and returns no row, which the
-- adapter maps to a version conflict. Without it two operators editing the
-- same cap silently overwrote each other.
INSERT INTO tenant_budgets (tenant_id, max_budget_usd, spent_usd, unit_code, period_start, period_end, updated_at, resource_version)
SELECT $1, sqlc.narg('max_budget_usd')::numeric, 0, COALESCE(NULLIF(sqlc.arg('unit_code')::text, ''), sqlc.arg('default_unit_code')::text), sqlc.arg('now')::timestamptz, sqlc.narg('period_end')::timestamptz, sqlc.arg('now')::timestamptz, 1
-- A missing row is version 0: the insert fires only for a caller that read
-- none. An existing row always reaches the DO UPDATE, whose guard decides.
WHERE sqlc.arg('expected_version')::bigint = 0
   OR EXISTS (SELECT 1 FROM tenant_budgets WHERE tenant_id = $1)
ON CONFLICT (tenant_id) DO UPDATE
SET max_budget_usd = EXCLUDED.max_budget_usd,
    spent_usd      = CASE WHEN sqlc.arg('reset_spend')::boolean THEN 0 ELSE tenant_budgets.spent_usd END,
    period_start   = CASE WHEN sqlc.arg('reset_spend')::boolean THEN sqlc.arg('now')::timestamptz ELSE tenant_budgets.period_start END,
    period         = CASE WHEN sqlc.arg('reset_spend')::boolean THEN tenant_budgets.period + 1 ELSE tenant_budgets.period END,
    period_end     = EXCLUDED.period_end,
    unit_code      = COALESCE(NULLIF(sqlc.arg('unit_code')::text, ''), tenant_budgets.unit_code),
    resource_version = tenant_budgets.resource_version + 1,
    updated_at     = sqlc.arg('now')::timestamptz
WHERE tenant_budgets.resource_version = sqlc.arg('expected_version')::bigint
RETURNING tenant_id, max_budget_usd, spent_usd, reserved_usd, unit_code, period_start, period_end, updated_at, resource_version;

-- name: GetTenantBudget :one
SELECT tenant_id, max_budget_usd, spent_usd, reserved_usd, unit_code, period_start, period_end, updated_at, resource_version
FROM tenant_budgets
WHERE tenant_id = $1;

-- name: ChargeTenantBudget :one
-- Atomic UPSERT-and-check, same shape as ChargeCapability. When the
-- row is missing, inserts a fresh accumulator row with no ceiling (NULL,
-- unlimited) and the charge's unit_code, normalised by the caller.
-- Returns the new spent_usd; pgx.ErrNoRows means "would exceed cap" —
-- caller maps to ErrTenantBudgetExceeded.
INSERT INTO tenant_budgets (tenant_id, max_budget_usd, spent_usd, unit_code, period_start, updated_at)
VALUES ($1, NULL, sqlc.arg('amount_usd')::numeric, sqlc.arg('unit_code')::text, sqlc.arg('now')::timestamptz, now())
ON CONFLICT (tenant_id) DO UPDATE
SET spent_usd  = tenant_budgets.spent_usd + sqlc.arg('amount_usd')::numeric,
    updated_at = now()
WHERE
    tenant_budgets.max_budget_usd IS NULL
    OR tenant_budgets.spent_usd + tenant_budgets.reserved_usd + sqlc.arg('amount_usd')::numeric
       <= tenant_budgets.max_budget_usd
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

-- name: ListTenantBudgets :many
-- Tenant budget rows, most at risk first (limes.TenantBudgets.ListTenantBudgets).
--
-- ceiling_class ranks the kind of ceiling: 0 for a ceiling of 0, which
-- allows nothing and leads; 1 for a bounded one; 2 for unlimited (NULL),
-- which has no utilization and trails. Bounded rows rank by spent / max,
-- unclamped, descending. The quotient is compared as
-- trunc(spent * 10^40 / max): spent and max carry nine decimals below
-- 10^9, so two different fractions differ by more than 10^-36 and their
-- keys by more than 10^4 — the order is the fractions' own, and equal
-- fractions get equal keys. Ties go by tenant id.
--
-- at_or_past keeps rows with a ceiling of 0, and bounded rows where
-- spent * 100 >= threshold_pct * max, exactly; unlimited_only keeps rows
-- with no ceiling. Both false lists every row. after_class, after_key and
-- after_tenant, all set or all NULL, resume after a row of this order.
SELECT
    tb.tenant_id,
    tb.max_budget_usd,
    tb.spent_usd,
    tb.reserved_usd,
    tb.unit_code,
    tb.period_start,
    tb.period_end,
    tb.updated_at,
    tb.resource_version,
    r.ceiling_class::int AS ceiling_class,
    r.rank_key::text AS rank_key
  FROM tenant_budgets AS tb
  CROSS JOIN LATERAL (
    SELECT CASE WHEN tb.max_budget_usd IS NULL THEN 2
                WHEN tb.max_budget_usd = 0 THEN 0
                ELSE 1
           END AS ceiling_class,
           CASE WHEN tb.max_budget_usd > 0
                THEN trunc(tb.spent_usd * 1e40 / tb.max_budget_usd)
                ELSE 0::numeric
           END AS rank_key
  ) AS r
 WHERE (NOT sqlc.arg('at_or_past')::bool
        OR r.ceiling_class = 0
        OR (r.ceiling_class = 1
            AND tb.spent_usd * 100 >= sqlc.arg('threshold_pct')::numeric * tb.max_budget_usd))
   AND (NOT sqlc.arg('unlimited_only')::bool OR r.ceiling_class = 2)
   AND (sqlc.narg('after_class')::int IS NULL
        OR r.ceiling_class > sqlc.narg('after_class')::int
        OR (r.ceiling_class = sqlc.narg('after_class')::int
            AND (r.rank_key < sqlc.narg('after_key')::numeric
                 OR (r.rank_key = sqlc.narg('after_key')::numeric
                     AND tb.tenant_id > sqlc.narg('after_tenant')::uuid))))
 ORDER BY r.ceiling_class, r.rank_key DESC, tb.tenant_id
 LIMIT sqlc.arg('row_limit')::int;
