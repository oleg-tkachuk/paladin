//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/eventingest"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
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
// write, as the store's own time.
func (f publicMigrationFixture) uploadedEventAt(bucket, key, etag, sequencer string, size int64, at time.Time) eventingest.CloudEvent {
	ev := f.uploadedEvent(bucket, key, etag, sequencer, size)
	ev.Time, ev.TimeReported = at, true
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

// rolledBackWithALateTargetWrite is a migration rolled back after docs/obj-1
// was overwritten in the target while it rolled back, the overwrite's event
// not yet delivered: the row never recorded it and the rollback copied
// nothing back. It returns the rebind's time, what the source holds, and
// the overwrite's ETag in the target.
func (f publicMigrationFixture) rolledBackWithALateTargetWrite(t *testing.T, ctx context.Context) (rebound time.Time, original, latest string) {
	t.Helper()
	f.addCollection(t, ctx, "docs", f.private, false)
	rebound = f.rollingBackAfterTheRebind(t, ctx)
	original, _ = f.store.ObjectETag(ctx, f.loc(f.private, "docs", rolledBackKey))
	f.store.put(f.loc(f.dedicated, "docs", rolledBackKey), rolledBackSize)
	latest, _ = f.store.ObjectETag(ctx, f.loc(f.dedicated, "docs", rolledBackKey))
	if state, reason := f.drive(t, ctx); state != worker.MigStateFailed {
		t.Fatalf("migration at %s (%s), want failed, rolled back", state, reason)
	}
	f.assertOnSource(t, ctx, "docs")
	return rebound, original, latest
}

// pastTheMargin is a target write's reported time clear of the clock margin
// after the rebind.
func pastTheMargin(rebound time.Time) time.Time {
	return rebound.Add(2 * statemachine.LateWriteClockMargin)
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
	rebound, _, latest := f.rolledBackWithALateTargetWrite(t, ctx)

	ev := f.uploadedEventAt(f.dedicated, rolledBackKey, latest, lateEventSequencer, rolledBackSize, pastTheMargin(rebound))
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

// A target event after the rebind back is moved only when it can be told
// from the migration's own copies, which raise storage events too (one of
// them perhaps the broken copy the verify failed on, which would overwrite
// the good source copy), and only while the rollback's target is still there
// and no retry has started over:
//
//   - the copy's event reports a write from before the rebind;
//   - one within the clock margin after it cannot be told from a catch-up
//     copy made just before, the store's clock against the database's;
//   - a time the ingest took from its own clock says when the event
//     arrived, not when the write was made;
//   - a target bucket being deleted ends the window;
//   - a retry clears the rebind's and the rebind back's times; it fails again
//     here before its rebind, which leaves the migration failed as the
//     rollback did, so only the cleared times tell the two apart.
func TestStorageMigrationDoesNotMoveATargetEventItCannotAttribute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, tc := range map[string]struct {
		at       func(rebound time.Time) time.Time
		reported bool
		after    func(t *testing.T, ctx context.Context, f publicMigrationFixture)
	}{
		"the copy's event": {at: func(rebound time.Time) time.Time { return rebound.Add(-time.Minute) }, reported: true},
		"within the clock margin": {
			at: func(rebound time.Time) time.Time { return rebound.Add(statemachine.LateWriteClockMargin / 2) }, reported: true,
		},
		"the ingest's clock": {at: pastTheMargin, reported: false},
		"the target is being deleted": {at: pastTheMargin, reported: true, after: func(t *testing.T, ctx context.Context, f publicMigrationFixture) {
			t.Helper()
			mustExec(t, ctx, f.pool, `UPDATE buckets SET provision_state = 'deleting' WHERE name = $1`, f.dedicated)
		}},
		"a retry started": {at: pastTheMargin, reported: true, after: func(t *testing.T, ctx context.Context, f publicMigrationFixture) {
			t.Helper()
			if err := f.start(t, ctx); err != nil {
				t.Fatalf("retry: %v", err)
			}
			mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET state = $2, error = 'copy: test' WHERE tenant_id = $1`,
				f.tenant, worker.MigStateFailed)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := seedPublicMigrationFixture(t, ctx)
			rebound, original, latest := f.rolledBackWithALateTargetWrite(t, ctx)
			if tc.after != nil {
				tc.after(t, ctx, f)
			}

			ev := f.uploadedEventAt(f.dedicated, rolledBackKey, latest, lateEventSequencer, rolledBackSize, tc.at(rebound))
			ev.TimeReported = tc.reported
			if err := f.ingestHandler().Handle(ctx, ev); err != nil {
				t.Fatalf("handle the event: %v", err)
			}
			f.assertLatestInSource(t, ctx, rolledBackKey, original)
		})
	}
}
