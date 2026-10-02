//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/objectpath"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// orphanPath is the path seedPurgeDebt owes bytes at.
const orphanPath = "k-orphan"

// insertWaitBudget is how long an insert may wait on a held path lock before
// the test concludes it is blocked.
const insertWaitBudget = 300 * time.Millisecond

// A storage key is derived from the path, not the object, so an object
// uploaded where a permanently deleted one lived writes the same key. The
// drainer retried the old debt by path and deleted the new object's bytes.
func TestPurgeDrainerSparesAReusedPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	debt := seedPurgeDebt(t, ctx, pool, f)

	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	newer, err := repo.CreateObject(ctx, objecth.CreateObjectArgs{
		TenantID: f.tenantID, Collection: f.collection, Key: orphanPath,
		PresignExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("upload at the reused path: %v", err)
	}

	deleter := &scriptedDeleter{}
	d := &worker.PurgeDrainer{
		Pool: pool, Q: sqlc.New(pool), Storage: deleter,
		BatchSize: 10, MaxBackoff: time.Hour, Logger: zap.NewNop(),
	}

	// Still uploading: whose bytes sit at the key is unknown, so neither
	// delete nor settle.
	d.Sweep(ctx)
	if deleter.calls != 0 {
		t.Fatalf("the drainer deleted the bytes at a path a new upload holds (%d calls)", deleter.calls)
	}
	if got := purgeRowCount(t, ctx, pool, debt); got != 1 {
		t.Fatalf("debt rows = %d, want 1 while the upload is in flight", got)
	}

	// Uploaded: its write replaced the old bytes, so the debt is paid.
	mustExec(t, ctx, pool, `UPDATE objects SET state = 'AVAILABLE' WHERE id = $1`, newer.ObjectID)
	mustExec(t, ctx, pool, `UPDATE pending_purges SET next_attempt_at = now() WHERE id = $1`, debt)
	d.Sweep(ctx)
	if deleter.calls != 0 {
		t.Fatalf("the drainer deleted a live object's bytes (%d calls)", deleter.calls)
	}
	if got := purgeRowCount(t, ctx, pool, debt); got != 0 {
		t.Errorf("debt rows = %d, want 0: the new object's write paid it", got)
	}
}

// An object insert waits while a purge holds its path's lock, and proceeds
// once the purge's transaction ends — so no upload starts between a purge's
// decision and its storage delete.
func TestObjectInsertWaitsForAPurgeOfItsPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	args := objecth.CreateObjectArgs{
		TenantID: f.tenantID, Collection: f.collection, Key: orphanPath,
		PresignExpiresAt: time.Now().Add(time.Hour),
	}

	purge := holdPathForPurge(t, ctx, pool, f)
	waitCtx, cancel := context.WithTimeout(ctx, insertWaitBudget)
	defer cancel()
	if _, err := repo.CreateObject(waitCtx, args); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("an insert during a purge of its path returned %v, want it to wait", err)
	}

	if err := purge.Commit(ctx); err != nil {
		t.Fatalf("end the purge: %v", err)
	}
	if _, err := repo.CreateObject(ctx, args); err != nil {
		t.Fatalf("insert after the purge: %v", err)
	}
}

// holdPathForPurge opens a transaction that has decided to purge orphanPath
// and still holds its lock.
func holdPathForPurge(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture) interface {
	Commit(context.Context) error
} {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	verdict, err := objectpath.Check(ctx, sqlc.New(pool).WithTx(tx), f.tenantID, f.collection, orphanPath)
	if err != nil || verdict != objectpath.Delete {
		t.Fatalf("check an unused path = %v, %v; want Delete", verdict, err)
	}
	return tx
}
