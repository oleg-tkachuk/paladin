-- +goose Up
-- +goose StatementBegin

-- A cost reported after the fact may be reported more than once: the event
-- carrying it is delivered at least once. external_ref is the consumer's own
-- name for the cost (a call id, an event id), and a capability is charged at
-- most once per name (052). reservation_id is the reservation a charge
-- settled, so settling one twice finds the first charge (053) instead of
-- charging again. overrun records a charge committed past a ceiling under
-- OverrunRecord: the cost had been incurred, and the ledger says so.
--
-- Nullable columns and a constant default: metadata-only changes
-- (CONVENTIONS.md). Existing rows were charged directly with no name.
ALTER TABLE charges
    ADD COLUMN external_ref   text,
    ADD COLUMN reservation_id uuid,
    ADD COLUMN overrun        boolean NOT NULL DEFAULT false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE charges
    DROP COLUMN overrun,
    DROP COLUMN reservation_id,
    DROP COLUMN external_ref;
-- +goose StatementEnd
