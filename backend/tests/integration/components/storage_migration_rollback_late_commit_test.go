//go:build integration

package components

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The objects the rollback test writes to the target after the rebind: one
// whose promote is held open across a rollback batch, and one that commits
// meanwhile, stamped later.
const (
	heldKey      = "held.bin"
	heldSize     = int64(13)
	committedKey = "committed.bin"
	committedSz  = int64(11)
)

// pendingIn registers docs/key as an upload whose bytes are already in
// bucket, and returns its id.
func (f publicMigrationFixture) pendingIn(t *testing.T, ctx context.Context, bucket, key string, size int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO objects (tenant_id, collection_id, path, state, content_type, size_bytes)
		SELECT $1, c.id, $2, 'PENDING', 'text/plain', $3 FROM collections c
		 WHERE c.tenant_id = $1 AND c.name = 'docs' RETURNING id`, f.tenant, key, size).Scan(&id); err != nil {
		t.Fatalf("register %s: %v", key, err)
	}
	f.store.put(f.loc(bucket, "docs", key), size)
	return id
}

// blockedOnMigrationPoll is how often awaitBlockedOnTheMigrationRow looks.
const blockedOnMigrationPoll = 10 * time.Millisecond

// awaitBlockedOnTheMigrationRow returns once a session waits for a lock in a
// statement on tenant_storage_migrations, or ctx ends.
func (f publicMigrationFixture) awaitBlockedOnTheMigrationRow(ctx context.Context) {
	for ctx.Err() == nil {
		var waiting bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND query ILIKE '%tenant_storage_migrations%'
			  AND pid <> pg_backend_pid())`).Scan(&waiting); err == nil && waiting {
			return
		}
		time.Sleep(blockedOnMigrationPoll)
	}
}

// The rollback copied back, behind a (written at, id) cursor, the objects
// written since the rebind. A promote stamps its write before it commits:
// one that committed after a batch had listed past its stamp fell behind the
// cursor, and the rebind back — which checked past the cursor only — left
// its bytes in the target alone. The rollback now records the write each
// copy took, and the rebind back waits for every write without one.
func TestStorageMigrationRollbackCopiesBackAWriteThatCommitsBehindItsBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	at := f.startedMigration(t, ctx, worker.MigStateRebinding)
	if err := f.store.CopyObject(ctx, f.loc(f.private, "docs", "obj-1"), f.loc(f.dedicated, "docs", "obj-1")); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool).RebindTenant(ctx, at, f.backend, f.dedicated); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET state = $2, error = 'physical verify: test' WHERE tenant_id = $1`,
		f.tenant, worker.MigStateRollingBack)

	// Stamped first, committed last.
	held := f.pendingIn(t, ctx, f.dedicated, heldKey, heldSize)
	inTx, release := make(chan struct{}), make(chan struct{})
	promoted := heldPromote(ctx, statemachine.New(f.pool), held, f.targetPlacement(), heldSize, inTx, release)
	<-inTx
	f.promotedUpload(t, ctx, "docs", committedKey, committedSz)

	// The worker lists the objects written since the rebind — committed.bin
	// only; held.bin is not committed — and then blocks on the held
	// promote's hold on the migration row, wherever it first writes or
	// locks it exclusively. The promote commits then: stamped before
	// committed.bin, behind anything a cursor would have passed.
	go func() {
		f.awaitBlockedOnTheMigrationRow(ctx)
		close(release)
	}()
	if state, reason := f.driveWith(t, ctx, f.store, f.store); state != worker.MigStateFailed {
		t.Fatalf("migration left at %s (%s), want failed, rolled back", state, reason)
	}
	if err := <-promoted; err != nil {
		t.Fatalf("held promote: %v", err)
	}
	f.assertOnSource(t, ctx, "docs")
	for key, size := range map[string]int64{heldKey: heldSize, committedKey: committedSz} {
		if got, ok := f.store.sizeOf(f.loc(f.private, "docs", key)); !ok || got != size {
			t.Errorf("source docs/%s: size %d, present %v; want it copied back", key, got, ok)
		}
	}
}
