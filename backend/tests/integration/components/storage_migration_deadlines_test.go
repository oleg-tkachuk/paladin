//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The deadlines these tests run a migration with: URLs live up to the SigV4
// ceiling, as on a deployment with the default max_ttl; a promoted object's
// anchor settles a minute after its URL expires, a PENDING upload an hour
// after; the source copies are kept an hour after the completion.
const (
	deadlinesMaxTTL        = 7 * 24 * time.Hour
	deadlinesSettleGrace   = time.Minute
	deadlinesPendingGrace  = time.Hour
	deadlinesCopyRetention = time.Hour
	// deadlinesQuietTicks is how many ticks a test runs to show a migration
	// stays where it is.
	deadlinesQuietTicks = 3
	stragglerCollection = "late"
)

// deadlinesWorker is the migration worker with the deadlines above, at now.
func (f publicMigrationFixture) deadlinesWorker(now func() time.Time) *worker.StorageMigrationWorker {
	return &worker.StorageMigrationWorker{
		Repo:   adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool),
		Copier: f.store, Deleter: f.store, Header: f.store, Identity: f.store,
		WriteWindow: deadlinesMaxTTL, SourceCopyRetention: deadlinesCopyRetention, Now: now, Logger: zap.NewNop(),
	}
}

// expireURLsBy moves the end of every URL of the tenant to ago in the past.
func (f publicMigrationFixture) expireURLsBy(t *testing.T, ctx context.Context, ago time.Duration) {
	t.Helper()
	mustExec(t, ctx, f.pool, `UPDATE objects SET presign_expires_at = now() - $2::interval
		WHERE tenant_id = $1 AND presign_expires_at IS NOT NULL`, f.tenant, ago.String())
}

// settleWithGraces runs one reconciler tick with the deadlines above.
func (f publicMigrationFixture) settleWithGraces(t *testing.T, ctx context.Context) {
	t.Helper()
	r := worker.NewReconcilerV2(statemachine.New(f.pool), adapters.NewReconcilerProbe(sqlc.New(f.pool), contentHead{s: f.store}),
		worker.ReconcilerV2Config{PendingGraceTTL: deadlinesPendingGrace, SettleGrace: deadlinesSettleGrace}, zap.NewNop())
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

// ticks runs n ticks of w.
func (f publicMigrationFixture) ticks(t *testing.T, ctx context.Context, w *worker.StorageMigrationWorker, n int) {
	t.Helper()
	for range n {
		f.tick(t, ctx, w)
	}
}

// writeDeadlineAhead reports whether the migration's write deadline is
// still more than within ahead.
func (f publicMigrationFixture) writeDeadlineAhead(t *testing.T, ctx context.Context, within time.Duration) bool {
	t.Helper()
	var ahead bool
	if err := f.pool.QueryRow(ctx, `SELECT writes_close_at > now() + $2::interval
		FROM tenant_storage_migrations WHERE tenant_id = $1`, f.tenant, within.String()).Scan(&ahead); err != nil {
		t.Fatalf("read the write deadline: %v", err)
	}
	return ahead
}

// tenantVersion is the tenant's resource_version.
func (f publicMigrationFixture) tenantVersion(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	var v int64
	if err := f.pool.QueryRow(ctx, `SELECT resource_version FROM tenants WHERE id = $1`, f.tenant).Scan(&v); err != nil {
		t.Fatalf("read the tenant's version: %v", err)
	}
	return v
}

// A migration whose only write URL for the source is a short one completes
// once that URL has expired and its anchor settled — a minute past its
// expiry — not max_ttl after it started moving. The source copies are kept
// for the GET URLs signed for them, and deleted only after that retention.
func TestStorageMigrationCompletesOnceItsURLsSettle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	id := f.registerUpload(t, ctx, lateKey, f.private)
	f.complete(t, ctx, id, f.private, f.putThroughURL(t, ctx, lateKey, f.private))

	f.enterMoving(t, ctx)
	start := time.Now()
	w := f.deadlinesWorker(func() time.Time { return start })
	f.ticks(t, ctx, w, deadlinesQuietTicks)
	if state := f.migrationState(t, ctx); state != worker.MigStateMoving {
		t.Fatalf("migration %s while a URL can write the source, want moving", state)
	}

	// The URL expired past the settle grace; the reconciler settles it.
	f.expireURLsBy(t, ctx, 2*deadlinesSettleGrace)
	f.settleWithGraces(t, ctx)
	f.tickUntil(t, ctx, w, worker.MigStateCompleted)
	if !f.writeDeadlineAhead(t, ctx, deadlinesMaxTTL-deadlinesPendingGrace) {
		t.Fatal("the migration completed only as its write deadline came; it should not wait for it")
	}
	if r := f.row(t, ctx, id); r.placed != f.dedicated {
		t.Fatalf("upload placed in %s, want the target", r.placed)
	}

	// Within the retention the source copies stay, and the passes the
	// completed migration keeps running leave the tenant's version alone.
	version := f.tenantVersion(t, ctx)
	f.ticks(t, ctx, f.deadlinesWorker(func() time.Time { return start.Add(deadlinesCopyRetention / 2) }), deadlinesQuietTicks)
	if state := f.migrationState(t, ctx); state != worker.MigStateCompleted {
		t.Fatalf("migration %s within the source copy retention, want completed", state)
	}
	if got := f.tenantVersion(t, ctx); got != version {
		t.Fatalf("tenant resource_version %d after passes that changed nothing, want %d", got, version)
	}
	if !f.store.has(f.loc(f.private, "docs", lateKey)) {
		t.Fatal("the source copy is gone within its retention")
	}
	f.tickUntil(t, ctx, f.deadlinesWorker(func() time.Time { return start.Add(2 * deadlinesCopyRetention) }), worker.MigStateCleaned)
	if f.store.has(f.loc(f.private, "docs", lateKey)) {
		t.Fatal("the cleanup left the source copy")
	}
}

