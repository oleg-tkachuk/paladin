//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TestReconcileQuotaUsage_DrivesCountersToLiveTruth is the regression test
// for the bug this job exists to fix: the upload path only ever incremented
// quotas.usage_*, nothing decremented on delete, and nothing recomputed —
// so middleware.QuotaSoftCheck eventually rejected uploads for a tenant
// storing nothing.
//
// The fixture writes a deliberately wrong counter (as a drifted deployment
// would have) and asserts the reconciler drags it back to what `objects`
// actually says, in both directions.
func TestReconcileQuotaUsage_DrivesCountersToLiveTruth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	// Two AVAILABLE objects (300 bytes) plus rows in every other state,
	// none of which count as stored: PENDING has no committed size, FAILED
	// never landed, DELETED is soft-deleted and unreadable.
	insertObj(t, ctx, pool, f, "AVAILABLE", 100)
	insertObj(t, ctx, pool, f, "AVAILABLE", 200)
	insertObj(t, ctx, pool, f, "PENDING", nil)
	insertObj(t, ctx, pool, f, "FAILED", 999)
	insertObj(t, ctx, pool, f, "DELETED", 5000)

	// A counter drifted upward — the exact shape the missing decrement
	// produces after a few upload/delete cycles.
	quotaID := uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO quotas (id, tenant_id, max_total_bytes, max_object_count,
		                     usage_total_bytes, usage_object_count)
		 VALUES ($1, $2, 10000, 100, 99999, 42)`, quotaID, f.tenantID)

	repo := adapters.NewQuotaReconcileRepo(pool)
	n, err := repo.ReconcileUsage(ctx)
	if err != nil {
		t.Fatalf("ReconcileUsage: %v", err)
	}
	if n != 1 {
		t.Errorf("corrected %d rows, want 1", n)
	}
	bytes, count := quotaUsage(t, ctx, pool, quotaID)
	if bytes != 300 || count != 2 {
		t.Errorf("usage = %d bytes / %d objects, want 300/2 (AVAILABLE only)", bytes, count)
	}

	// Idempotent: a second pass over already-correct rows must write
	// nothing, or the trg_quotas_bump_rv trigger would invalidate every
	// concurrent SetQuota caller's OCC token on every tick.
	n, err = repo.ReconcileUsage(ctx)
	if err != nil {
		t.Fatalf("second ReconcileUsage: %v", err)
	}
	if n != 0 {
		t.Errorf("second pass corrected %d rows, want 0 (no-op when in sync)", n)
	}

	// Deleting everything must drive the counter to zero — the direction
	// the increment-only path could never go, and the one that wedged
	// tenants.
	mustExec(t, ctx, pool, `UPDATE objects SET state = 'DELETED' WHERE tenant_id = $1`, f.tenantID)
	if _, err := repo.ReconcileUsage(ctx); err != nil {
		t.Fatalf("ReconcileUsage after delete: %v", err)
	}
	bytes, count = quotaUsage(t, ctx, pool, quotaID)
	if bytes != 0 || count != 0 {
		t.Errorf("usage after deleting everything = %d/%d, want 0/0", bytes, count)
	}
}

// Bucket-scoped quota rows were never maintained at all — OnObjectPromoted
// only ever touched the tenant-scoped row — so their usage sat at zero
// forever. The reconciler reaches them by hopping objects → collections →
// (backend_id, bucket_name), and must not mix the two scopes up.
func TestReconcileQuotaUsage_CoversBucketScopedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	insertObj(t, ctx, pool, f, "AVAILABLE", 700)

	var backendID, bucketName string
	if err := pool.QueryRow(ctx,
		`SELECT sb.name, b.name
		   FROM collections c
		   JOIN buckets b           ON b.id = c.bucket_id
		   JOIN storage_backends sb ON sb.id = b.backend_id
		  WHERE c.tenant_id = $1 AND c.name = $2`,
		f.tenantID, f.collection).Scan(&backendID, &bucketName); err != nil {
		t.Fatalf("lookup binding: %v", err)
	}

	bucketQuota := uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO bucket_quotas (id, bucket_id, max_total_bytes)
		 VALUES ($1, (SELECT b.id FROM buckets b
		          JOIN storage_backends sb ON sb.id = b.backend_id
		         WHERE sb.name = $2 AND b.name = $3), 100000)`, bucketQuota, backendID, bucketName)
	// An unrelated bucket with a quota and no objects must land on 0, not
	// inherit the other bucket's rollup.
	emptyQuota := uuid.New()
	hex := uuid.NewString()[:8]
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (backend_id, name)
		 SELECT sb.id, $2 FROM storage_backends sb WHERE sb.name = $1`, backendID, "bkt-e-"+hex)
	mustExec(t, ctx, pool,
		`INSERT INTO bucket_quotas (id, bucket_id, max_total_bytes)
		 VALUES ($1, (SELECT b.id FROM buckets b
		          JOIN storage_backends sb ON sb.id = b.backend_id
		         WHERE sb.name = $2 AND b.name = $3), 100000)`, emptyQuota, backendID, "bkt-e-"+hex)

	if _, err := adapters.NewQuotaReconcileRepo(pool).ReconcileUsage(ctx); err != nil {
		t.Fatalf("ReconcileUsage: %v", err)
	}

	if bytes, count := quotaUsage(t, ctx, pool, bucketQuota); bytes != 700 || count != 1 {
		t.Errorf("bucket quota usage = %d/%d, want 700/1", bytes, count)
	}
	if bytes, count := quotaUsage(t, ctx, pool, emptyQuota); bytes != 0 || count != 0 {
		t.Errorf("empty bucket quota usage = %d/%d, want 0/0", bytes, count)
	}
}

// The per-day counters meter admission, so they stay incremental — which
// makes the day-boundary roll mandatory. Without it `max_bytes_per_day`
// silently becomes a lifetime cap.
func TestRollDailyCounters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	repo := adapters.NewQuotaReconcileRepo(pool)

	stale := uuid.New() // accumulated yesterday, never rolled
	mustExec(t, ctx, pool,
		`INSERT INTO quotas (id, tenant_id, max_bytes_per_day,
		                     usage_bytes_today, usage_objects_today, last_reset_at)
		 VALUES ($1, $2, 1000, 900, 9, now() - interval '2 days')`, stale, f.tenantID)

	dayStart := time.Now().UTC().Truncate(24 * time.Hour)
	n, err := repo.RollDailyCounters(ctx, dayStart)
	if err != nil {
		t.Fatalf("RollDailyCounters: %v", err)
	}
	if n != 1 {
		t.Fatalf("rolled %d rows, want 1", n)
	}
	var bytesToday, objectsToday int64
	var lastReset time.Time
	if err := pool.QueryRow(ctx,
		`SELECT usage_bytes_today, usage_objects_today, last_reset_at
		   FROM quotas WHERE id = $1`, stale).
		Scan(&bytesToday, &objectsToday, &lastReset); err != nil {
		t.Fatalf("read rolled row: %v", err)
	}
	if bytesToday != 0 || objectsToday != 0 {
		t.Errorf("daily counters = %d/%d after roll, want 0/0", bytesToday, objectsToday)
	}
	if !lastReset.UTC().Equal(dayStart) {
		t.Errorf("last_reset_at = %v, want %v", lastReset.UTC(), dayStart)
	}

	// Idempotent within the day: a second tick (or a second pod) finds
	// nothing to do rather than re-zeroing usage accrued since the roll.
	if n, err := repo.RollDailyCounters(ctx, dayStart); err != nil || n != 0 {
		t.Errorf("second roll = (%d, %v), want (0, nil)", n, err)
	}

	// Usage accrued *after* today's roll must survive further ticks —
	// otherwise the daily budget would reset continuously and never cap.
	mustExec(t, ctx, pool,
		`UPDATE quotas SET usage_bytes_today = 250 WHERE id = $1`, stale)
	if _, err := repo.RollDailyCounters(ctx, dayStart); err != nil {
		t.Fatalf("third roll: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT usage_bytes_today FROM quotas WHERE id = $1`, stale).
		Scan(&bytesToday); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if bytesToday != 250 {
		t.Errorf("usage_bytes_today = %d after same-day tick, want 250 preserved", bytesToday)
	}
}

