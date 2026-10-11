-- +goose Up

-- A tenant_budgets row the runtime creates by counting a charge or a hold has
-- no ceiling yet, and limes's contract gives such a row version 0: an operator
-- who read "no ceiling" sets the first one with ExpectedVersion 0. The column
-- defaulted to 1, so that first set was refused as a conflict. SetTenantBudget
-- writes version 1 itself when it creates a row. A constant default is a
-- metadata-only change.
ALTER TABLE tenant_budgets ALTER COLUMN resource_version SET DEFAULT 0;

-- +goose Down
ALTER TABLE tenant_budgets ALTER COLUMN resource_version SET DEFAULT 1;
