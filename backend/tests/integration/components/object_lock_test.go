//go:build integration

// Object Lock (ADR-0013) is a retention control, and a retention control is
// only worth what its worst case is worth: the case where the person who set
// it changes their mind, or the person auditing it is the person who could
// lift it. So these tests are written against the boundary rather than the
// happy path.
//
// The two modes differ in exactly one way and it is the whole feature:
//
//	GOVERNANCE has an override. A caller holding lock.governance.bypass can
//	shorten the window or delete through it. It protects against accident.
//
//	COMPLIANCE has none. Not the tenant admin, not the platform admin, not
//	the person who set it, not a direct SQL UPDATE through the application
//	role. It protects against intent, including your own.
//
// Legal hold is a third thing: no expiry, reversible, and deliberately not
// subject to the bypass — a hold exists to survive the person with the
// strongest role.
package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

type lockFixture struct {
	pool      *pgxpool.Pool
	locks     *adapters.ObjectLockRepo
	tenant    uuid.UUID
	objectID  uuid.UUID
	versionID uuid.UUID
}

func newLockFixture(t *testing.T) (context.Context, lockFixture) {
	t.Helper()
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")

	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind) VALUES ('lock-be', 's3-compatible')`)
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (backend_id, name, versioning_enabled, object_lock_enabled)
		 SELECT sb.id, 'lock-bucket', true, true FROM storage_backends sb WHERE sb.name = 'lock-be'`)
	var collectionID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO collections (tenant_id, name, bucket_id)
		 SELECT $1, 'docs', b.id FROM buckets b WHERE b.name = 'lock-bucket'
		 RETURNING id`, tenant).Scan(&collectionID); err != nil {
		t.Fatalf("seed collection: %v", err)
	}

	objectID := uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type)
		 VALUES ($1, $2, $3, 'report.pdf', 'AVAILABLE', 'application/pdf')`,
		objectID, tenant, collectionID)

	versionID := uuid.Must(uuid.NewV7())
	mustExec(t, ctx, pool,
		`INSERT INTO object_versions (id, tenant_id, object_id, storage_path)
		 VALUES ($1, $2, $3, 'tenant/docs/report.pdf')`, versionID, tenant, objectID)
	mustExec(t, ctx, pool,
		`UPDATE objects SET current_version_id = $2 WHERE id = $1`, objectID, versionID)

	return ctx, lockFixture{
		pool:      pool,
		locks:     adapters.NewObjectLockRepo(sqlc.New(pool)),
		tenant:    tenant,
		objectID:  objectID,
		versionID: versionID,
	}
}

func (f lockFixture) retain(t *testing.T, ctx context.Context, mode string, until time.Time, bypass bool) (objecth.ObjectLock, error) {
	t.Helper()
	return f.locks.SetRetention(ctx, objecth.SetRetentionArgs{
		TenantID:         f.tenant,
		VersionID:        f.versionID,
		Mode:             mode,
		RetainUntil:      until,
		BypassGovernance: bypass,
	})
}

