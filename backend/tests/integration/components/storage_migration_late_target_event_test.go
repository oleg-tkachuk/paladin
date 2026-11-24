//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/eventingest"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// rolledBackKey and rolledBackSize are the object addCollection seeds, which
// the tenant overwrites in the target while the migration rolls back.
const (
	rolledBackKey  = "obj-1"
	rolledBackSize = int64(42)
)

// uploadedEventAt is uploadedEvent stamped with when the store reported the
// write.
func (f publicMigrationFixture) uploadedEventAt(bucket, key, etag, sequencer string, size int64, at time.Time) eventingest.CloudEvent {
	ev := f.uploadedEvent(bucket, key, etag, sequencer, size)
	ev.Time = at
	return ev
}

// rollingBackAfterTheRebind is a migration rebound to the dedicated bucket,
// docs/obj-1 copied there, whose verify failed: rolling back. It returns
// when the rebind committed, as the migration recorded it.
func (f publicMigrationFixture) rollingBackAfterTheRebind(t *testing.T, ctx context.Context) time.Time {
	t.Helper()
	at := f.startedMigration(t, ctx, worker.MigStateRebinding)
	if err := f.store.CopyObject(ctx, f.loc(f.private, "docs", rolledBackKey), f.loc(f.dedicated, "docs", rolledBackKey)); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool).RebindTenant(ctx, at, f.backend, f.dedicated); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET state = $2, error = 'physical verify: test' WHERE tenant_id = $1`,
		f.tenant, worker.MigStateRollingBack)
	var rebound time.Time
	if err := f.pool.QueryRow(ctx, `SELECT rebound_at FROM tenant_storage_migrations WHERE tenant_id = $1`, f.tenant).Scan(&rebound); err != nil {
		t.Fatalf("read rebound_at: %v", err)
	}
	return rebound
}

// assertLatestInSource checks the shared bucket holds key with the bytes
// the target last had for it.
func (f publicMigrationFixture) assertLatestInSource(t *testing.T, ctx context.Context, key, latest string) {
	t.Helper()
	got, err := f.store.ObjectETag(ctx, f.loc(f.private, "docs", key))
	if err != nil {
		t.Fatalf("docs/%s is not in the shared bucket: %v", key, err)
	}
	if got != latest {
		t.Fatalf("the shared bucket holds docs/%s as %s, want %s", key, got, latest)
	}
}

// The mirror of a late source write. An overwrite written to the target
// while the migration rolled back, whose storage event is delivered after
// the rebind back, named a bucket the collection was no longer bound to and
// was skipped: the source kept the older bytes. The rebind back now records
// when it committed, and the write is moved into the source and promoted
// there.
func TestStorageMigrationMovesATargetWriteWhoseEventArrivesAfterTheRebindBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.rollingBackAfterTheRebind(t, ctx)

	// Overwritten in the target while the migration rolls back; its event
	// is late, so the row never records it and the rollback copies nothing.
	f.store.put(f.loc(f.dedicated, "docs", rolledBackKey), rolledBackSize)
	latest, _ := f.store.ObjectETag(ctx, f.loc(f.dedicated, "docs", rolledBackKey))
	written := time.Now()
	if state, reason := f.drive(t, ctx); state != worker.MigStateFailed {
		t.Fatalf("migration at %s (%s), want failed, rolled back", state, reason)
	}
	f.assertOnSource(t, ctx, "docs")

	ev := f.uploadedEventAt(f.dedicated, rolledBackKey, latest, lateEventSequencer, rolledBackSize, written)
	if err := f.ingestHandler().Handle(ctx, ev); err != nil {
		t.Fatalf("handle the late event: %v", err)
	}
	f.assertLatestInSource(t, ctx, rolledBackKey, latest)
	// Promoted under the ETag the source's HEAD reports for the moved bytes.
	var etag *string
	if err := f.pool.QueryRow(ctx, `SELECT etag FROM objects WHERE id = $1`, f.objectID(t, ctx, "docs", rolledBackKey)).Scan(&etag); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if want := "etag-" + f.private; etag == nil || *etag != want {
		t.Fatalf("the row records etag %v, want the source's %q: the move was not promoted", etag, want)
	}
}

// A retry starts the migration over and copies into the target again, so a
// late target event of the attempt it replaced is no longer moved: the retry
// clears the rebind's and the rebind back's times (BACKLOG). The retry here
// fails again before its rebind, which leaves the migration failed as the
// rollback did: only the cleared times tell the two apart.
func TestStorageMigrationRetryStopsMovingLateTargetWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.rollingBackAfterTheRebind(t, ctx)
	original, _ := f.store.ObjectETag(ctx, f.loc(f.private, "docs", rolledBackKey))
	f.store.put(f.loc(f.dedicated, "docs", rolledBackKey), rolledBackSize)
	latest, _ := f.store.ObjectETag(ctx, f.loc(f.dedicated, "docs", rolledBackKey))
	written := time.Now()
	if state, reason := f.drive(t, ctx); state != worker.MigStateFailed {
		t.Fatalf("migration at %s (%s), want failed, rolled back", state, reason)
	}
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("retry: %v", err)
	}
	mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET state = $2, error = 'copy: test' WHERE tenant_id = $1`,
		f.tenant, worker.MigStateFailed)

	ev := f.uploadedEventAt(f.dedicated, rolledBackKey, latest, lateEventSequencer, rolledBackSize, written)
	if err := f.ingestHandler().Handle(ctx, ev); err != nil {
		t.Fatalf("handle the late event: %v", err)
	}
	f.assertLatestInSource(t, ctx, rolledBackKey, original)
}

// The migration's own copies into the target raise storage events too. One
// delivered after the rebind back reports a write from before the rebind —
// the copy, perhaps the broken one the verify failed on — and moving it would
// put it over the good source copy. So is one whose time the store did not
// report. Both are skipped.
func TestStorageMigrationSkipsATargetEventFromBeforeTheRebind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, at := range map[string]func(rebound time.Time) time.Time{
		"the copy's event": func(rebound time.Time) time.Time { return rebound.Add(-time.Minute) },
		"no time reported": func(time.Time) time.Time { return time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := seedPublicMigrationFixture(t, ctx)
			f.addCollection(t, ctx, "docs", f.private, false)
			rebound := f.rollingBackAfterTheRebind(t, ctx)
			original, _ := f.store.ObjectETag(ctx, f.loc(f.private, "docs", rolledBackKey))
			// The target copy differs from the source's: the broken copy.
			f.store.put(f.loc(f.dedicated, "docs", rolledBackKey), rolledBackSize)
			broken, _ := f.store.ObjectETag(ctx, f.loc(f.dedicated, "docs", rolledBackKey))
			if state, reason := f.drive(t, ctx); state != worker.MigStateFailed {
				t.Fatalf("migration at %s (%s), want failed, rolled back", state, reason)
			}

			ev := f.uploadedEventAt(f.dedicated, rolledBackKey, broken, lateEventSequencer, rolledBackSize, at(rebound))
			if err := f.ingestHandler().Handle(ctx, ev); err != nil {
				t.Fatalf("handle the event: %v", err)
			}
			f.assertLatestInSource(t, ctx, rolledBackKey, original)
		})
	}
}
