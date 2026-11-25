//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/migstate"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// migrationRefs is how many storage migrations DeleteBucket's reference
// check finds holding bucket.
func (f publicMigrationFixture) migrationRefs(t *testing.T, ctx context.Context, bucket string) int64 {
	t.Helper()
	refs, err := adapters.NewBucketRepoV2(sqlc.New(f.pool), f.pool).CountBucketReferences(
		auth.WithCrossTenantRead(ctx), f.backend, bucket)
	if err != nil {
		t.Fatalf("count references to %s: %v", bucket, err)
	}
	for _, r := range refs {
		if r.Relation == admindomain.RelationTenantStorageMigration {
			return r.Count
		}
	}
	t.Fatalf("the reference check of %s reports no storage migrations: %+v", bucket, refs)
	return 0
}

// deleteBucket deletes bucket's row as DeleteBucket does, as the
// application's role and scoped to no tenant: the guard must see the
// migration under row-level security all the same.
func (f publicMigrationFixture) deleteBucket(ctx context.Context, app *adapters.BucketRepoV2, bucket string) error {
	return app.Delete(ctx, f.backend, bucket, 0)
}

// emptyTenant removes what the operator removes before a purge: the
// tenant's objects and collections, and its default binding.
func (f publicMigrationFixture) emptyTenant(t *testing.T, ctx context.Context) {
	t.Helper()
	mustExec(t, ctx, f.pool, `DELETE FROM objects WHERE tenant_id = $1`, f.tenant)
	mustExec(t, ctx, f.pool, `DELETE FROM collections WHERE tenant_id = $1`, f.tenant)
	mustExec(t, ctx, f.pool, `DELETE FROM tenant_default_bindings WHERE tenant_id = $1`, f.tenant)
}

// A cleaned migration holds neither of its buckets: the dedicated one is
// deleted, the tenant purged, and the shared source deleted after it, once
// nothing else holds it. The migration's status reads on without its bucket.
func TestStorageMigrationFinishedReleasesItsBuckets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if state, reason := f.drive(t, ctx); state != worker.MigStateCleaned {
		t.Fatalf("migration left at %s (%s), want cleaned", state, reason)
	}
	if n := f.migrationRefs(t, ctx, f.dedicated); n != 0 {
		t.Fatalf("a cleaned migration holds the dedicated bucket (%d)", n)
	}

	app := rlsPool(t, ctx, f.pool)
	buckets := adapters.NewBucketRepoV2(sqlc.New(app), app)
	f.emptyTenant(t, ctx)
	if err := f.deleteBucket(ctx, buckets, f.dedicated); err != nil {
		t.Fatalf("delete the dedicated bucket after the cleanup: %v", err)
	}
	if m := f.status(t, ctx); m.State != worker.MigStateCleaned || m.TargetBucketName != "" || m.SourceBucketName != f.private {
		t.Fatalf("status after the dedicated bucket went: %+v, want cleaned with no target", m)
	}

	tenants := adapters.NewTenantRepo(sqlc.New(app), app)
	if err := tenants.SoftDelete(auth.WithActingTenant(ctx, f.tenant), f.tenant, 0); err != nil {
		t.Fatalf("trash: %v", err)
	}
	preview, err := tenants.PreviewPurge(ctx, f.tenant, maxMoveTicks)
	if err != nil || !preview.Blockers.None() {
		t.Fatalf("purge preview blockers %+v (%v), want none", preview.Blockers, err)
	}
	if err := tenants.Purge(ctx, tenanth.PurgeTenantArgs{TenantID: f.tenant, Actor: "admin"}); err != nil {
		t.Fatalf("purge: %v", err)
	}
	mustExec(t, ctx, f.pool, `DELETE FROM bucket_grants WHERE bucket_id IN (SELECT id FROM buckets WHERE name = $1)`, f.private)
	if err := f.deleteBucket(ctx, buckets, f.private); err != nil {
		t.Fatalf("delete the shared source bucket after the purge: %v", err)
	}
}

