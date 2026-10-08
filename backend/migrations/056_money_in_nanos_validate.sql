-- +goose Up

-- Validates 055's bounds under SHARE UPDATE EXCLUSIVE, not a full lock.
ALTER TABLE capability_usage VALIDATE CONSTRAINT capability_usage_spent_usd_nanos;
ALTER TABLE capability_usage VALIDATE CONSTRAINT capability_usage_reserved_usd_nanos;
ALTER TABLE charges VALIDATE CONSTRAINT charges_amount_nanos;
ALTER TABLE tenant_budgets VALIDATE CONSTRAINT tenant_budgets_max_budget_usd_nanos;
ALTER TABLE tenant_budgets VALIDATE CONSTRAINT tenant_budgets_spent_usd_nanos;
ALTER TABLE tenant_budgets VALIDATE CONSTRAINT tenant_budgets_reserved_usd_nanos;
ALTER TABLE charge_refunds VALIDATE CONSTRAINT charge_refunds_amount_nanos;
ALTER TABLE capability_reservations VALIDATE CONSTRAINT capability_reservations_amount_nanos;
ALTER TABLE capability_copy_usage VALIDATE CONSTRAINT capability_copy_usage_spent_nanos;
ALTER TABLE capability_copy_usage VALIDATE CONSTRAINT capability_copy_usage_reserved_nanos;

-- +goose Down
-- Nothing to undo: 055's Down drops the constraints.
SELECT 1;
