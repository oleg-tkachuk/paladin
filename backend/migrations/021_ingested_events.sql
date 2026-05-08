-- +goose Up
-- +goose StatementBegin

-- Idempotency table for the ingest worker.
--
-- Drives at-least-once-with-dedup semantics: every CloudEvent the
-- ingest plane handles writes a row here keyed by event_id BEFORE the
-- handler runs. INSERT ... ON CONFLICT DO NOTHING + RETURNING tells us
-- in one round-trip whether this is the first sighting (process) or a
-- duplicate (skip). Without this, a flaky NATS reconnect or RabbitMQ
-- redelivery would replay PromoteToAvailable for already-promoted
-- objects — harmless given the state-machine guard, but we'd waste
-- DB roundtrips and confuse audit-log analysis.
--
-- event_id is opaque — the upstream MQ's id when present (NATS
-- Nats-Msg-Id, RabbitMQ message_id, CloudEvents `id`) or the source
-- adapter's hash of (subject, sequencer) as a fallback.
--
-- ingested_at + reaper: rows older than the configured TTL (default 24h)
-- are dropped by IngestEventReaper. Window must exceed the longest
-- broker re-delivery window we expect — 24h is a comfortable
-- conservative cap for SeaweedFS / NATS JetStream / RabbitMQ default
-- TTLs.

CREATE TABLE ingested_events (
    event_id      TEXT        PRIMARY KEY,
    source        TEXT        NOT NULL,         -- "seaweedfs://primary", "minio://primary", ...
    type          TEXT        NOT NULL,         -- "paladin.object.uploaded", "paladin.object.deleted"
    subject       TEXT,                         -- canonical resource name when known
    ingested_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Reaper iterates by ingested_at; partial index keeps it small while
-- still covering the typical "old rows" sweep.
CREATE INDEX idx_ingested_events_age ON ingested_events (ingested_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_ingested_events_age;
DROP TABLE IF EXISTS ingested_events;
-- +goose StatementEnd