// A migration the worker still drives holds both buckets — at the reference
// check, at the database under the application's role, and at the purge,
// which says what to do — until a cancel has rolled it back to failed; the
// failed migration's status and retry outlive its dedicated bucket.
func TestStorageMigrationInFlightHoldsItsBuckets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.enterMoving(t, ctx)
	app := rlsPool(t, ctx, f.pool)
	buckets := adapters.NewBucketRepoV2(sqlc.New(app), app)
	tenants := adapters.NewTenantRepo(sqlc.New(app), app)

	for _, b := range []string{f.private, f.dedicated} {
		if n := f.migrationRefs(t, ctx, b); n != 1 {
			t.Errorf("reference check of %s finds %d migrations, want 1", b, n)
		}
	}
	// Past the reference check, as a delete racing it would be: nothing
	// but the migration holds the dedicated bucket by then.
	f.emptyTenant(t, ctx)
	if err := f.deleteBucket(ctx, buckets, f.dedicated); !errors.Is(err, admindomain.ErrConflict) {
		t.Fatalf("delete of a moving migration's bucket: %v, want ErrConflict", err)
	}
	preview, err := tenants.PreviewPurge(ctx, f.tenant, maxMoveTicks)
	if err != nil || preview.Blockers.StorageMigrations != 1 {
		t.Fatalf("purge preview blockers %+v (%v), want the migration", preview.Blockers, err)
	}
	if err := tenants.Purge(ctx, tenanth.PurgeTenantArgs{TenantID: f.tenant, Actor: "admin"}); !errors.Is(err, tenanth.ErrTenantMigrating) {
		t.Fatalf("purge of a migrating tenant: %v, want ErrTenantMigrating", err)
	}

	if _, err := f.cancel(ctx, f.status(t, ctx).ResourceVersion); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if n := f.migrationRefs(t, ctx, f.dedicated); n != 1 {
		t.Errorf("a rolling-back migration holds the dedicated bucket %d times, want 1", n)
	}
	f.tickUntil(t, ctx, f.deadlinesWorker(time.Now), worker.MigStateFailed)
	if n := f.migrationRefs(t, ctx, f.dedicated); n != 0 {
		t.Fatalf("a failed migration holds the dedicated bucket (%d)", n)
	}
	if err := f.deleteBucket(ctx, buckets, f.dedicated); err != nil {
		t.Fatalf("delete the dedicated bucket after the rollback: %v", err)
	}
	if m := f.status(t, ctx); m.State != worker.MigStateFailed || m.TargetBucketName != "" {
		t.Fatalf("status after the dedicated bucket went: %+v, want failed with no target", m)
	}
	f.addCollection(t, ctx, "docs", f.private, false)
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("retry after the dedicated bucket went: %v", err)
	}
	if state := f.migrationState(t, ctx); state != worker.MigStateProvisioning {
		t.Fatalf("retried migration %s, want provisioning", state)
	}
}

// The database's guard and the reference check agree with
// migstate.HoldsBuckets on every state: the trigger lists the states in SQL,
// so this is what keeps the two lists one.
func TestStorageMigrationGuardMatchesHoldsBuckets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	holds := map[string]bool{}
	for _, s := range migstate.HoldsBuckets() {
		holds[s] = true
	}
	for _, state := range []string{worker.MigStateProvisioning, worker.MigStateMoving, worker.MigStateCompleted,
		worker.MigStateRollingBack, worker.MigStateFailed, worker.MigStateCleaned} {
		mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET state = $2 WHERE tenant_id = $1`, f.tenant, state)
		if got := f.migrationRefs(t, ctx, f.dedicated) == 1; got != holds[state] {
			t.Errorf("%s: the reference check holds the bucket %v, want %v", state, got, holds[state])
		}
		tx, err := f.pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		_, err = tx.Exec(ctx, `DELETE FROM buckets WHERE name = $1`, f.dedicated)
		_ = tx.Rollback(ctx)
		if refused := pgerr.Is(err, pgerr.ForeignKeyViolation); refused != holds[state] {
			t.Errorf("%s: the database refused the delete %v (%v), want %v", state, refused, err, holds[state])
		}
	}
}
