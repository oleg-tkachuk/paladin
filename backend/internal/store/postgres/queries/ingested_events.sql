-- Idempotency / dedup for the ingest plane. Every CloudEvent the worker
-- claims passes through ClaimIngestedEvent — INSERT ... ON CONFLICT
-- DO NOTHING + RETURNING tells us in one round-trip whether this is the
-- first sighting (claimed = true → process) or a duplicate (false → skip).

-- name: ClaimIngestedEvent :one
INSERT INTO ingested_events (event_id, source, type, subject)
VALUES ($1, $2, $3, sqlc.narg('subject')::text)
ON CONFLICT (event_id) DO NOTHING
RETURNING event_id;

-- name: PurgeIngestedEventsBefore :execrows
-- Drops rows older than the cutoff in batches of 10k. Reaper loops
-- until 0 rows so a long-overdue first sweep doesn't pin a single
-- statement for minutes.
DELETE FROM ingested_events
WHERE ctid IN (
    SELECT ie.ctid FROM ingested_events AS ie
    WHERE ie.ingested_at < $1
    ORDER BY ie.ingested_at
    LIMIT 10000
);
