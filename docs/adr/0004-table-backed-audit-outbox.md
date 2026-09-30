# ADR-0004: Crash-durable (table-backed) audit outbox

- **Status:** Accepted — form (A) implemented 2026-06. The audit
  interceptor now writes synchronously and durably; `AsyncWriter` is
  removed. See "Status of implementation" at the end.
- **Context:** `internal/audit/async_writer.go` makes the audit interceptor
  cheap by enqueueing entries to a bounded in-memory channel and flushing
  them to Postgres in a background batch. The trade-off is durability:
  queued-but-not-yet-flushed entries are lost on an abrupt process kill
  (worst case ~one batch, ~32 rows / ~200ms). For SOC 2 / ISO 27001 the
  audit trail must survive a crash.

## Decision

Persist audit entries durably at write time, then project to the queryable
`audit_log` asynchronously — the same transactional-outbox shape as
ADR-0003, applied to audit.

Two viable forms; pick (A) unless the synchronous insert latency proves
unacceptable under load:

- **(A) Synchronous durable insert in the request tx.** The audit
  interceptor writes the row inside (or right after) the handler's own
  transaction. Durable immediately; removes the AsyncWriter entirely. Cost:
  one extra insert on the response path (mitigated — it's a single indexed
  append, and most mutating RPCs already hold a tx).
- **(B) Durable staging table + projector.** Interceptor appends to a
  lean `audit_outbox` table (cheap unlogged-or-logged append), a projector
  worker moves rows into `audit_log` and deletes them. Keeps the response
  path append-only; adds a table + reaper.

## Implementation plan (form A)

1. Drop `AsyncWriter`; the audit interceptor calls the repo insert
   directly (it already returns fast for an indexed append).
2. Where the handler runs in a tx, write the audit row on that tx so it
   commits/rolls back atomically with the operation; otherwise a
   standalone insert (still durable).
3. Keep the bounded-buffer behaviour only as an optional write-behind for
   read-only audit (`recordReads`) where loss is acceptable.

## Consequences

- No audit loss on crash; the "shutdown drain" + `ErrClosed` machinery in
  AsyncWriter goes away.
- Couples to ADR-0003's tx-threading work (same seam), so the two are best
  done together or back-to-back.
- Benchmark gate before committing to (A): p99 of the mutating RPCs with
  the synchronous insert must stay within budget; if not, fall back to (B).
  - **2026-06-26:** the gate is now measured at two levels.
    `BenchmarkAuditInterceptor` (`internal/middleware/audit_bench_test.go`)
    isolates the interceptor's non-DB overhead (~0.5µs, 152 B/6 allocs —
    negligible). `BenchmarkAuditInsertDurable`
    (`backend/tests/integration/components/audit_insert_bench_test.go`, `-tags=integration`,
    testcontainers Postgres) measures the real synchronous-insert tax: on
    postgres:17 / Apple M3 Max, **~0.16–0.18 ms/op** per audit row
    (no_payload 185µs / with ~1KB payload 160µs). That is the per-mutating-
    RPC audit cost; it sits well within budget, so form (A) holds and form
    (B) is NOT triggered. Re-run the integration bench in the target
    environment to confirm the gate before scaling.

## Status of implementation (2026-06)

Form (A) landed:
- `internal/audit/async_writer.go` (the bounded-buffer + background-flush
  wrapper) and its test are **deleted**. The `audit` package is gone; the
  `AsyncWriter` BackgroundJob and the `SharedDeps.AsyncAudit` field with it.
- Both audit interceptors (`AudienceAdmin`, `AudienceIAM`) are wired
  directly to `repos.Audit` (the Postgres `admindomain.AuditRepository`,
  which already satisfies `middleware.AuditWriter`). The interceptor calls
  `Insert` synchronously after the handler returns, so the row commits
  before the RPC response reaches the caller — no in-memory loss window on
  an abrupt kill.
- Write remains best-effort for the *caller*: an `Insert` error is logged
  and swallowed, never failing a mutation that already committed
  (`auditInterceptor.write` returns the error to the interceptor, which
  discards it). Durability ≠ blocking the request on the audit DB.
- Unit tests (`internal/middleware/audit_durable_test.go`): the row is
  written synchronously on the response path (not before the handler
  returns, and exactly once), and an `Insert` error does not fail the RPC.

Why this is durable enough: the residual window is operation-commit →
standalone audit insert (one synchronous indexed append, no batching). It
is not atomic with the operation's own tx — true atomicity would require
threading the handler tx out to the interceptor, a much larger change — but
the crash-loss surface drops from "up to one ~200ms batch" to "a single
in-flight insert", which meets the SOC 2 / ISO 27001 survive-a-crash bar.

Deferred (tracked in BACKLOG):
- Form (B) staging-table + projector, if the synchronous insert's tail
  latency ever becomes a problem under load (the benchmark gate above).
- Read-path (`recordReads=true`) write-behind: currently `recordReads` is
  `false` on every plane, so there is no async read-audit path to keep.
