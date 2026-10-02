-- name: PurgeTerminalEventDeliveries :execrows
-- One bounded batch of delivered and failed rows last attempted before the
-- cutoff; the purger repeats it until a batch comes back short. The predicate
-- matches event_deliveries_terminal_idx, so the pending queue is never read.
DELETE FROM event_deliveries
WHERE ctid IN (
    SELECT d.ctid FROM event_deliveries AS d
    WHERE d.status IN ('delivered', 'failed')
      AND d.last_attempt_at < sqlc.arg('cutoff')::timestamptz
    ORDER BY d.last_attempt_at
    LIMIT sqlc.arg('batch_size')::int
);
