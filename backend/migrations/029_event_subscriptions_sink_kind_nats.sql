-- 029_event_subscriptions_sink_kind_nats.sql
--
-- Widen `event_subscriptions.sink_kind` CHECK to include 'nats'.
-- The NATS sink driver landed in commit 09d6396 (frontend +
-- backend dispatcher), but migration 008 still pins sink_kind to
-- ('http','kafka','sqs') — so CreateSubscription with a NATS sink
-- fails at INSERT time with SQLSTATE 23514:
--
--   new row for relation "event_subscriptions" violates check
--   constraint "event_subscriptions_sink_kind_check"
--
-- Drop and recreate. Kafka / SQS stay listed even though
-- EventSubscriptionService rejects them at validation time —
-- the constraint exists to catch typos / corruption, not to
-- enforce roadmap stages, and the proto already restricts the
-- accepted set.
--
-- NOT VALID + VALIDATE pattern from migration 008 isn't needed
-- here: the table is small and the new constraint is a strict
-- superset of the old one, so VALIDATE would never reject an
-- existing row anyway.

ALTER TABLE event_subscriptions
    DROP CONSTRAINT IF EXISTS event_subscriptions_sink_kind_check;

ALTER TABLE event_subscriptions
    ADD CONSTRAINT event_subscriptions_sink_kind_check
    CHECK (sink_kind IN ('http', 'nats', 'kafka', 'sqs'));
