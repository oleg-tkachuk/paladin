//go:build integration

// A tenant in the trash leaves it two ways: restored, or purged. Purging is
// the one that destroys data, and Object Lock (ADR-0013) exists so that some
// data cannot be destroyed — not by the tenant, not by the platform. These
// tests walk every route from a trashed tenant to its removal and check that
// each one stops at an object under legal hold or COMPLIANCE retention:
//
//   - the purge itself: PurgeTenant's DELETE on tenants, through the
//     repository and as raw SQL, with and without the governance bypass;
//   - emptying the tenant first: deleting its collection, hard-deleting its
//     object through the repository (PermanentDelete) and through the
//     lifecycle hard-deleter's queries, and the raw DELETEs underneath them;
//   - a lock that is not on the current version (hard_delete_version_lock_test.go
//     covers how the hard-delete queries treat one).
//
// Until 061 the purge was stopped by collections_tenant_id_fkey — every
// object needs a collection — so a locked object was protected as a side
// effect of protecting any object, and the refusal named "collections". The
// purge now has a check of its own that runs before any foreign key.
package components

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// purgeLock is one of the two locks no role can lift by deleting through it.
type purgeLock struct {
	name  string
	apply func(t *testing.T, ctx context.Context, f lockFixture, versionID uuid.UUID)
}

var purgeLocks = []purgeLock{
	{"legal hold", func(t *testing.T, ctx context.Context, f lockFixture, v uuid.UUID) {
		t.Helper()
		if _, err := f.locks.SetLegalHold(ctx, f.tenant, v, true); err != nil {
			t.Fatalf("place hold: %v", err)
		}
	}},
	{"COMPLIANCE retention", func(t *testing.T, ctx context.Context, f lockFixture, v uuid.UUID) {
		t.Helper()
		mustExec(t, ctx, f.pool,
			`INSERT INTO object_locks (tenant_id, version_id, mode, retain_until)
			 VALUES ($1, $2, 'COMPLIANCE', now() + interval '30 days')`, f.tenant, v)
	}},
}

// trash moves the fixture's tenant to the trash the way DeleteTenant does.
func trash(t *testing.T, ctx context.Context, f lockFixture) {
	t.Helper()
	repo := adapters.NewTenantRepo(sqlc.New(f.pool), f.pool)
	if err := repo.SoftDelete(ctx, f.tenant, 0); err != nil {
		t.Fatalf("trash tenant: %v", err)
	}
}

// inTx runs fn in a transaction that always rolls back, optionally with the
// governance bypass set — which must make no difference to either lock.
func inTx(t *testing.T, ctx context.Context, f lockFixture, bypass bool, fn func(tx pgx.Tx) error) error {
	t.Helper()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if bypass {
		if _, err := tx.Exec(ctx, "SET LOCAL paladin.bypass_governance_retention = 'on'"); err != nil {
			t.Fatalf("set bypass: %v", err)
		}
	}
	if err := fn(tx); err != nil {
		return err
	}
	// Deferred constraints (objects_current_version_fkey) resolve here.
	return tx.Commit(ctx)
}

