//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// raceCollection is the collection these tests create while the tenant's
// collections move.
const raceCollection = "created-during-rebind"

// createArgs creates raceCollection, private, bound to bucket.
func (f publicMigrationFixture) createArgs(bucket string) objectkey.CreateCollectionArgs {
	return objectkey.CreateCollectionArgs{
		TenantID: f.tenant, Collection: raceCollection, DisplayName: raceCollection,
		BackendID: f.backend, BucketName: bucket,
	}
}

// A collection whose creation read the default binding — the source —
// before the rebind and committed after it stayed bound to the source: the
// rebind had listed the collections without it. The rebind now waits for
// the creation in flight, and moves the collection with the others.
func TestStorageMigrationRebindMovesACollectionCreatedMeanwhile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	at := f.startedMigration(t, ctx, worker.MigStateRebinding)
	repo := adapters.NewCollectionRepo(sqlc.New(f.pool), f.pool)

	inTx, release := make(chan struct{}), make(chan struct{})
	created := make(chan error, 1)
	go func() {
		created <- repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := repo.CreateTx(ctx, tx, f.createArgs(f.private))
			close(inTx)
			<-release
			return err
		})
	}()
	<-inTx
	rebound := make(chan error, 1)
	go func() {
		rebound <- adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool).RebindTenant(ctx, at, f.backend, f.dedicated)
	}()
	select {
	case err := <-rebound:
		t.Errorf("the rebind did not wait for the collection being created: %v", err)
		close(release)
	case <-time.After(fenceWait):
		close(release)
		if err := <-rebound; err != nil {
			t.Fatalf("rebind: %v", err)
		}
	}
	if err := <-created; err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := f.boundTo(t, ctx, raceCollection); got != f.dedicated {
		t.Fatalf("%s bound to %s after the rebind, want %s", raceCollection, got, f.dedicated)
	}
}

// A creation that resolved the source before the rebind and reached the
// database after it is refused, retryably; resolved again, it lands in the
// target.
func TestStorageMigrationRefusesACollectionBoundToTheSourceAfterTheRebind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	at := f.startedMigration(t, ctx, worker.MigStateRebinding)
	if err := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool).RebindTenant(ctx, at, f.backend, f.dedicated); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	repo := adapters.NewCollectionRepo(sqlc.New(f.pool), f.pool)
	if _, err := repo.Create(ctx, f.createArgs(f.private)); !errors.Is(err, objectkey.ErrBucketMigratedAway) {
		t.Fatalf("create bound to the source = %v, want ErrBucketMigratedAway", err)
	}
	if _, err := repo.Create(ctx, f.createArgs(f.dedicated)); err != nil {
		t.Fatalf("create bound to the target: %v", err)
	}
	if got := f.boundTo(t, ctx, raceCollection); got != f.dedicated {
		t.Fatalf("%s bound to %s, want %s", raceCollection, got, f.dedicated)
	}
}

// The mirror, after a rollback: the rebind back moved every collection to
// the source, so one bound to the target after it would stay there.
func TestStorageMigrationRefusesACollectionBoundToTheTargetAfterTheRebindBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	at := f.startedMigration(t, ctx, worker.MigStateRebinding)
	if err := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool).RebindTenant(ctx, at, f.backend, f.dedicated); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	// Rolled back: failed, rebound_at kept, the tenant on its source again.
	mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET state = $2 WHERE tenant_id = $1`, f.tenant, worker.MigStateFailed)
	repo := adapters.NewCollectionRepo(sqlc.New(f.pool), f.pool)
	if _, err := repo.Create(ctx, f.createArgs(f.dedicated)); !errors.Is(err, objectkey.ErrBucketMigratedAway) {
		t.Fatalf("create bound to the rolled-back target = %v, want ErrBucketMigratedAway", err)
	}
	if _, err := repo.Create(ctx, f.createArgs(f.private)); err != nil {
		t.Fatalf("create bound to the source: %v", err)
	}
}
