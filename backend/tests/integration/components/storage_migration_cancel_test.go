//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// cancelReason is what the tests record a cancel with.
const cancelReason = "cancelled by an operator"

func (f publicMigrationFixture) tenantRepo() *adapters.TenantRepo {
	return adapters.NewTenantRepo(sqlc.New(f.pool), f.pool)
}

// status is the tenant's migration as GetTenantStorageMigration reads it.
func (f publicMigrationFixture) status(t *testing.T, ctx context.Context) tenanth.StorageMigration {
	t.Helper()
	m, err := f.tenantRepo().GetStorageMigration(ctx, f.tenant)
	if err != nil {
		t.Fatalf("read the migration: %v", err)
	}
	return m
}

// cancel cancels the tenant's migration at the version read.
func (f publicMigrationFixture) cancel(ctx context.Context, version int64) (tenanth.StorageMigration, error) {
	return f.tenantRepo().CancelStorageMigration(ctx, f.tenant, version, cancelReason)
}

// assertAllInSource checks every object of the tenant is placed in the
// source.
func (f publicMigrationFixture) assertAllInSource(t *testing.T, ctx context.Context) {
	t.Helper()
	var elsewhere int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM objects o JOIN buckets b ON b.id = o.placement_bucket_id
		WHERE o.tenant_id = $1 AND b.name <> $2`, f.tenant, f.private).Scan(&elsewhere); err != nil {
		t.Fatalf("count objects outside the source: %v", err)
	}
	if elsewhere != 0 {
		t.Fatalf("%d objects placed outside the source after the cancel", elsewhere)
	}
}

// A migration cancelled midway rolls back: the version read before a pass
// still applies, the objects already moved move back, and the migration
// fails with the tenant shared and every object in the source.
func TestStorageMigrationCancelMidway(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.addCollection(t, ctx, "more", f.private, false)
	f.registerUpload(t, ctx, pendingKey, f.private)
	f.enterMoving(t, ctx)
	read := f.status(t, ctx)
	w := f.deadlinesWorker(time.Now)
	f.tick(t, ctx, w)
	if r := f.row(t, ctx, f.objectID(t, ctx, "docs", "obj-1")); r.placed != f.dedicated {
		t.Fatalf("obj-1 placed in %s before the cancel, want the target", r.placed)
	}
	if got := f.status(t, ctx).ResourceVersion; got != read.ResourceVersion {
		t.Fatalf("a pass's progress moved the version %d -> %d", read.ResourceVersion, got)
	}

	m, err := f.cancel(ctx, read.ResourceVersion)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if m.State != worker.MigStateRollingBack || m.Error != cancelReason || m.ResourceVersion == read.ResourceVersion {
		t.Fatalf("cancelled migration %+v, want rolling_back with the reason and a new version", m)
	}
	f.expireURLsBy(t, ctx, 2*deadlinesPendingGrace)
	f.settleWithGraces(t, ctx)
	f.tickUntil(t, ctx, w, worker.MigStateFailed)
	f.assertOnSource(t, ctx, "docs", "more")
	f.assertAllInSource(t, ctx)
}

// A completed migration is cancelled until it is cleaned: its objects move
// back from the target, and its completion is forgotten.
func TestStorageMigrationCancelCompleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.enterMoving(t, ctx)
	w := f.deadlinesWorker(time.Now)
	f.tickUntil(t, ctx, w, worker.MigStateCompleted)

	if _, err := f.cancel(ctx, f.status(t, ctx).ResourceVersion); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	var completed, cleanupAfter bool
	if err := f.pool.QueryRow(ctx, `SELECT completed_at IS NOT NULL, cleanup_after IS NOT NULL
		FROM tenant_storage_migrations WHERE tenant_id = $1`, f.tenant).Scan(&completed, &cleanupAfter); err != nil {
		t.Fatalf("read the migration: %v", err)
	}
	if completed || cleanupAfter {
		t.Fatal("the cancelled migration kept its completion")
	}
	f.tickUntil(t, ctx, w, worker.MigStateFailed)
	f.assertOnSource(t, ctx, "docs")
	f.assertAllInSource(t, ctx)
}

// A provisioning migration had nothing pointed at its target: the cancel
// fails it at once, settled, and the worker never passes it.
func TestStorageMigrationCancelProvisioning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	if err := f.startRaw(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	m, err := f.cancel(ctx, f.status(t, ctx).ResourceVersion)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if m.State != worker.MigStateFailed || !f.settled(t, ctx) || f.listed(t, ctx) {
		t.Fatalf("cancelled provisioning migration %s, settled %v, listed %v; want failed, settled, not listed",
			m.State, f.settled(t, ctx), f.listed(t, ctx))
	}
	if got := f.boundTo(t, ctx, "docs"); got != f.private || f.layout(t, ctx) != "shared" {
		t.Fatalf("docs bound to %s, layout %s; want the source, shared", got, f.layout(t, ctx))
	}
}

// A cancel applies only to the version read, and only to a state that takes
// one: rolling back, failed and cleaned are refused, as is a version read
// before the worker moved the migration on.
func TestStorageMigrationCancelRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	if _, err := f.cancel(ctx, 1); !errors.Is(err, tenanth.ErrStorageMigrationNotFound) {
		t.Fatalf("cancel with no migration: %v, want ErrStorageMigrationNotFound", err)
	}
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	stale := f.status(t, ctx).ResourceVersion
	f.tickUntil(t, ctx, f.deadlinesWorker(time.Now), worker.MigStateMoving)
	if _, err := f.cancel(ctx, stale); !errors.Is(err, tenanth.ErrVersionMismatch) {
		t.Fatalf("cancel at the provisioning version after the move to moving: %v, want ErrVersionMismatch", err)
	}
	if state := f.migrationState(t, ctx); state != worker.MigStateMoving {
		t.Fatalf("a refused cancel moved the migration to %s", state)
	}

	for _, state := range []string{worker.MigStateRollingBack, worker.MigStateFailed, worker.MigStateCleaned} {
		mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET state = $2 WHERE tenant_id = $1`, f.tenant, state)
		if _, err := f.cancel(ctx, f.status(t, ctx).ResourceVersion); !errors.Is(err, tenanth.ErrStorageMigrationNotCancellable) {
			t.Errorf("cancel of a %s migration: %v, want ErrStorageMigrationNotCancellable", state, err)
		}
	}
}

