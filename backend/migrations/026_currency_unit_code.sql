-- +goose Up
-- +goose StatementBegin

-- Add unit_code to monetary tables. Existing rows default to 'USD'
-- so the migration is non-disruptive. Future inserts must populate
-- it explicitly (handlers do); the DEFAULT covers the seed data.
--
-- Allowed values are validated at the application layer — keeping a
-- CHECK constraint here would couple the schema to the ISO 4217 list
-- the frontend supports, and updating that list later would mean a
-- migration. The amount column stays NUMERIC(14,6) — same precision
-- as before; unit_code only changes the *interpretation* of the
-- amount, not its precision.
--
-- Naming dichotomy: the Go domain type and proto field are
-- `Amount` + `UnitCode`. The SQL columns retain their `_usd` suffixes
-- (`spent_usd`, `max_budget_usd`) for historical reasons; renaming
-- would force a full sqlc regen + every-query update with no runtime
-- benefit. The unit_code column is the source of truth for currency
-- interpretation; the `_usd` suffix is no longer accurate but kept
-- to avoid churn.

ALTER TABLE capability_usage
    ADD COLUMN unit_code TEXT NOT NULL DEFAULT 'USD';

COMMENT ON COLUMN capability_usage.spent_usd IS
    'Accumulated spend amount. Despite the _usd suffix (kept for sqlc/query stability), the currency is determined by unit_code. Pre-migration rows are USD by default.';

COMMENT ON COLUMN capability_usage.unit_code IS
    'ISO 4217 currency code (USD/EUR/UAH/GBP) or the abstract sentinel UNIT for non-currency metering. Validated at the application layer.';

ALTER TABLE tenant_budgets
    ADD COLUMN unit_code TEXT NOT NULL DEFAULT 'USD';

COMMENT ON COLUMN tenant_budgets.max_budget_usd IS
    'Aggregate spend cap. Despite the _usd suffix (kept for sqlc/query stability), the currency is determined by unit_code.';

COMMENT ON COLUMN tenant_budgets.spent_usd IS
    'Accumulated tenant spend. Despite the _usd suffix, the currency is determined by unit_code.';

COMMENT ON COLUMN tenant_budgets.unit_code IS
    'ISO 4217 currency code (USD/EUR/UAH/GBP) or the abstract sentinel UNIT for non-currency metering. Validated at the application layer.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tenant_budgets DROP COLUMN IF EXISTS unit_code;
ALTER TABLE capability_usage DROP COLUMN IF EXISTS unit_code;
-- +goose StatementEnd
