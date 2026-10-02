package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// QuotaReconcileRepo recomputes the `quotas` usage columns from the rows
// they are supposed to summarise.
//
// Why this exists: `usage_total_bytes` / `usage_object_count` are written
// incrementally by the upload path (object + multipart handlers call
// QuotaRepoV2.OnObjectPromoted on a successful promote) and were never
// decremented on delete, with nothing recomputing them. Since
// middleware.QuotaSoftCheck rejects uploads against those exact columns,
// an unreconciled deployment eventually refuses writes for a tenant that
// is storing nothing — the counter only ever climbed. This repo is the
// missing other half.
//
// It takes a raw pool rather than *sqlc.Queries because both statements
// are set-based cross-table updates that sqlc's per-row model would turn
// into an N+1 loop over every quota row.
//
// Pool contract: the caller MUST pass a BYPASSRLS pool. `quotas` and
// `objects` are both RLS'd (the RLS baseline (002_roles_and_rls.sql)), so on the RLS-scoped runtime
// pool with no paladin.tenant_id GUC these statements match zero rows and the
// reconciler silently does nothing — the same failure mode the background
// jobs' pool split exists to prevent.
type QuotaReconcileRepo struct {
	pool *pgxpool.Pool
}

func NewQuotaReconcileRepo(pool *pgxpool.Pool) *QuotaReconcileRepo {
	return &QuotaReconcileRepo{pool: pool}
}

// reconcileUsageSQL recomputes both quota scopes in one statement.
//
// "Stored" is defined as state = 'AVAILABLE', matching what the increment
// path counts (OnObjectPromoted fires on the transition into AVAILABLE).
// PENDING rows carry a NULL size and are not yet committed; FAILED never
// landed; DELETED is soft-deleted and no longer readable. Bytes for
// soft-deleted objects may still sit in the backend until the lifecycle
// hard-deleter runs — that is storage-reclamation lag, not quota usage,
// and counting it would make a tenant's cap depend on a worker's schedule.
//
// The LEFT JOINs are load-bearing: a quota whose tenant has no objects
// left must be driven to 0, and an INNER JOIN would simply not produce a
// row for it, leaving the stale value in place.
//
// The trailing inequality predicate keeps this a no-op write when nothing
// drifted. That matters because `quotas` carries trg_quotas_bump_rv:
// every UPDATE bumps resource_version, and a reconciler that touched
// every row on every tick would invalidate concurrent SetQuota callers'
// optimistic-concurrency tokens for no reason.
const reconcileUsageSQL = `
WITH live AS (
    -- Tenant-scoped quota: every object the tenant owns, across buckets.
    SELECT q.id AS quota_id,
           COALESCE(sum(o.size_bytes), 0)::bigint AS bytes,
           count(o.id)::bigint                    AS cnt
      FROM quotas q
      LEFT JOIN objects o
             ON o.tenant_id = q.tenant_id
            AND o.state = 'AVAILABLE'
     WHERE q.tenant_id IS NOT NULL
     GROUP BY q.id

    UNION ALL

    -- Bucket-scoped quota: objects reach a bucket through their collection's
    -- bucket_id, so the rollup hops through collections.
    SELECT q.id AS quota_id,
           COALESCE(sum(o.size_bytes), 0)::bigint AS bytes,
           count(o.id)::bigint                    AS cnt
      FROM quotas q
      LEFT JOIN collections c
             ON c.bucket_id = q.bucket_id
      LEFT JOIN objects o
             ON o.collection_id = c.id
            AND o.state = 'AVAILABLE'
     WHERE q.bucket_id IS NOT NULL
     GROUP BY q.id
)
UPDATE quotas q
   SET usage_total_bytes  = live.bytes,
       usage_object_count = live.cnt
  FROM live
 WHERE q.id = live.quota_id
   AND (q.usage_total_bytes  IS DISTINCT FROM live.bytes
     OR q.usage_object_count IS DISTINCT FROM live.cnt)`

// ReconcileUsage recomputes the stored-usage columns for every quota row
// and returns how many rows actually needed correcting. A steady-state
// deployment returns 0 — a persistently non-zero count means the
// increment path is losing writes (it is best-effort by design) or
// deletes are outpacing it.
func (r *QuotaReconcileRepo) ReconcileUsage(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, reconcileUsageSQL)
	if err != nil {
		return 0, fmt.Errorf("reconcile quota usage: %w", err)
	}
	return tag.RowsAffected(), nil
}

// rollDailySQL clears the per-day admission counters once the day they
// were accumulated in has passed.
//
// The daily counters are deliberately NOT recomputed from `objects` the
// way the totals are. They meter *admission*, so an upload that is later
// deleted must still count against the day's budget — deriving them from
// live rows would let a caller cycle upload/delete to bypass the cap.
// That makes them a true counter, which in turn makes the day-boundary
// reset mandatory: without it `max_bytes_per_day` silently degrades into
// a lifetime cap.
//
// Idempotent and self-healing: the guard is "the stamp predates today",
// so a pod that was down at midnight catches up on its next tick, and a
// second pod running the same tick finds nothing left to do. Rows with no
// accumulated usage are skipped so an idle fleet does not take a daily
// resource_version bump on every quota row; such a row's stamp is brought to
// the day by its first charge instead (IncrementQuotaUsage).
const rollDailySQL = `
UPDATE quotas
   SET usage_bytes_today   = 0,
       usage_objects_today = 0,
       last_reset_at       = $1
 WHERE (last_reset_at IS NULL OR last_reset_at < $1)
   AND (usage_bytes_today <> 0 OR usage_objects_today <> 0)`

// RollDailyCounters zeroes the per-day counters for rows last reset before
// dayStart, returning how many rows were rolled. Pass the UTC start of the
// current day.
func (r *QuotaReconcileRepo) RollDailyCounters(ctx context.Context, dayStart time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, rollDailySQL, dayStart)
	if err != nil {
		return 0, fmt.Errorf("roll daily quota counters: %w", err)
	}
	return tag.RowsAffected(), nil
}
