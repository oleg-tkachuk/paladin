-- +goose NO TRANSACTION
-- +goose Up

-- One charge per settled reservation, and the lookup a repeated Settle makes.
-- Concurrently, in a file of its own (CONVENTIONS.md).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS charges_reservation_key
    ON charges (reservation_id)
    WHERE reservation_id IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS charges_reservation_key;
