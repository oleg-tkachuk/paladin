-- +goose Up

-- Money is counted in nanos, billionths of a unit, so its columns hold nine
-- decimals. Dropping the (14,6) typmod is a catalog change, not a rewrite;
-- the bounds a numeric(18,9) would have set — nine decimals, under a billion
-- units, capability.MaxNanos — are CHECKs, added NOT VALID here and
-- validated in 056 (CONVENTIONS.md).
--
-- A reservation's copy budgets were micros; they become nanos. The table
-- holds only open holds, minutes long, so the rewrite is of a handful of rows.
ALTER TABLE capability_reservations RENAME COLUMN copy_max_budget_micros TO copy_max_budget_nanos;
UPDATE capability_reservations
   SET copy_max_budget_nanos = ARRAY(SELECT b * 1000 FROM unnest(copy_max_budget_nanos) AS b)
 WHERE copy_max_budget_nanos IS NOT NULL;
ALTER TABLE capability_usage ALTER COLUMN spent_usd TYPE numeric;
ALTER TABLE capability_usage ADD CONSTRAINT capability_usage_spent_usd_nanos
    CHECK (scale(spent_usd) <= 9 AND spent_usd <= 999999999.999999999) NOT VALID;
ALTER TABLE capability_usage ALTER COLUMN reserved_usd TYPE numeric;
ALTER TABLE capability_usage ADD CONSTRAINT capability_usage_reserved_usd_nanos
    CHECK (scale(reserved_usd) <= 9 AND reserved_usd <= 999999999.999999999) NOT VALID;
ALTER TABLE charges ALTER COLUMN amount TYPE numeric;
ALTER TABLE charges ADD CONSTRAINT charges_amount_nanos
    CHECK (scale(amount) <= 9 AND amount <= 999999999.999999999) NOT VALID;
ALTER TABLE tenant_budgets ALTER COLUMN max_budget_usd TYPE numeric;
ALTER TABLE tenant_budgets ADD CONSTRAINT tenant_budgets_max_budget_usd_nanos
    CHECK (scale(max_budget_usd) <= 9 AND max_budget_usd <= 999999999.999999999) NOT VALID;
ALTER TABLE tenant_budgets ALTER COLUMN spent_usd TYPE numeric;
ALTER TABLE tenant_budgets ADD CONSTRAINT tenant_budgets_spent_usd_nanos
    CHECK (scale(spent_usd) <= 9 AND spent_usd <= 999999999.999999999) NOT VALID;
ALTER TABLE tenant_budgets ALTER COLUMN reserved_usd TYPE numeric;
ALTER TABLE tenant_budgets ADD CONSTRAINT tenant_budgets_reserved_usd_nanos
    CHECK (scale(reserved_usd) <= 9 AND reserved_usd <= 999999999.999999999) NOT VALID;
ALTER TABLE charge_refunds ALTER COLUMN amount TYPE numeric;
ALTER TABLE charge_refunds ADD CONSTRAINT charge_refunds_amount_nanos
    CHECK (scale(amount) <= 9 AND amount <= 999999999.999999999) NOT VALID;
ALTER TABLE capability_reservations ALTER COLUMN amount TYPE numeric;
ALTER TABLE capability_reservations ADD CONSTRAINT capability_reservations_amount_nanos
    CHECK (scale(amount) <= 9 AND amount <= 999999999.999999999) NOT VALID;
ALTER TABLE capability_copy_usage ALTER COLUMN spent TYPE numeric;
ALTER TABLE capability_copy_usage ADD CONSTRAINT capability_copy_usage_spent_nanos
    CHECK (scale(spent) <= 9 AND spent <= 999999999.999999999) NOT VALID;
ALTER TABLE capability_copy_usage ALTER COLUMN reserved TYPE numeric;
ALTER TABLE capability_copy_usage ADD CONSTRAINT capability_copy_usage_reserved_nanos
    CHECK (scale(reserved) <= 9 AND reserved <= 999999999.999999999) NOT VALID;

-- +goose Down
UPDATE capability_reservations
   SET copy_max_budget_nanos = ARRAY(SELECT b / 1000 FROM unnest(copy_max_budget_nanos) AS b)
 WHERE copy_max_budget_nanos IS NOT NULL;
ALTER TABLE capability_reservations RENAME COLUMN copy_max_budget_nanos TO copy_max_budget_micros;
ALTER TABLE capability_usage DROP CONSTRAINT IF EXISTS capability_usage_spent_usd_nanos;
ALTER TABLE capability_usage ALTER COLUMN spent_usd TYPE numeric(14,6);
ALTER TABLE capability_usage DROP CONSTRAINT IF EXISTS capability_usage_reserved_usd_nanos;
ALTER TABLE capability_usage ALTER COLUMN reserved_usd TYPE numeric(14,6);
ALTER TABLE charges DROP CONSTRAINT IF EXISTS charges_amount_nanos;
ALTER TABLE charges ALTER COLUMN amount TYPE numeric(14,6);
ALTER TABLE tenant_budgets DROP CONSTRAINT IF EXISTS tenant_budgets_max_budget_usd_nanos;
ALTER TABLE tenant_budgets ALTER COLUMN max_budget_usd TYPE numeric(14,6);
ALTER TABLE tenant_budgets DROP CONSTRAINT IF EXISTS tenant_budgets_spent_usd_nanos;
ALTER TABLE tenant_budgets ALTER COLUMN spent_usd TYPE numeric(14,6);
ALTER TABLE tenant_budgets DROP CONSTRAINT IF EXISTS tenant_budgets_reserved_usd_nanos;
ALTER TABLE tenant_budgets ALTER COLUMN reserved_usd TYPE numeric(14,6);
ALTER TABLE charge_refunds DROP CONSTRAINT IF EXISTS charge_refunds_amount_nanos;
ALTER TABLE charge_refunds ALTER COLUMN amount TYPE numeric(14,6);
ALTER TABLE capability_reservations DROP CONSTRAINT IF EXISTS capability_reservations_amount_nanos;
ALTER TABLE capability_reservations ALTER COLUMN amount TYPE numeric(14,6);
ALTER TABLE capability_copy_usage DROP CONSTRAINT IF EXISTS capability_copy_usage_spent_nanos;
ALTER TABLE capability_copy_usage ALTER COLUMN spent TYPE numeric(14,6);
ALTER TABLE capability_copy_usage DROP CONSTRAINT IF EXISTS capability_copy_usage_reserved_nanos;
ALTER TABLE capability_copy_usage ALTER COLUMN reserved TYPE numeric(14,6);