// A worker step read before the cancel applies nothing after it: not a move,
// and not a cleanup's delete of a source copy the rollback needs back.
func TestStorageMigrationCancelFencesTheWorker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.enterMoving(t, ctx)
	f.tickUntil(t, ctx, f.deadlinesWorker(time.Now), worker.MigStateCompleted)
	m := f.active(t, ctx)
	if _, err := f.cancel(ctx, f.status(t, ctx).ResourceVersion); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	repo := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool)
	deleted := false
	err := repo.AdvanceCleanup(ctx, m.Read(), "docs", "obj-1", func(context.Context) error { deleted = true; return nil })
	if !errors.Is(err, worker.ErrMigrationMoved) || deleted {
		t.Fatalf("cleanup read before the cancel: %v, deleted %v; want ErrMigrationMoved and no delete", err, deleted)
	}
	copied := false
	_, err = repo.MoveObject(ctx, m.Read(), m.Target, m.Source, worker.MoveCandidate{ID: f.objectID(t, ctx, "docs", "obj-1")},
		func(context.Context, worker.MoveCandidate) (string, error) { copied = true; return "", nil })
	if !errors.Is(err, worker.ErrMigrationMoved) || copied {
		t.Fatalf("move read before the cancel: %v, copied %v; want ErrMigrationMoved and no copy", err, copied)
	}
}
