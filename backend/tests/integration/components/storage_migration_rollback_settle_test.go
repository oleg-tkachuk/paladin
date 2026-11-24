//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// settled reports whether the tenant's migration has stopped passing.
func (f publicMigrationFixture) settled(t *testing.T, ctx context.Context) bool {
	t.Helper()
	var settled bool
	if err := f.pool.QueryRow(ctx, `SELECT settled_at IS NOT NULL FROM tenant_storage_migrations
		WHERE tenant_id = $1`, f.tenant).Scan(&settled); err != nil {
		t.Fatalf("read the migration's settle: %v", err)
	}
	return settled
}

// listed reports whether the worker lists the tenant's migration.
func (f publicMigrationFixture) listed(t *testing.T, ctx context.Context) bool {
	t.Helper()
	migs, err := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool).ListActive(ctx, maxMoveTicks)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	for _, m := range migs {
		if m.TenantID == f.tenant {
			return true
		}
	}
	return false
}

// rollBackToFailed moves the migration's objects into the target — an
// upload pending in the source keeps it moving — takes the target out of
// ready to roll the migration back, discards the pending upload, and ticks w
// until the rollback has failed the migration.
func (f publicMigrationFixture) rollBackToFailed(t *testing.T, ctx context.Context, w *worker.StorageMigrationWorker) {
	t.Helper()
	f.registerUpload(t, ctx, pendingKey, f.private)
	f.enterMoving(t, ctx)
	f.tick(t, ctx, w)
	mustExec(t, ctx, f.pool, `UPDATE buckets SET provision_state = 'deleting' WHERE name = $1`, f.dedicated)
	f.tickUntil(t, ctx, w, worker.MigStateRollingBack)
	mustExec(t, ctx, f.pool, `UPDATE buckets SET provision_state = 'ready' WHERE name = $1`, f.dedicated)
	f.expireURLsBy(t, ctx, 2*deadlinesPendingGrace)
	f.settleWithGraces(t, ctx)
	f.tickUntil(t, ctx, w, worker.MigStateFailed)
}

// pendingKey is the upload that keeps a migration moving until the test
// rolls it back.
const pendingKey = "pending.bin"

// A request that read the target binding before the switch back may anchor
// an upload to the target, or bind a collection to it, after the rollback
// failed the migration. The failed migration keeps passing: the collection
// is pointed at the source again, the objects are moved back once settled,
// and the passes stop only past the settle window with nothing left in the
// target.
func TestStorageMigrationFailedMovesAStragglerBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.rollBackToFailed(t, ctx, f.deadlinesWorker(time.Now))
	f.assertOnSource(t, ctx, "docs")

	// Within the settle window the passes go on, though nothing holds the
	// target; a regenerated or part URL for it is still bounded by its write
	// deadline.
	f.ticks(t, ctx, f.deadlinesWorker(time.Now), deadlinesQuietTicks)
	if f.settled(t, ctx) || !f.listed(t, ctx) {
		t.Fatal("the rolled-back migration stopped passing within its settle window")
	}
	if _, err := sqlc.New(f.pool).UploadWritesCloseAt(ctx, pgtype.UUID{Bytes: f.tenant, Valid: true}, f.backend, f.dedicated); err != nil {
		t.Fatalf("the target's write deadline after the rollback: %v", err)
	}

	late := f.registerUpload(t, ctx, lateKey, f.dedicated)
	f.complete(t, ctx, late, f.dedicated, f.putThroughURL(t, ctx, lateKey, f.dedicated))
	f.addCollection(t, ctx, stragglerCollection, f.dedicated, false)

	later := f.deadlinesWorker(func() time.Time { return time.Now().Add(2 * deadlinesCopyRetention) })
	f.ticks(t, ctx, later, deadlinesQuietTicks)
	if f.settled(t, ctx) {
		t.Fatal("the migration settled while an upload is anchored to the target")
	}
	if state := f.migrationState(t, ctx); state != worker.MigStateFailed {
		t.Fatalf("migration %s after the straggler's passes, want failed", state)
	}
	if got := f.boundTo(t, ctx, stragglerCollection); got != f.private {
		t.Fatalf("%s bound to %s after a pass, want the source", stragglerCollection, got)
	}
	if !f.store.has(f.loc(f.private, stragglerCollection, "obj-1")) {
		t.Fatal("the straggling collection's object was not moved back")
	}

	f.expireURLsBy(t, ctx, 2*deadlinesSettleGrace)
	f.settleWithGraces(t, ctx)
	for range maxMoveTicks {
		if f.settled(t, ctx) {
			break
		}
		f.tick(t, ctx, later)
	}
	if !f.settled(t, ctx) || f.listed(t, ctx) {
		t.Fatal("the rolled-back migration kept passing with nothing in its target past the settle window")
	}
	if r := f.row(t, ctx, late); r.placed != f.private {
		t.Fatalf("straggler placed in %s, want the source", r.placed)
	}
	f.assertOnSource(t, ctx, "docs", stragglerCollection)
}

// A migration that failed in its provisioning had nothing pointed at its
// target: it is settled as it fails, and the worker never passes it — a
// reverse pass would write the tenant a default binding it may not have.
func TestStorageMigrationFailedInProvisioningIsNotPassed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	if err := f.startRaw(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	mustExec(t, ctx, f.pool, `UPDATE buckets SET provision_state = 'failed' WHERE name = $1`, f.dedicated)
	f.tickUntil(t, ctx, f.moveWorker(time.Now), worker.MigStateFailed)
	if !f.settled(t, ctx) || f.listed(t, ctx) {
		t.Fatal("a migration that failed in provisioning is still passed")
	}
}

// A retry of a failed migration starts it over: the failed attempt's writes,
// read while its passes went on, apply to nothing, the retry keeps nothing of
// its settle, and it is passed forward from where the objects are.
func TestStorageMigrationRetryAfterARollback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.rollBackToFailed(t, ctx, f.deadlinesWorker(time.Now))
	m := f.active(t, ctx)
	later := f.deadlinesWorker(func() time.Time { return time.Now().Add(2 * deadlinesCopyRetention) })
	for range maxMoveTicks {
		if f.settled(t, ctx) {
			break
		}
		f.tick(t, ctx, later)
	}
	if !f.settled(t, ctx) {
		t.Fatal("the rolled-back migration did not settle past its window")
	}
	if err := f.startRaw(ctx); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if f.settled(t, ctx) {
		t.Fatal("the retry kept the failed attempt's settle")
	}

	repo := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool)
	if err := repo.Settle(ctx, m.Read()); !errors.Is(err, worker.ErrMigrationMoved) {
		t.Fatalf("settle of the failed attempt after the retry: %v, want ErrMigrationMoved", err)
	}
	id := f.objectID(t, ctx, "docs", "obj-1")
	if _, err := repo.MoveObject(ctx, m.Read(), m.Source, m.Target, worker.MoveCandidate{ID: id},
		func(context.Context, worker.MoveCandidate) (string, error) { return "", nil }); !errors.Is(err, worker.ErrMigrationMoved) {
		t.Fatalf("move of the failed attempt after the retry: %v, want ErrMigrationMoved", err)
	}
	f.tickUntil(t, ctx, f.deadlinesWorker(time.Now), worker.MigStateCompleted)
}