// deleteObject attempts the removal a retention window is supposed to block,
// by the route production takes: DELETE the objects row, which cascades to
// object_versions and from there to object_locks, where the BEFORE DELETE
// trigger refuses.
//
// Deleting the version row directly would trip objects_current_version_fkey
// first and prove nothing about the lock.
func (f lockFixture) deleteObject(ctx context.Context, bypass bool) error {
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if bypass {
		if _, err := tx.Exec(ctx, "SET LOCAL paladin.bypass_governance_retention = 'on'"); err != nil {
			return err
		}
	}
	// The FK from objects.current_version_id is DEFERRABLE INITIALLY
	// DEFERRED, so the cascade resolves at commit rather than mid-statement.
	if _, err := tx.Exec(ctx, `DELETE FROM objects WHERE id = $1`, f.objectID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TestComplianceRetentionCannotBeWeakened is the property the whole mode
// exists for. Every one of these is a different way of asking "can I get out
// of this early", and the answer has to be no to all of them.
func TestComplianceRetentionCannotBeWeakened(t *testing.T) {
	ctx, f := newLockFixture(t)
	far := time.Now().Add(365 * 24 * time.Hour).UTC().Truncate(time.Millisecond)

	if _, err := f.retain(t, ctx, "COMPLIANCE", far, false); err != nil {
		t.Fatalf("set compliance retention: %v", err)
	}

	t.Run("cannot be shortened", func(t *testing.T) {
		_, err := f.retain(t, ctx, "COMPLIANCE", time.Now().Add(time.Hour), false)
		if !errors.Is(err, objecth.ErrRetentionWeakened) {
			t.Fatalf("shortening a COMPLIANCE window returned %v, want ErrRetentionWeakened", err)
		}
	})

	t.Run("cannot be shortened with the bypass flag", func(t *testing.T) {
		// The bypass exists for GOVERNANCE. If it worked here, the two modes
		// would be the same control with different names.
		_, err := f.retain(t, ctx, "COMPLIANCE", time.Now().Add(time.Hour), true)
		if !errors.Is(err, objecth.ErrRetentionWeakened) {
			t.Fatalf("bypass shortened a COMPLIANCE window (err=%v)", err)
		}
	})

	t.Run("cannot be downgraded to GOVERNANCE", func(t *testing.T) {
		_, err := f.retain(t, ctx, "GOVERNANCE", far.Add(24*time.Hour), false)
		if !errors.Is(err, objecth.ErrRetentionWeakened) {
			t.Fatalf("COMPLIANCE was downgraded to GOVERNANCE (err=%v)", err)
		}
	})

	t.Run("can still be extended", func(t *testing.T) {
		longer := far.Add(30 * 24 * time.Hour)
		got, err := f.retain(t, ctx, "COMPLIANCE", longer, false)
		if err != nil {
			t.Fatalf("extending a COMPLIANCE window was refused: %v", err)
		}
		if got.RetainUntil == nil || !got.RetainUntil.Equal(longer) {
			t.Errorf("retain_until = %v, want %v", got.RetainUntil, longer)
		}
	})

	t.Run("blocks deletion, bypass or not", func(t *testing.T) {
		if err := f.deleteObject(ctx, false); err == nil {
			t.Fatal("a COMPLIANCE-retained version was deleted")
		}
		if err := f.deleteObject(ctx, true); err == nil {
			t.Fatal("a COMPLIANCE-retained version was deleted with the governance bypass")
		}
	})

	t.Run("survives a direct UPDATE that skips the repository", func(t *testing.T) {
		// The rules also live in SetObjectRetention's WHERE clause, but a
		// clause is a property of one query. COMPLIANCE has to be a property
		// of the data: "no role can shorten it" has to include a role holding
		// a psql session. 007's BEFORE UPDATE trigger is what makes that true.
		if _, err := f.pool.Exec(ctx,
			`UPDATE object_locks SET retain_until = now() WHERE version_id = $1`, f.versionID); err == nil {
			t.Error("a direct UPDATE shortened a COMPLIANCE window")
		}
		if _, err := f.pool.Exec(ctx,
			`UPDATE object_locks SET mode = 'GOVERNANCE' WHERE version_id = $1`, f.versionID); err == nil {
			t.Error("a direct UPDATE downgraded COMPLIANCE to GOVERNANCE")
		}
		// Even with the bypass GUC set, which only ever applied to GOVERNANCE.
		if _, err := f.pool.Exec(ctx,
			`SET LOCAL paladin.bypass_governance_retention = 'on'`); err == nil {
			if _, err := f.pool.Exec(ctx,
				`UPDATE object_locks SET retain_until = now() WHERE version_id = $1`, f.versionID); err == nil {
				t.Error("the governance bypass shortened a COMPLIANCE window")
			}
		}
	})
}

// TestGovernanceRetentionYieldsToTheBypass pins the other mode: the same
// protections, and one documented way out for a caller who holds the role.
func TestGovernanceRetentionYieldsToTheBypass(t *testing.T) {
	ctx, f := newLockFixture(t)
	far := time.Now().Add(90 * 24 * time.Hour).UTC().Truncate(time.Millisecond)

	if _, err := f.retain(t, ctx, "GOVERNANCE", far, false); err != nil {
		t.Fatalf("set governance retention: %v", err)
	}

	t.Run("refuses shortening without the bypass", func(t *testing.T) {
		if _, err := f.retain(t, ctx, "GOVERNANCE", time.Now().Add(time.Hour), false); !errors.Is(err, objecth.ErrRetentionWeakened) {
			t.Fatalf("shortened without bypass (err=%v)", err)
		}
	})

	t.Run("blocks deletion without the bypass", func(t *testing.T) {
		if err := f.deleteObject(ctx, false); err == nil {
			t.Fatal("a GOVERNANCE-retained version was deleted without the bypass")
		}
	})

	t.Run("tightening to COMPLIANCE needs no bypass", func(t *testing.T) {
		// Same expiry, stronger mode. Not a release, so not gated.
		got, err := f.retain(t, ctx, "COMPLIANCE", far, false)
		if err != nil {
			t.Fatalf("tightening GOVERNANCE to COMPLIANCE was refused: %v", err)
		}
		if got.Mode != "COMPLIANCE" {
			t.Errorf("mode = %q, want COMPLIANCE", got.Mode)
		}
	})
}

// TestGovernanceBypassActuallyReachesTheTrigger is a regression test with a
// specific history: the writer set `paladin.governance_bypass` and the trigger
// read `paladin.bypass_governance_retention`. Two names for one switch means
// the switch was never on — and nobody noticed, because no lock row had ever
// been written for the trigger to fire on.
func TestGovernanceBypassActuallyReachesTheTrigger(t *testing.T) {
	ctx, f := newLockFixture(t)
	if _, err := f.retain(t, ctx, "GOVERNANCE", time.Now().Add(30*24*time.Hour), false); err != nil {
		t.Fatalf("set retention: %v", err)
	}

	if err := f.deleteObject(ctx, false); err == nil {
		t.Fatal("deleted without the bypass — the trigger is not firing at all")
	}
	if err := f.deleteObject(ctx, true); err != nil {
		t.Fatalf("the bypass did not reach the trigger: %v", err)
	}

	var remaining int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM objects WHERE id = $1`, f.objectID).Scan(&remaining); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if remaining != 0 {
		t.Error("the bypassed delete reported success but removed nothing")
	}
}

// TestLegalHoldIsAbsoluteAndReversible pins the third control. A hold has no
// expiry and no bypass, and it can be lifted — that combination is what makes
// it the right answer to "preserve this until the matter closes".
func TestLegalHoldIsAbsoluteAndReversible(t *testing.T) {
	ctx, f := newLockFixture(t)

	got, err := f.locks.SetLegalHold(ctx, f.tenant, f.versionID, true)
	if err != nil {
		t.Fatalf("place hold: %v", err)
	}
	if !got.LegalHold {
		t.Fatal("hold reported as not placed")
	}

	if err := f.deleteObject(ctx, false); err == nil {
		t.Fatal("a version under legal hold was deleted")
	}
	if err := f.deleteObject(ctx, true); err == nil {
		t.Fatal("the governance bypass lifted a legal hold — it must not")
	}

	got, err = f.locks.SetLegalHold(ctx, f.tenant, f.versionID, false)
	if err != nil {
		t.Fatalf("release hold: %v", err)
	}
	if got.LegalHold {
		t.Error("hold still reported after release")
	}
	if err := f.deleteObject(ctx, false); err != nil {
		t.Errorf("deletion still blocked after the hold was released: %v", err)
	}
}

// TestLegalHoldAndRetentionAreIndependent pins that releasing one does not
// release the other. Both are reasons to keep the object; either one alone is
// enough.
func TestLegalHoldAndRetentionAreIndependent(t *testing.T) {
	ctx, f := newLockFixture(t)
	until := time.Now().Add(24 * time.Hour)

	if _, err := f.retain(t, ctx, "GOVERNANCE", until, false); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	if _, err := f.locks.SetLegalHold(ctx, f.tenant, f.versionID, true); err != nil {
		t.Fatalf("place hold: %v", err)
	}

	// Releasing the hold must leave the retention row intact, not delete it.
	got, err := f.locks.SetLegalHold(ctx, f.tenant, f.versionID, false)
	if err != nil {
		t.Fatalf("release hold: %v", err)
	}
	if got.Mode != "GOVERNANCE" || got.RetainUntil == nil {
		t.Fatalf("releasing the hold dropped the retention: %+v", got)
	}
	if err := f.deleteObject(ctx, false); err == nil {
		t.Error("retention stopped blocking once the hold was released")
	}

	// And the reverse: a hold on top of an expired window still blocks.
	f2ctx, f2 := newLockFixture(t)
	mustExec(t, f2ctx, f2.pool,
		`INSERT INTO object_locks (tenant_id, version_id, mode, retain_until, legal_hold)
		 VALUES ($1, $2, 'GOVERNANCE', now() - interval '1 hour', true)`, f2.tenant, f2.versionID)
	if err := f2.deleteObject(f2ctx, true); err == nil {
		t.Error("a hold over an expired window did not block deletion")
	}
}

// TestExpiredRetentionStopsBlocking pins that a window actually ends. A
// retention control that never releases is a storage leak with a compliance
// story attached.
func TestExpiredRetentionStopsBlocking(t *testing.T) {
	ctx, f := newLockFixture(t)
	mustExec(t, ctx, f.pool,
		`INSERT INTO object_locks (tenant_id, version_id, mode, retain_until, legal_hold)
		 VALUES ($1, $2, 'COMPLIANCE', now() - interval '1 second', false)`, f.tenant, f.versionID)

	if err := f.deleteObject(ctx, false); err != nil {
		t.Fatalf("an expired COMPLIANCE window still blocked deletion: %v", err)
	}
}

// TestExpiredWindowMayBeReplaced pins the corollary: once a window has
// closed, a new one of any length may be set. Refusing that would make an
// expired COMPLIANCE lock permanently un-relockable, which is the opposite of
// the intent.
func TestExpiredWindowMayBeReplaced(t *testing.T) {
	ctx, f := newLockFixture(t)
	mustExec(t, ctx, f.pool,
		`INSERT INTO object_locks (tenant_id, version_id, mode, retain_until, legal_hold)
		 VALUES ($1, $2, 'COMPLIANCE', now() - interval '1 hour', false)`, f.tenant, f.versionID)

	short := time.Now().Add(time.Hour).UTC().Truncate(time.Millisecond)
	got, err := f.retain(t, ctx, "GOVERNANCE", short, false)
	if err != nil {
		t.Fatalf("replacing an expired window was refused: %v", err)
	}
	if got.Mode != "GOVERNANCE" {
		t.Errorf("mode = %q, want GOVERNANCE", got.Mode)
	}
}

// TestGetLockReportsUnlockedRatherThanMissing pins the read contract: an
// object with no lock is unlocked, which is an answer, not an error.
func TestGetLockReportsUnlockedRatherThanMissing(t *testing.T) {
	ctx, f := newLockFixture(t)

	got, err := f.locks.GetByVersion(ctx, f.versionID)
	if err != nil {
		t.Fatalf("reading an unlocked version errored: %v", err)
	}
	if got.Mode != "" || got.LegalHold || got.RetainUntil != nil {
		t.Errorf("unlocked version reported a lock: %+v", got)
	}

	until := time.Now().Add(time.Hour).UTC().Truncate(time.Millisecond)
	if _, err := f.retain(t, ctx, "GOVERNANCE", until, false); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	got, err = f.locks.GetByVersion(ctx, f.versionID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Mode != "GOVERNANCE" || got.RetainUntil == nil || !got.RetainUntil.Equal(until) {
		t.Errorf("read back %+v, want GOVERNANCE until %v", got, until)
	}
}

// TestBucketDefaultAppliesOnceAndDoesNotOverwrite pins the bucket-level
// default: it lands on a version that has no lock, and it never overrides an
// explicit retention that got there first. A default that clobbers an
// explicit choice is worse than no default.
func TestBucketDefaultAppliesOnceAndDoesNotOverwrite(t *testing.T) {
	ctx, f := newLockFixture(t)

	if err := f.locks.ApplyBucketDefault(ctx, f.tenant, f.versionID, "GOVERNANCE", 48*time.Hour); err != nil {
		t.Fatalf("apply default: %v", err)
	}
	got, err := f.locks.GetByVersion(ctx, f.versionID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Mode != "GOVERNANCE" || got.RetainUntil == nil {
		t.Fatalf("default did not apply: %+v", got)
	}
	first := *got.RetainUntil

	// A second promote-time application must not extend or reset the window.
	if err := f.locks.ApplyBucketDefault(ctx, f.tenant, f.versionID, "COMPLIANCE", 10*365*24*time.Hour); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	got, err = f.locks.GetByVersion(ctx, f.versionID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Mode != "GOVERNANCE" || !got.RetainUntil.Equal(first) {
		t.Errorf("the default overwrote an existing lock: %+v", got)
	}

	t.Run("no default configured is a no-op", func(t *testing.T) {
		_, f2 := newLockFixture(t)
		ctx2 := context.Background()
		if err := f2.locks.ApplyBucketDefault(ctx2, f2.tenant, f2.versionID, "", 24*time.Hour); err != nil {
			t.Fatalf("empty mode: %v", err)
		}
		if err := f2.locks.ApplyBucketDefault(ctx2, f2.tenant, f2.versionID, "GOVERNANCE", 0); err != nil {
			t.Fatalf("zero retention: %v", err)
		}
		got, err := f2.locks.GetByVersion(ctx2, f2.versionID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Mode != "" {
			t.Errorf("a bucket with no default still produced a lock: %+v", got)
		}
	})
}

// TestLockedObjectSurvivesHardDelete pins the guard on the objects table
// itself. The trigger protects the lock row; this clause protects the object
// whose current version that row covers, and they are separate defences
// because a DELETE on objects does not pass through object_locks.
func TestLockedObjectSurvivesHardDelete(t *testing.T) {
	ctx, f := newLockFixture(t)
	if _, err := f.retain(t, ctx, "COMPLIANCE", time.Now().Add(72*time.Hour), false); err != nil {
		t.Fatalf("set retention: %v", err)
	}

	repo := adapters.NewObjectRepo(sqlc.New(f.pool), f.pool)
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The DELETE's own NOT EXISTS clause refuses a locked row, so it matches
	// nothing and the adapter reports a version mismatch — the caller cannot
	// tell "locked" from "stale" from this alone, which is why the handler
	// pre-checks the lock for a precise error. What matters here is that the
	// row survives.
	if derr := repo.HardDeleteTx(ctx, tx, f.tenant, f.objectID, 0); derr == nil {
		t.Error("HardDelete reported success on a COMPLIANCE-locked object")
	}

	var remaining int
	if qerr := tx.QueryRow(ctx,
		`SELECT count(*) FROM objects WHERE id = $1`, f.objectID).Scan(&remaining); qerr != nil {
		t.Fatalf("verify: %v", qerr)
	}
	if remaining != 1 {
		t.Fatal("a COMPLIANCE-locked object was hard-deleted")
	}
}
