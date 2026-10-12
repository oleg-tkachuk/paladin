-- +goose Up
-- +goose StatementBegin

-- A tenant's accounting period, counted: SetTenantBudget with ResetSpend
-- moves it on, and a charge records the period it was counted in. A refund
-- gives back to the tenant's spend only for a charge of the current period.
-- Comparing the charge's time with the period's start told the two apart
-- only while no charge shared its instant with a reset.
--
-- A constant default and a nullable column: metadata-only changes
-- (CONVENTIONS.md). A charge written before this file has no period; its
-- time is compared with the period's start, as before.
ALTER TABLE tenant_budgets ADD COLUMN period bigint NOT NULL DEFAULT 0;
ALTER TABLE charges ADD COLUMN tenant_period bigint;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE charges DROP COLUMN tenant_period;
ALTER TABLE tenant_budgets DROP COLUMN period;
-- +goose StatementEnd