// Bucket quotas live in their own table (044_bucket_quotas.sql) and need the
// same day-boundary roll; one tick rolls both tables and counts both.
func TestRollDailyCounters_CoversBucketQuotas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	tenantStale, bucketStale := uuid.New(), uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO quotas (id, tenant_id, max_bytes_per_day, usage_bytes_today, last_reset_at)
		 VALUES ($1, $2, 1000, 900, now() - interval '2 days')`, tenantStale, f.tenantID)
	mustExec(t, ctx, pool,
		`INSERT INTO bucket_quotas (id, bucket_id, max_bytes_per_day, usage_bytes_today, last_reset_at)
		 SELECT $1, c.bucket_id, 1000, 900, now() - interval '2 days'
		   FROM collections c WHERE c.tenant_id = $2 AND c.name = $3`,
		bucketStale, f.tenantID, f.collection)

	dayStart := time.Now().UTC().Truncate(24 * time.Hour)
	n, err := adapters.NewQuotaReconcileRepo(pool).RollDailyCounters(ctx, dayStart)
	if err != nil {
		t.Fatalf("RollDailyCounters: %v", err)
	}
	if n != 2 {
		t.Errorf("rolled %d rows, want 2 — one per table", n)
	}
	var bytesToday int64
	if err := pool.QueryRow(ctx,
		`SELECT usage_bytes_today FROM bucket_quotas WHERE id = $1`, bucketStale).
		Scan(&bytesToday); err != nil {
		t.Fatalf("read bucket quota: %v", err)
	}
	if bytesToday != 0 {
		t.Errorf("bucket usage_bytes_today = %d after the roll, want 0", bytesToday)
	}
}

// A quota idle since an earlier day keeps that day's reset stamp: the roll
// skips rows with nothing to clear. Its first charge of today used to add to
// that stale row, and the roll's next tick then saw an old stamp and nonzero
// usage and zeroed today's admissions, letting the day's cap be exceeded.
func TestFirstChargeOfTheDayIsKeptByTheRoll(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	quotas := adapters.NewQuotaRepoV2(sqlc.New(pool), pool)
	roll := adapters.NewQuotaReconcileRepo(pool)
	const charged = 300

	idle := uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO quotas (id, tenant_id, max_bytes_per_day,
		                     usage_bytes_today, usage_objects_today, last_reset_at)
		 VALUES ($1, $2, 1000, 0, 0, now() - interval '3 days')`, idle, f.tenantID)

	if err := quotas.IncrementUsage(ctx, idle, charged, 1); err != nil {
		t.Fatalf("charge: %v", err)
	}
	dayStart := time.Now().UTC().Truncate(24 * time.Hour)
	if _, err := roll.RollDailyCounters(ctx, dayStart); err != nil {
		t.Fatalf("roll: %v", err)
	}
	var bytesToday, objectsToday int64
	if err := pool.QueryRow(ctx,
		`SELECT usage_bytes_today, usage_objects_today FROM quotas WHERE id = $1`, idle).
		Scan(&bytesToday, &objectsToday); err != nil {
		t.Fatalf("read: %v", err)
	}
	if bytesToday != charged || objectsToday != 1 {
		t.Errorf("today's usage = %d bytes / %d objects after the roll, want %d / 1",
			bytesToday, objectsToday, charged)
	}

	// A row not yet rolled from yesterday restarts at today's charge rather
	// than adding to yesterday's.
	unrolled := uuid.New()
	other, _ := mkTenant(t, ctx, pool, "shared")
	mustExec(t, ctx, pool,
		`INSERT INTO quotas (id, tenant_id, max_bytes_per_day,
		                     usage_bytes_today, usage_objects_today, last_reset_at)
		 VALUES ($1, $2, 1000, 900, 9, now() - interval '1 day')`, unrolled, other)
	if err := quotas.IncrementUsage(ctx, unrolled, charged, 1); err != nil {
		t.Fatalf("charge unrolled: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT usage_bytes_today FROM quotas WHERE id = $1`, unrolled).Scan(&bytesToday); err != nil {
		t.Fatalf("read unrolled: %v", err)
	}
	if bytesToday != charged {
		t.Errorf("usage_bytes_today = %d, want %d: yesterday's usage counted against today", bytesToday, charged)
	}
}

func insertObj(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture, state string, size any) {
	t.Helper()
	mustExec(t, ctx, pool,
		`INSERT INTO objects (id, tenant_id, collection_id, path, state,
		                      content_type, checksum_algorithm, size_bytes)
		 SELECT $1, $2, c.id, $4, $5, 'application/octet-stream', 0, $6
		   FROM collections c WHERE c.tenant_id = $2 AND c.name = $3`,
		uuid.Must(uuid.NewV7()), f.tenantID, f.collection,
		"k-"+uuid.NewString()[:8], state, size)
}

// quotaUsage reads a quota of either scope: the id is unique across both tables.
func quotaUsage(t *testing.T, ctx context.Context, pool *pgxpool.Pool, quotaID uuid.UUID) (int64, int64) {
	t.Helper()
	var bytes, count int64
	if err := pool.QueryRow(ctx,
		`SELECT usage_total_bytes, usage_object_count FROM quotas WHERE id = $1
		 UNION ALL
		 SELECT usage_total_bytes, usage_object_count FROM bucket_quotas WHERE id = $1`,
		quotaID).Scan(&bytes, &count); err != nil {
		t.Fatalf("read quota usage: %v", err)
	}
	return bytes, count
}