// A PENDING upload anchored to the source holds the migration — its URL, or
// one regenerated for it, may still write there — until the reconciler has
// resolved it, on the pending grace, not the settle one.
func TestStorageMigrationWaitsForAPendingUploadToTheSource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.registerUpload(t, ctx, lateKey, f.private)

	f.enterMoving(t, ctx)
	w := f.deadlinesWorker(time.Now)
	f.expireURLsBy(t, ctx, 2*deadlinesSettleGrace)
	f.settleWithGraces(t, ctx)
	f.ticks(t, ctx, w, deadlinesQuietTicks)
	if !f.store.has(f.loc(f.dedicated, "docs", "obj-1")) {
		t.Fatal("the settled object was not moved")
	}
	if state := f.migrationState(t, ctx); state != worker.MigStateMoving {
		t.Fatalf("migration %s with a pending upload anchored to the source, want moving", state)
	}

	// Past the pending grace the reconciler discards the upload that never
	// came, and nothing holds the source.
	f.expireURLsBy(t, ctx, 2*deadlinesPendingGrace)
	f.settleWithGraces(t, ctx)
	f.tickUntil(t, ctx, w, worker.MigStateCompleted)
}

// A request that read the bindings before the switch may anchor an upload
// to the source, or bind a collection to it, after the migration completed.
// The completed migration keeps passing: the collection is pointed at the
// target, the objects are moved once settled, and the source is not cleaned
// while anything holds it — past its retention too.
func TestStorageMigrationCompletedMovesAStraggler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.enterMoving(t, ctx)
	f.tickUntil(t, ctx, f.deadlinesWorker(time.Now), worker.MigStateCompleted)
	// A regenerated or part URL for the source is still bounded by its
	// write deadline after the completion.
	if _, err := sqlc.New(f.pool).UploadWritesCloseAt(ctx, pgtype.UUID{Bytes: f.tenant, Valid: true}, f.backend, f.private); err != nil {
		t.Fatalf("the source's write deadline after the completion: %v", err)
	}

	late := f.registerUpload(t, ctx, lateKey, f.private)
	f.complete(t, ctx, late, f.private, f.putThroughURL(t, ctx, lateKey, f.private))
	f.addCollection(t, ctx, stragglerCollection, f.private, false)

	later := f.deadlinesWorker(func() time.Time { return time.Now().Add(2 * deadlinesCopyRetention) })
	f.ticks(t, ctx, later, deadlinesQuietTicks)
	if state := f.migrationState(t, ctx); state != worker.MigStateCompleted {
		t.Fatalf("migration %s while an upload is anchored to the source, want completed", state)
	}
	if got := f.boundTo(t, ctx, stragglerCollection); got != f.dedicated {
		t.Fatalf("%s bound to %s after a pass, want the target", stragglerCollection, got)
	}
	if !f.store.has(f.loc(f.dedicated, stragglerCollection, "obj-1")) {
		t.Fatal("the straggling collection's object was not moved")
	}
	if !f.store.has(f.loc(f.private, "docs", "obj-1")) {
		t.Fatal("the source was cleaned while an upload is anchored to it")
	}

	f.expireURLsBy(t, ctx, 2*deadlinesSettleGrace)
	f.settleWithGraces(t, ctx)
	f.tickUntil(t, ctx, later, worker.MigStateCleaned)
	if r := f.row(t, ctx, late); r.placed != f.dedicated {
		t.Fatalf("straggler placed in %s, want the target", r.placed)
	}
	if f.store.has(f.loc(f.private, "docs", lateKey)) {
		t.Fatal("the cleanup left the straggler's source copy")
	}
}
