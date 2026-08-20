//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// The drainer is the retry half of the byte-reclaim outbox: it drains
// pending_purges rows written by permanent deletes whose synchronous storage
// delete failed. Its two obligations are opposites, and both are tested here:
// never drop debt whose bytes might still exist, and never announce
// paladin.object.purged until a storage delete has actually succeeded.

// scriptedDeleter fails a configurable number of times before succeeding, so a
// test can drive the retry curve.
type scriptedDeleter struct {
	failures int
	calls    int
	err      error
}

func (d *scriptedDeleter) DeleteObject(context.Context, string, string, uuid.UUID, string, string) error {
	d.calls++
	if d.calls <= d.failures {
		return d.err
	}
	return nil
}

func seedPurgeDebt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture) uuid.UUID {
	t.Helper()
	id := uuid.New()
	mustExec(t, ctx, pool, `
		INSERT INTO pending_purges
		  (id, tenant_id, object_id, bucket_id, collection_name, path)
		SELECT $1, $2, $3, b.id, $4, 'k-orphan'
		  FROM buckets b
		  JOIN storage_backends sb ON sb.id = b.backend_id
		 WHERE sb.name = 'primary' AND b.name = 'bkt-1'`,
		id, f.tenantID, uuid.New(), f.collection)
	return id
}

func purgeRowCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pending_purges WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count purge rows: %v", err)
	}
	return n
}

func purgedEventCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM event_deliveries
		  WHERE tenant_id = $1 AND event_type = 'paladin.object.purged'`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count purged events: %v", err)
	}
	return n
}

// TestPurgeDrainerKeepsDebtWhenStorageFails is the property that makes the
// whole design work. Debt is the only remaining record of where the bytes are
// — the objects row was deleted in the same transaction that wrote it — so
// dropping a row here re-creates the leak this fixes.
func TestPurgeDrainerKeepsDebtWhenStorageFails(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	id := seedPurgeDebt(t, ctx, pool, f)

	deleter := &scriptedDeleter{failures: 99, err: errors.New("backend unreachable")}
	d := &worker.PurgeDrainer{
		Pool: pool, Q: sqlc.New(pool), Storage: deleter,
		BatchSize: 10, MaxBackoff: time.Hour, Logger: zap.NewNop(),
	}
	d.Sweep(ctx)

	if got := purgeRowCount(t, ctx, pool, id); got != 1 {
		t.Fatalf("debt rows = %d, want 1 — a failed reclaim must stay owed", got)
	}
	var attempts int32
	var lastErr string
	if err := pool.QueryRow(ctx,
		`SELECT attempts, last_error FROM pending_purges WHERE id = $1`, id).
		Scan(&attempts, &lastErr); err != nil {
		t.Fatalf("read debt row: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 — the retry curve needs the count to advance", attempts)
	}
	if lastErr == "" {
		t.Error("last_error is empty; an operator debugging a stuck purge has nothing to go on")
	}
	// And nothing was announced, because nothing was reclaimed.
	if got := purgedEventCount(t, ctx, pool, f.tenantID); got != 0 {
		t.Errorf("paladin.object.purged emitted %d times before the bytes were gone", got)
	}
}

// TestPurgeDrainerSettlesAndAnnouncesOnSuccess covers the terminal path: the
// debt clears and paladin.object.purged fires, in that order and atomically.
func TestPurgeDrainerSettlesAndAnnouncesOnSuccess(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	id := seedPurgeDebt(t, ctx, pool, f)
	// The fixture seeds one enabled http subscription, so the fan-out has a
	// subscriber and the event lands in event_deliveries.
	deleter := &scriptedDeleter{}

	d := &worker.PurgeDrainer{
		Pool: pool, Q: sqlc.New(pool), Storage: deleter,
		Events: &worker.Dispatcher{
			Store:       worker.NewRepoSubscriptionStore(adapters.NewEventSubscriptionRepoV2(sqlc.New(pool))),
			Outbox:      worker.PgxOutboxWriter{Pool: pool},
			Logger:      zap.NewNop(),
			MaxAttempts: 3,
		},
		BatchSize: 10, MaxBackoff: time.Hour, Logger: zap.NewNop(),
	}
	d.Sweep(ctx)

	if got := purgeRowCount(t, ctx, pool, id); got != 0 {
		t.Errorf("debt rows = %d, want 0 — a confirmed reclaim must settle", got)
	}
	if got := purgedEventCount(t, ctx, pool, f.tenantID); got != 1 {
		t.Errorf("paladin.object.purged emitted %d times, want 1", got)
	}
	if deleter.calls != 1 {
		t.Errorf("storage delete called %d times, want 1", deleter.calls)
	}
}

// TestPurgeDrainerRetriesUntilSuccess walks the debt through a failure and out
// the other side, proving the backoff schedule does not strand a row. The
// second sweep has to be forced past next_attempt_at, which is what the manual
// reset below stands in for — a real deployment just waits.
func TestPurgeDrainerRetriesUntilSuccess(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	id := seedPurgeDebt(t, ctx, pool, f)

	deleter := &scriptedDeleter{failures: 1, err: errors.New("transient")}
	d := &worker.PurgeDrainer{
		Pool: pool, Q: sqlc.New(pool), Storage: deleter,
		BatchSize: 10, MaxBackoff: time.Hour, Logger: zap.NewNop(),
	}

	d.Sweep(ctx)
	if got := purgeRowCount(t, ctx, pool, id); got != 1 {
		t.Fatalf("after the failing sweep debt rows = %d, want 1", got)
	}

	// Fast-forward past the backoff.
	mustExec(t, ctx, pool, `UPDATE pending_purges SET next_attempt_at = now() WHERE id = $1`, id)
	d.Sweep(ctx)

	if got := purgeRowCount(t, ctx, pool, id); got != 0 {
		t.Errorf("after the succeeding sweep debt rows = %d, want 0", got)
	}
	if deleter.calls != 2 {
		t.Errorf("storage delete called %d times, want 2 (one failure, one success)", deleter.calls)
	}
}
