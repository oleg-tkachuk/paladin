-- +goose Up
-- +goose StatementBegin

-- TenantBudgetService.Set was a plain upsert with no concurrency guard.
--
-- Two operators editing the same tenant's spend cap raced with last-write-wins
-- and neither was told: the loser's change vanished and its UI reported
-- success. SetQuota carried the same defect until its guard landed; this is
-- the same fix on the same shape, and it needs the column first because
-- tenant_budgets never had one.
--
-- DEFAULT 1 so existing rows start where a freshly-inserted row would.
--
-- No bump_resource_version trigger here, deliberately — unlike every other
-- versioned table, this row is written by the runtime on every charge and
-- refund. A trigger would advance the version each time a capability spends,
-- so an operator's open budget form would go stale from ordinary traffic they
-- have nothing to do with. The version is advanced explicitly by SetTenantBudget
-- instead, which is the only writer whose concurrency this guard is about; the
-- charge path continues to touch spent_usd and updated_at only.

ALTER TABLE tenant_budgets
    ADD COLUMN resource_version bigint NOT NULL DEFAULT 1;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE tenant_budgets DROP COLUMN IF EXISTS resource_version;

-- +goose StatementEnd
