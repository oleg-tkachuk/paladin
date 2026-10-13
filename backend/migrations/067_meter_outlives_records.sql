-- +goose Up
-- +goose StatementBegin

-- limes v0.15 makes Store.Purge remove expired capability records, not only
-- their revocation entries. What the meter keeps about a capability must
-- outlive its record:
--
--   * charges is the billing ledger. Its RESTRICT reference would refuse the
--     purge of any capability that ever spent; a ledger row names the
--     capability it charged whether or not the record is still there.
--   * capability_usage, capability_copy_usage and capability_reservations
--     CASCADEd. A reservation deleted by the cascade never gave its hold
--     back to the ancestors and the tenant, which then counted it for good.
--     UsageHousekeeping.PurgeOrphans releases the holds first, then drops the
--     rows; the cascade would get there before it.
--
-- capability_revocations and capability_biscuit_revocations keep their
-- CASCADE: a revocation entry goes with its record, as the contract says.
ALTER TABLE charges DROP CONSTRAINT IF EXISTS charges_capability_id_fkey;
ALTER TABLE capability_usage DROP CONSTRAINT IF EXISTS capability_usage_capability_id_fkey;
ALTER TABLE capability_copy_usage DROP CONSTRAINT IF EXISTS capability_copy_usage_capability_id_fkey;
ALTER TABLE capability_reservations DROP CONSTRAINT IF EXISTS capability_reservations_capability_id_fkey;

-- The ancestors a charge or a hold moved, nearest first, so a refund, a
-- settle or a release returns to them once the capability's own record — and
-- with it the lineage — is gone. NULL on a row written before this file; the
-- lineage is read from the records then. Nullable with no default: a metadata
-- change, no rewrite (CONVENTIONS.md).
ALTER TABLE charges ADD COLUMN ancestor_ids uuid[];
ALTER TABLE capability_reservations ADD COLUMN ancestor_ids uuid[];

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE capability_reservations DROP COLUMN ancestor_ids;
ALTER TABLE charges DROP COLUMN ancestor_ids;
-- The references are not restored: rows of purged capabilities would violate
-- them.
-- +goose StatementEnd
