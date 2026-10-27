//go:build integration

package components

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// Deleting an object removes every version, so a lock on an older version
// has to stop the delete as surely as one on the current version. The cascade
// trigger on object_locks always did, but the hard-delete queries looked only
// at the current version's lock: PermanentDelete got a raw check_violation
// (Internal) instead of a 0-row refusal its pre-check explains, and the
// lifecycle hard-deleter listed the object on every sweep and failed on it.
func TestHardDeleteHonoursALockOnAnOlderVersion(t *testing.T) {
	t.Parallel()
	for _, lock := range purgeLocks {
		t.Run(lock.name, func(t *testing.T) {
			t.Parallel()
			ctx, f := newLockFixture(t)
			// The fixture's version becomes the older one, locked; a newer,
			// unlocked version becomes current.
			lock.apply(t, ctx, f, f.versionID)
			newer := uuid.NewV7()
			mustExec(t, ctx, f.pool,
				`INSERT INTO object_versions (id, tenant_id, object_id, storage_path)
				 VALUES ($1, $2, $3, 'tenant/docs/report.pdf')`, newer, f.tenant, f.objectID)
			mustExec(t, ctx, f.pool,
				`UPDATE objects SET current_version_id = $2 WHERE id = $1`, f.objectID, newer)

			objects := adapters.NewObjectRepo(sqlc.New(f.pool), f.pool)

			t.Run("the pre-check sees the older version's lock", func(t *testing.T) {
				locks, err := objects.VersionLocks(ctx, f.tenant, f.objectID)
				if err != nil {
					t.Fatalf("version locks: %v", err)
				}
				active := false
				for _, l := range locks {
					active = active || l.Active(time.Now(), true)
				}
				if !active {
					t.Fatalf("VersionLocks = %+v, want an active lock", locks)
				}
				// GetObject still describes the current version, which is unlocked.
				cur, err := objects.ObjectLock(ctx, f.tenant, f.objectID)
				if err != nil {
					t.Fatalf("object lock: %v", err)
				}
				if cur.Active(time.Now(), false) {
					t.Errorf("ObjectLock = %+v, want the current version's (no) lock", cur)
				}
			})

			t.Run("the delete matches no row instead of tripping the trigger", func(t *testing.T) {
				for _, bypass := range []bool{false, true} {
					err := inTx(t, ctx, f, bypass, func(tx pgx.Tx) error {
						return objects.HardDeleteTx(ctx, tx, f.tenant, f.objectID, 0)
					})
					var pgErr *pgconn.PgError
					if errors.As(err, &pgErr) {
						t.Fatalf("bypass=%v: the delete reached the trigger: %v", bypass, err)
					}
					if !errors.Is(err, objecth.ErrVersionMismatch) {
						t.Fatalf("bypass=%v: hard delete = %v, want the 0-row refusal", bypass, err)
					}
				}
				assertIntact(t, ctx, f)
			})

			t.Run("the lifecycle hard-deleter skips it", func(t *testing.T) {
				mustExec(t, ctx, f.pool,
					`UPDATE objects SET state = 'DELETED', terminated_at = now() - interval '365 days'
					  WHERE id = $1`, f.objectID)
				q := sqlc.New(f.pool)
				rows, err := q.ListHardDeletable(ctx, pgtype.Timestamptz{Time: time.Now(), Valid: true}, 100)
				if err != nil {
					t.Fatalf("list: %v", err)
				}
				for _, r := range rows {
					if uuid.UUID(r.ID.Bytes) == f.objectID {
						t.Fatal("an object with a locked older version was listed for hard delete")
					}
				}
				var rv int64
				if err := f.pool.QueryRow(ctx,
					`SELECT resource_version FROM objects WHERE id = $1`, f.objectID).Scan(&rv); err != nil {
					t.Fatalf("read version: %v", err)
				}
				n, err := q.HardDeleteObjectIfStillDeleted(ctx, pgtype.UUID{Bytes: f.objectID, Valid: true}, rv)
				if err != nil {
					t.Fatalf("worker delete reached the trigger: %v", err)
				}
				if n != 0 {
					t.Fatal("the worker's DELETE removed an object with a locked older version")
				}
				assertIntact(t, ctx, f)
			})
		})
	}
}

// The other half: once the older version's hold is released, nothing holds
// the object and the hard-deleter takes it.
func TestHardDeleterTakesTheObjectOnceAnOlderHoldIsReleased(t *testing.T) {
	t.Parallel()
	ctx, f := newLockFixture(t)
	if _, err := f.locks.SetLegalHold(ctx, f.tenant, f.versionID, true); err != nil {
		t.Fatalf("place hold: %v", err)
	}
	newer := uuid.NewV7()
	mustExec(t, ctx, f.pool,
		`INSERT INTO object_versions (id, tenant_id, object_id, storage_path)
		 VALUES ($1, $2, $3, 'tenant/docs/report.pdf')`, newer, f.tenant, f.objectID)
	mustExec(t, ctx, f.pool,
		`UPDATE objects SET current_version_id = $2, state = 'DELETED',
		        terminated_at = now() - interval '365 days'
		  WHERE id = $1`, f.objectID, newer)
	if _, err := f.locks.SetLegalHold(ctx, f.tenant, f.versionID, false); err != nil {
		t.Fatalf("release hold: %v", err)
	}

	q := sqlc.New(f.pool)
	rows, err := q.ListHardDeletable(ctx, pgtype.Timestamptz{Time: time.Now(), Valid: true}, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var rv int64
	listed := false
	for _, r := range rows {
		if uuid.UUID(r.ID.Bytes) == f.objectID {
			listed, rv = true, r.ResourceVersion
		}
	}
	if !listed {
		t.Fatal("an object whose older hold was released was not listed for hard delete")
	}
	n, err := q.HardDeleteObjectIfStillDeleted(ctx, pgtype.UUID{Bytes: f.objectID, Valid: true}, rv)
	if err != nil || n != 1 {
		t.Fatalf("worker delete = (%d, %v), want (1, nil)", n, err)
	}
}
