-- +goose NO TRANSACTION
-- +goose Up

-- The admin API and the dispatcher have carried a RabbitMQ sink since the
-- baseline, but the enum never gained its value, so every create of one was
-- rejected at INSERT. ADD VALUE runs outside a transaction so the new value is
-- usable as soon as this file has applied.
ALTER TYPE event_sink_kind ADD VALUE IF NOT EXISTS 'rabbitmq';

-- +goose Down
-- Nothing to undo: Postgres cannot drop a value from an enum, and rows may
-- already reference it.
