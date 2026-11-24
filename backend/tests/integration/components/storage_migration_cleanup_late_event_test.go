//go:build integration

package components

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// An overwrite written to the source behind the copy's cursor is recorded
// only by its storage event. One whose event was still on its way when the
// retention window passed lost its bytes: the cleanup saw the target hold
// the object at its recorded size and deleted the source copy, and the event
// then found nothing to move. The copy now records the ETag the source held
// when it took the object, and the cleanup keeps a source copy that no longer
// matches it until the late event has moved the write.
func TestStorageMigrationCleanupKeepsASourceWriteWhoseEventIsLate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := catchUpFixture(t, ctx)
	var latest string
	f.runToCompleted(t, ctx, &hookCopier{fakeObjectStore: f.store, key: hookKey, hook: func() {
		f.store.put(f.loc(f.private, "docs", behindKey), behindSize)
		latest, _ = f.store.ObjectETag(ctx, f.loc(f.private, "docs", behindKey))
	}})
	if latest == "" {
		t.Fatal("the overwrite never ran")
	}

	// Past the retention window, before the event: the cleanup lists obj-1
	// before obj-2, so once obj-2's source copy is gone it has decided both.
	if !f.runWorker(t, ctx, func() bool { return !f.store.has(f.loc(f.private, "docs", hookKey)) }) {
		t.Fatal("the cleanup never ran")
	}
	if got, err := f.store.ObjectETag(ctx, f.loc(f.private, "docs", behindKey)); err != nil || got != latest {
		t.Fatalf("source docs/%s = %q, %v; want the unrecorded write %q kept", behindKey, got, err, latest)
	}
	if state := f.migrationState(t, ctx); state != worker.MigStateCompleted {
		t.Fatalf("migration at %s, want completed while the source holds an unrecorded write", state)
	}

	ev := f.uploadedEvent(f.private, behindKey, latest, lateEventSequencer, behindSize)
	if err := f.ingestHandler().Handle(ctx, ev); err != nil {
		t.Fatalf("handle the late event: %v", err)
	}
	if !f.runWorker(t, ctx, func() bool { return f.migrationState(t, ctx) == worker.MigStateCleaned }) {
		t.Fatalf("migration at %s, want cleaned once the late event moved the write", f.migrationState(t, ctx))
	}
	f.assertLatestInTarget(t, ctx, behindKey, latest)
}
