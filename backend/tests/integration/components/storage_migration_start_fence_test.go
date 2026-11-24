//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// A promote holds its tenant's migration row only if the row is in a fencing
// state when the promote reads it. One that read it before the migration
// existed held nothing: it could commit after the copy listed past its key,
// stamped before the copy's start, which the catch-up then skipped — a new
// object missing from the target, and the verify rolled the migration back.
// The start now waits for every promote in flight.
func TestStorageMigrationStartWaitsForAPromoteInFlight(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	id := f.presignBeforeRebind(t, ctx)
	f.store.put(f.loc(f.private, "docs", lateKey), lateSize)

	inTx, release := make(chan struct{}), make(chan struct{})
	promoted := heldPromote(ctx, statemachine.New(f.pool), id, f.sourcePlacement(), lateSize, inTx, release)
	<-inTx
	started := make(chan error, 1)
	go func() { started <- f.startRaw(ctx) }()

	// The copy lists docs/late.bin before docs/obj-1: releasing the promote
	// while obj-1 is copied lands it behind the cursor.
	copier := &hookCopier{fakeObjectStore: f.store, key: "obj-1", hook: func() {
		close(release)
		if err := <-promoted; err != nil {
			t.Errorf("promote: %v", err)
		}
	}}
	select {
	case err := <-started:
		// The start did not wait: run the copy with the promote still open.
		if err != nil {
			close(release)
			t.Fatalf("start: %v", err)
		}
		t.Errorf("the start did not wait for the promote in flight")
	case <-time.After(fenceWait):
		copier.once.Do(copier.hook)
		if err := <-started; err != nil {
			t.Fatalf("start: %v", err)
		}
	}
	mustExec(t, ctx, f.pool, `UPDATE buckets SET provision_state = 'ready' WHERE name = $1`, f.dedicated)

	if state, reason := f.driveWith(t, ctx, copier, f.store); state != worker.MigStateCleaned {
		t.Fatalf("migration left at %s (%s), want cleaned", state, reason)
	}
	if size, ok := f.store.sizeOf(f.loc(f.dedicated, "docs", lateKey)); !ok || size != lateSize {
		t.Fatalf("the promoted object is not in the target (size %d, present %v)", size, ok)
	}
}
