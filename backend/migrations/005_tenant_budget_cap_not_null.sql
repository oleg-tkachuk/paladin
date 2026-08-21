-- +goose Up
-- +goose StatementBegin

-- tenant_budgets.max_budget_usd was nullable, and every predicate that reads
-- it is written as `= 0` (unlimited) or `> 0` (capped). SQL three-valued logic
-- makes both false for NULL, so a NULL cap put a tenant in neither branch:
--
--   ChargeTenantBudget       — its WHERE matched no row, so every charge came
--                              back as ErrTenantBudgetExceeded. The tenant
--                              could not spend at all.
--   ListTenantBudgetSummaries — absent from the capped list AND from the
--                              unlimited list, so no operator view showed it.
--
-- A tenant that cannot spend and cannot be seen is the worst of both: the
-- symptom reaches the customer and the diagnosis reaches nobody.
--
-- "No cap" already has a spelling, and it is 0. NULL added a second one that
-- no query understood.
--
-- NaN is the same failure from the other side. Postgres orders NaN above every
-- other numeric, so `spent + amount <= NaN` is true and a NaN cap silently
-- means unlimited — a spend ceiling that reads as a number and enforces
-- nothing. It cannot arrive through the API (buf.validate pins gte = 0, which
-- NaN fails) but nothing stopped a direct write, and numericFromFloat happily
-- encoded it.

UPDATE tenant_budgets
SET    max_budget_usd = 0
WHERE  max_budget_usd IS NULL
   OR  max_budget_usd = 'NaN'::numeric;

UPDATE tenant_budgets
SET    spent_usd = 0
WHERE  spent_usd = 'NaN'::numeric;

ALTER TABLE tenant_budgets ALTER COLUMN max_budget_usd SET DEFAULT 0;
ALTER TABLE tenant_budgets ALTER COLUMN max_budget_usd SET NOT NULL;

-- Postgres defines NaN = NaN as true for numeric (so it can be indexed and
-- grouped), which means `x <> 'NaN'` is the reliable test — the usual
-- `x = x` trick does not detect it here.
ALTER TABLE tenant_budgets
    ADD CONSTRAINT tenant_budgets_max_budget_usd_finite
    CHECK (max_budget_usd >= 0 AND max_budget_usd <> 'NaN'::numeric);

ALTER TABLE tenant_budgets
    ADD CONSTRAINT tenant_budgets_spent_usd_finite
    CHECK (spent_usd >= 0 AND spent_usd <> 'NaN'::numeric);

-- capability_usage carries the same pair of accumulators and the same
-- `max = 0 means unlimited` convention in ChargeCapability.
ALTER TABLE capability_usage
    ADD CONSTRAINT capability_usage_spent_usd_finite
    CHECK (spent_usd >= 0 AND spent_usd <> 'NaN'::numeric);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE capability_usage DROP CONSTRAINT IF EXISTS capability_usage_spent_usd_finite;
ALTER TABLE tenant_budgets DROP CONSTRAINT IF EXISTS tenant_budgets_spent_usd_finite;
ALTER TABLE tenant_budgets DROP CONSTRAINT IF EXISTS tenant_budgets_max_budget_usd_finite;
ALTER TABLE tenant_budgets ALTER COLUMN max_budget_usd DROP NOT NULL;
-- +goose StatementEnd
