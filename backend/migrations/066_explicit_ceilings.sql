-- +goose Up
-- +goose StatementBegin

-- limes v0.27 made "no ceiling" explicit: a ceiling of 0 allows nothing, and
-- only Unlimited lifts one. Every ceiling column here read 0 as "no ceiling".
-- From this file on, NULL is unlimited and 0 is a ceiling of nothing; the rows
-- written under the old reading are rewritten to say what they meant.

-- An ancestor's ceilings (057), as the charge path reads them.
UPDATE capability_records SET max_budget = NULL WHERE max_budget = 0;
UPDATE capability_records SET max_requests = NULL WHERE max_requests = 0;

-- The stored caveats, in the token's spelling: a ceiling absent or 0 was
-- unlimited, and now carries the flag that says so.
UPDATE capability_records
SET caveats = (caveats - 'max_budget') || '{"unlimited_budget": true}'::jsonb
WHERE NOT caveats ? 'unlimited_budget'
  AND COALESCE((caveats->>'max_budget')::numeric, 0) = 0;
UPDATE capability_records
SET caveats = (caveats - 'max_requests') || '{"unlimited_requests": true}'::jsonb
WHERE NOT caveats ? 'unlimited_requests'
  AND COALESCE((caveats->>'max_requests')::numeric, 0) = 0;

-- The tenant aggregate. 005 made the column NOT NULL with 0 for "no ceiling";
-- a tenant the runtime counts before any ceiling is set is now NULL.
ALTER TABLE tenant_budgets ALTER COLUMN max_budget_usd DROP NOT NULL;
ALTER TABLE tenant_budgets ALTER COLUMN max_budget_usd DROP DEFAULT;
UPDATE tenant_budgets SET max_budget_usd = NULL WHERE max_budget_usd = 0;

-- A hold's copy budgets: an element of 0 was a copy with no budget of its
-- own, which is now a NULL element.
UPDATE capability_reservations
SET copy_max_budget_nanos = array_replace(copy_max_budget_nanos, 0::bigint, NULL)
WHERE 0 = ANY (copy_max_budget_nanos);

-- +goose StatementEnd

-- +goose Down
-- The old reading is not restored: the code it belongs to cannot verify the
-- tokens this release issues.
SELECT 1;
