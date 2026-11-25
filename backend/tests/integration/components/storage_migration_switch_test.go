//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// uploadBucket is the bucket an UploadObject into collection is signed for
// now: the binding it resolves (objecth's uploadTarget reads it through
// LookupBucketMeta).
func (f publicMigrationFixture) uploadBucket(t *testing.T, ctx context.Context, collection string) string {
	t.Helper()
	meta, err := adapters.NewObjectRepo(sqlc.New(f.pool), f.pool).LookupBucketMeta(ctx, f.tenant, collection, bucketgate.BytesWrite)
	if err != nil {
		t.Fatalf("resolve the upload bucket of %s: %v", collection, err)
	}
	return meta.BucketName
}

// writeDeadlineSet reports whether the migration has recorded the write
// deadline of the bucket it empties.
func (f publicMigrationFixture) writeDeadlineSet(t *testing.T, ctx context.Context) bool {
	t.Helper()
	var set bool
	if err := f.pool.QueryRow(ctx, `SELECT writes_close_at IS NOT NULL FROM tenant_storage_migrations
		WHERE tenant_id = $1`, f.tenant).Scan(&set); err != nil {
		t.Fatalf("read the write deadline: %v", err)
	}
	return set
}

// The transaction that moves a migration to moving points the tenant's
// uploads at the target (ADR-0051): an upload right after it is signed for
// the target, not for the source a pass would have repointed a tick later.
func TestStorageMigrationEnteringMovingPointsUploadsAtTheTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	f.tick(t, ctx, f.deadlinesWorker(time.Now))
	if state := f.migrationState(t, ctx); state != worker.MigStateMoving {
		t.Fatalf("migration %s after the provisioning tick, want moving", state)
	}
	if got := f.uploadBucket(t, ctx, "docs"); got != f.dedicated {
		t.Fatalf("an upload right after the switch is signed for %s, want the target %s", got, f.dedicated)
	}
	if got := f.defaultBinding(t, ctx); got != f.dedicated {
		t.Errorf("default binding %s, want the target", got)
	}
	if f.layout(t, ctx) != worker.LayoutDedicated || !f.writeDeadlineSet(t, ctx) {
		t.Errorf("layout %s, write deadline set %v; want dedicated with the source's deadline",
			f.layout(t, ctx), f.writeDeadlineSet(t, ctx))
	}
}

// A collection the schema refuses to move refuses the switch itself: the
// migration fails with nothing pointed at the target.
func TestStorageMigrationRefusedSwitchFailsTheMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	// A migration whose target is a public bucket: a private collection may
	// not bind to it.
	if _, err := sqlc.New(f.pool).CreateStorageMigration(ctx, pgtype.UUID{Bytes: f.tenant, Valid: true},
		f.backend, f.private, f.backend, f.public, 0); err != nil {
		t.Fatalf("create migration: %v", err)
	}
	f.tick(t, ctx, f.deadlinesWorker(time.Now))
	if state := f.migrationState(t, ctx); state != worker.MigStateFailed || !f.settled(t, ctx) {
		t.Fatalf("migration %s (settled %v) after a refused switch, want failed and settled", state, f.settled(t, ctx))
	}
	if got := f.uploadBucket(t, ctx, "docs"); got != f.private {
		t.Fatalf("uploads signed for %s after a refused switch, want the source", got)
	}
}

// The rollback's transition, the worker's or an operator's cancel, points the
// uploads back at the source on its own transaction.
func TestStorageMigrationRollingBackPointsUploadsAtTheSource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("the worker's", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		f.enterMoving(t, ctx)
		if got := f.uploadBucket(t, ctx, "docs"); got != f.dedicated {
			t.Fatalf("uploads signed for %s while moving, want the target", got)
		}
		mustExec(t, ctx, f.pool, `UPDATE buckets SET provision_state = 'deleting' WHERE name = $1`, f.dedicated)
		f.tick(t, ctx, f.deadlinesWorker(time.Now))
		if state := f.migrationState(t, ctx); state != worker.MigStateRollingBack {
			t.Fatalf("migration %s, want rolling_back", state)
		}
		if got := f.uploadBucket(t, ctx, "docs"); got != f.private {
			t.Fatalf("an upload right after the rollback started is signed for %s, want the source", got)
		}
		if !f.writeDeadlineSet(t, ctx) {
			t.Error("the rollback recorded no write deadline for the target")
		}
		f.assertOnSource(t, ctx, "docs")
	})

	t.Run("a cancel", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		f.enterMoving(t, ctx)
		if _, err := f.cancel(ctx, f.status(t, ctx).ResourceVersion); err != nil {
			t.Fatalf("cancel: %v", err)
		}
		if got := f.uploadBucket(t, ctx, "docs"); got != f.private {
			t.Fatalf("an upload right after the cancel is signed for %s, want the source", got)
		}
		f.assertOnSource(t, ctx, "docs")
	})
}
