//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// fenceWait is how long a test lets a step that must wait on a promote in
// flight run before it concludes the step is waiting.
const fenceWait = 300 * time.Millisecond

// startedMigration starts the tenant's migration and moves it to state, its
// target bucket ready, as the worker would have left it there.
func (f publicMigrationFixture) startedMigration(t *testing.T, ctx context.Context, state string) worker.MigrationRead {
	t.Helper()
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET state = $2 WHERE tenant_id = $1`, f.tenant, state)
	return worker.MigrationRead{TenantID: f.tenant, State: state}
}

// sourcePlacement and targetPlacement are the buckets a promote can find the
// bytes in during the migration.
func (f publicMigrationFixture) sourcePlacement() *statemachine.Placement {
	return &statemachine.Placement{BackendID: f.backend, Bucket: f.private}
}

func (f publicMigrationFixture) targetPlacement() *statemachine.Placement {
	return &statemachine.Placement{BackendID: f.backend, Bucket: f.dedicated}
}

// A completion resolves the collection's bucket, finds the bytes there and
// then promotes, on another transaction. A rebind in between used to let it
// promote bytes left in the source while the object was read from the
// target — and lost once the cleanup emptied the source. The promote now
// checks the binding on its own transaction and is refused.
func TestPromoteIsRefusedOnceTheRebindMovedTheCollection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	id := f.presignBeforeRebind(t, ctx)
	at := f.startedMigration(t, ctx, worker.MigStateRebinding)
	if err := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool).RebindTenant(ctx, at, f.backend, f.dedicated); err != nil {
		t.Fatalf("rebind: %v", err)
	}

	sm := statemachine.New(f.pool)
	_, err := sm.PromoteToAvailable(ctx, id, f.sourcePlacement(), "e", lateSize, "", "", statemachine.SourceRPC)
	if !errors.Is(err, statemachine.ErrBindingMoved) {
		t.Fatalf("promote in the source = %v, want ErrBindingMoved", err)
	}
	if state := f.stateOf(t, ctx, id); state != string(statemachine.StatePending) {
		t.Fatalf("a refused promote left the object %s, want PENDING", state)
	}
	if changed, err := sm.PromoteToAvailable(ctx, id, f.targetPlacement(), "e", lateSize, "", "", statemachine.SourceRPC); err != nil || !changed {
		t.Fatalf("promote in the target: changed=%v err=%v, want a promote", changed, err)
	}
}

// heldPromote runs a promote of id whose transaction stays open, after its
// UPDATE, until release is closed; inTx is closed once it is there. The
// promote's outcome is sent on the returned channel.
func heldPromote(ctx context.Context, sm *statemachine.Transitioner, id uuid.UUID, placed *statemachine.Placement,
	size int64, inTx, release chan struct{},
) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := sm.PromoteToAvailableInTx(ctx, id, placed, "e", size, "", "", statemachine.SourceRPC,
			func(context.Context, pgx.Tx) error {
				close(inTx)
				<-release
				return nil
			})
		done <- err
	}()
	return done
}

// rebindingProbe HEADs through the reconciler's probe, then runs rebind: the
// collection moves between the HEAD and the promote.
type rebindingProbe struct {
	worker.StorageProbe
	rebind func()
}

func (p rebindingProbe) HeadByObjectID(ctx context.Context, id uuid.UUID) (string, int64, string, string, statemachine.Placement, bool, error) {
	etag, size, sum, seq, placed, found, err := p.StorageProbe.HeadByObjectID(ctx, id)
	p.rebind()
	return etag, size, sum, seq, placed, found, err
}

// The reconciler promotes where its probe found the bytes; a rebind after
// the HEAD refuses that promote, and the row stays PENDING, anchored, for the
// next tick to move.
func TestReconcilerLeavesPendingAnUploadTheRebindOvertook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	id := f.presignBeforeRebind(t, ctx)
	f.store.put(f.loc(f.private, "docs", lateKey), lateSize)
	at := f.startedMigration(t, ctx, worker.MigStateRebinding)
	mustExec(t, ctx, f.pool, `UPDATE objects SET presign_expires_at = now() - interval '48 hours' WHERE id = $1`, id)

	repo := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool)
	probe := rebindingProbe{
		StorageProbe: adapters.NewReconcilerProbe(sqlc.New(f.pool), storeObjects{s: f.store, notFound: s3adapter.ErrObjectNotFound}),
		rebind: func() {
			if err := repo.RebindTenant(ctx, at, f.backend, f.dedicated); err != nil {
				t.Errorf("rebind: %v", err)
			}
		},
	}
	r := worker.NewReconcilerV2(statemachine.New(f.pool), probe, worker.ReconcilerV2Config{PendingGraceTTL: time.Second}, zap.NewNop())
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if state := f.stateOf(t, ctx, id); state != string(statemachine.StatePending) {
		t.Fatalf("the upload is %s after a rebind overtook its HEAD, want PENDING", state)
	}
	if got := f.uploadAnchor(t, ctx, id); got != f.private {
		t.Fatalf("the upload is anchored to %q, want %s", got, f.private)
	}
}