func rowCount(t *testing.T, ctx context.Context, f lockFixture, sql string, arg any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(ctx, sql, arg).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// assertIntact is the outcome every path must leave: tenant, object, version
// and lock all still there.
func assertIntact(t *testing.T, ctx context.Context, f lockFixture) {
	t.Helper()
	if rowCount(t, ctx, f, `SELECT count(*) FROM tenants WHERE id = $1`, f.tenant) != 1 {
		t.Fatal("the tenant was removed while it held a locked object")
	}
	if rowCount(t, ctx, f, `SELECT count(*) FROM objects WHERE id = $1`, f.objectID) != 1 {
		t.Fatal("a locked object was removed")
	}
	if rowCount(t, ctx, f, `SELECT count(*) FROM object_locks WHERE tenant_id = $1`, f.tenant) == 0 {
		t.Fatal("the lock row was removed")
	}
}

func TestTrashedTenantWithLockedObjectCannotBePurged(t *testing.T) {
	t.Parallel()
	for _, lock := range purgeLocks {
		t.Run(lock.name, func(t *testing.T) {
			t.Parallel()
			ctx, f := newLockFixture(t)
			lock.apply(t, ctx, f, f.versionID)
			trash(t, ctx, f)
			tenants := adapters.NewTenantRepo(sqlc.New(f.pool), f.pool)

			t.Run("PurgeTenant refuses, naming the lock", func(t *testing.T) {
				err := tenants.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
					return tenants.HardDeleteTx(ctx, tx, f.tenant, 0)
				})
				if !errors.Is(err, tenanth.ErrTenantHoldsLockedObjects) {
					t.Fatalf("purge returned %v, want ErrTenantHoldsLockedObjects", err)
				}
				// The lock answers first, not whichever child table the
				// foreign keys happen to check first.
				if strings.Contains(err.Error(), "collections") {
					t.Errorf("the refusal named a child table, not the lock.\ngot: %v", err)
				}
				assertIntact(t, ctx, f)
			})

			for _, bypass := range []bool{false, true} {
				t.Run("raw DELETE on tenants refuses, bypass="+map[bool]string{false: "off", true: "on"}[bypass], func(t *testing.T) {
					err := inTx(t, ctx, f, bypass, func(tx pgx.Tx) error {
						_, e := tx.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, f.tenant)
						return e
					})
					if err == nil {
						t.Fatal("a raw DELETE removed a tenant holding a locked object")
					}
					assertIntact(t, ctx, f)
				})
			}

			t.Run("the guard does not depend on the session's tenant scope", func(t *testing.T) {
				// objects/object_versions/object_locks are under FORCE RLS. As
				// the runtime role scoped to some other tenant, a guard that
				// read them with the caller's visibility would see nothing and
				// fall through to the foreign keys. The trigger reads as its
				// owner, which bypasses RLS, so the refusal is still the lock.
				err := inTx(t, ctx, f, false, func(tx pgx.Tx) error {
					if _, e := tx.Exec(ctx, `SET LOCAL ROLE paladin_app`); e != nil {
						t.Fatalf("set role: %v", e)
					}
					if _, e := tx.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, true)`, uuid.New().String()); e != nil {
						t.Fatalf("scope session: %v", e)
					}
					_, e := tx.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, f.tenant)
					return e
				})
				if err == nil || !strings.Contains(err.Error(), "legal hold or retention") {
					t.Fatalf("as paladin_app, purge returned %v; want the lock guard's refusal", err)
				}
				assertIntact(t, ctx, f)
			})
		})
	}
}

// TestTrashedTenantCannotBeEmptiedPastALock covers the other way to the same
// end: remove what the tenant owns, then purge an empty tenant. Each step
// that would remove the locked object has to refuse on its own.
func TestTrashedTenantCannotBeEmptiedPastALock(t *testing.T) {
	t.Parallel()
	for _, lock := range purgeLocks {
		t.Run(lock.name, func(t *testing.T) {
			t.Parallel()
			ctx, f := newLockFixture(t)
			lock.apply(t, ctx, f, f.versionID)
			trash(t, ctx, f)

			t.Run("collection delete refuses", func(t *testing.T) {
				err := inTx(t, ctx, f, false, func(tx pgx.Tx) error {
					_, e := tx.Exec(ctx, `DELETE FROM collections WHERE tenant_id = $1`, f.tenant)
					return e
				})
				if err == nil {
					t.Fatal("the collection holding a locked object was deleted")
				}
				assertIntact(t, ctx, f)
			})

			t.Run("PermanentDelete's repository call refuses", func(t *testing.T) {
				objects := adapters.NewObjectRepo(sqlc.New(f.pool), f.pool)
				for _, bypass := range []bool{false, true} {
					err := inTx(t, ctx, f, false, func(tx pgx.Tx) error {
						if bypass {
							return objects.HardDeleteWithBypassTx(ctx, tx, f.tenant, f.objectID, 0)
						}
						return objects.HardDeleteTx(ctx, tx, f.tenant, f.objectID, 0)
					})
					if err == nil {
						t.Fatalf("hard delete (bypass=%v) reported success on a locked object", bypass)
					}
				}
				assertIntact(t, ctx, f)
			})

			t.Run("lifecycle hard-deleter neither lists nor deletes it", func(t *testing.T) {
				mustExec(t, ctx, f.pool,
					`UPDATE objects SET state = 'DELETED', terminated_at = now() - interval '365 days'
					  WHERE id = $1`, f.objectID)
				q := sqlc.New(f.pool)
				// Checked with the tenant both trashed and restored: the trash
				// freeze is one reason to skip the row, and the lock has to be
				// enough on its own.
				for _, trashed := range []bool{true, false} {
					if !trashed {
						mustExec(t, ctx, f.pool, `UPDATE tenants SET deleted_at = NULL WHERE id = $1`, f.tenant)
					}
					rows, err := q.ListHardDeletable(ctx,
						pgtype.Timestamptz{Time: time.Now(), Valid: true}, 100)
					if err != nil {
						t.Fatalf("list: %v", err)
					}
					for _, r := range rows {
						if uuid.UUID(r.ID.Bytes) == f.objectID {
							t.Errorf("trashed=%v: a locked object was listed for hard delete", trashed)
						}
					}
					var rv int64
					if err := f.pool.QueryRow(ctx,
						`SELECT resource_version FROM objects WHERE id = $1`, f.objectID).Scan(&rv); err != nil {
						t.Fatalf("read version: %v", err)
					}
					n, err := q.HardDeleteObjectIfStillDeleted(ctx, pgtype.UUID{Bytes: f.objectID, Valid: true}, rv)
					if err != nil {
						t.Fatalf("worker delete: %v", err)
					}
					if n != 0 {
						t.Fatalf("trashed=%v: the worker's DELETE removed a locked object", trashed)
					}
				}
				mustExec(t, ctx, f.pool, `UPDATE tenants SET deleted_at = now() WHERE id = $1`, f.tenant)
				assertIntact(t, ctx, f)
			})

			t.Run("raw DELETEs underneath refuse, bypass or not", func(t *testing.T) {
				for _, bypass := range []bool{false, true} {
					for _, stmt := range []string{
						`DELETE FROM objects WHERE tenant_id = $1`,
						`DELETE FROM object_locks WHERE tenant_id = $1`,
					} {
						err := inTx(t, ctx, f, bypass, func(tx pgx.Tx) error {
							_, e := tx.Exec(ctx, stmt, f.tenant)
							return e
						})
						if err == nil {
							t.Errorf("bypass=%v: %q removed a locked object", bypass, stmt)
						}
					}
				}
				assertIntact(t, ctx, f)
			})
		})
	}
}

// TestPurgeRefusesALockTheForeignKeysCannotSee pins that the purge guard is a
// check of its own. The tenant here owns no collection, no user and no
// object, so no RESTRICT foreign key stands in the way; the only thing
// between the purge and a lock row is the lock. Before 061 the cascade into
// object_locks still tripped the retention trigger, but as an unmapped
// Internal error; now it is the same precondition as any other locked purge.
func TestPurgeRefusesALockTheForeignKeysCannotSee(t *testing.T) {
	t.Parallel()
	for _, lock := range purgeLocks {
		t.Run(lock.name, func(t *testing.T) {
			t.Parallel()
			ctx, f := newLockFixture(t)
			bare, _ := mkTenant(t, ctx, f.pool, "shared")
			// A lock row that names the bare tenant. The lock is the fixture
			// tenant's object's; only the row's tenant_id differs.
			g := f
			g.tenant = bare
			lock.apply(t, ctx, g, f.versionID)
			mustExec(t, ctx, f.pool, `UPDATE tenants SET deleted_at = now() WHERE id = $1`, bare)

			tenants := adapters.NewTenantRepo(sqlc.New(f.pool), f.pool)
			err := tenants.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
				return tenants.HardDeleteTx(ctx, tx, bare, 0)
			})
			if !errors.Is(err, tenanth.ErrTenantHoldsLockedObjects) {
				t.Fatalf("purge returned %v, want ErrTenantHoldsLockedObjects", err)
			}
			if rowCount(t, ctx, f, `SELECT count(*) FROM object_locks WHERE version_id = $1`, f.versionID) != 1 {
				t.Fatal("the purge removed the lock row")
			}
		})
	}
}

// TestLockOnANonCurrentVersionStillBlocks covers the gap in the hard-delete
// queries' NOT EXISTS clause, which looks only at the CURRENT version's lock.
// A hold on an older version is not seen there; the cascade from objects to
// object_versions to object_locks is what reaches it, and the retention
// trigger refuses. The object must survive, through the repository and raw.
func TestLockOnANonCurrentVersionStillBlocks(t *testing.T) {
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
			trash(t, ctx, f)

			objects := adapters.NewObjectRepo(sqlc.New(f.pool), f.pool)
			if err := inTx(t, ctx, f, false, func(tx pgx.Tx) error {
				return objects.HardDeleteTx(ctx, tx, f.tenant, f.objectID, 0)
			}); err == nil {
				t.Fatal("hard delete removed an object whose older version is locked")
			}
			tenants := adapters.NewTenantRepo(sqlc.New(f.pool), f.pool)
			err := tenants.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
				return tenants.HardDeleteTx(ctx, tx, f.tenant, 0)
			})
			if !errors.Is(err, tenanth.ErrTenantHoldsLockedObjects) {
				t.Fatalf("purge returned %v, want ErrTenantHoldsLockedObjects", err)
			}
			assertIntact(t, ctx, f)
		})
	}
}

// TestPurgeProceedsOnceTheLockIsReleased pins the other half: the guard
// refuses locks, not tenants. A released hold or an expired window holds
// nothing, and the purge then fails only for the ordinary reason — the tenant
// still owns a collection — which is the existing contract.
func TestPurgeProceedsOnceTheLockIsReleased(t *testing.T) {
	t.Parallel()
	ctx, f := newLockFixture(t)
	if _, err := f.locks.SetLegalHold(ctx, f.tenant, f.versionID, true); err != nil {
		t.Fatalf("place hold: %v", err)
	}
	if _, err := f.locks.SetLegalHold(ctx, f.tenant, f.versionID, false); err != nil {
		t.Fatalf("release hold: %v", err)
	}
	trash(t, ctx, f)

	tenants := adapters.NewTenantRepo(sqlc.New(f.pool), f.pool)
	err := tenants.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tenants.HardDeleteTx(ctx, tx, f.tenant, 0)
	})
	if errors.Is(err, tenanth.ErrTenantHoldsLockedObjects) {
		t.Fatalf("a released hold still blocked the purge: %v", err)
	}
	if !errors.Is(err, tenanth.ErrTenantHasChildren) {
		t.Fatalf("purge returned %v, want ErrTenantHasChildren (the collection)", err)
	}

	// Empty it the ordinary way, with an expired COMPLIANCE window on the
	// version for good measure, and the purge goes through.
	mustExec(t, ctx, f.pool,
		`UPDATE object_locks SET mode = 'COMPLIANCE', retain_until = now() - interval '1 second'
		  WHERE version_id = $1`, f.versionID)
	mustExec(t, ctx, f.pool, `DELETE FROM objects WHERE id = $1`, f.objectID)
	mustExec(t, ctx, f.pool, `DELETE FROM collections WHERE tenant_id = $1`, f.tenant)
	if err := tenants.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tenants.HardDeleteTx(ctx, tx, f.tenant, 0)
	}); err != nil {
		t.Fatalf("purging an emptied tenant: %v", err)
	}
}
