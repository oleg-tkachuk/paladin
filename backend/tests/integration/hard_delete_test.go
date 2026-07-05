//go:build integration

// LifecycleHardDeleter against real Postgres. Validates the full
// sweep: ListHardDeletable → storage.DeleteObject (recorded by
// fake) → HardDeleteObjectIfStillDeleted with OCC guard.
//
// Why integration: the OCC guard runs against a real
// resource_version that PostgreSQL's BEFORE-UPDATE trigger bumps on
// Restore. Mocking the trigger would lose the property under test.
package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/internal/worker"
	"github.com/oleg-tkachuk/paladin/tests/integration/pgharness"
)

// TestHardDelete_FullSweep: one DELETED row past the cooling-off
// window — worker calls storage.DeleteObject AND removes the DB row.
func TestHardDelete_FullSweep(t *testing.T) {
	h := pgharness.Setup(t)
	tenantID := mustCreateTenant(t, h.PoolMigrate, "hd-tenant")
	mustCreateObjectKey(t, h.PoolMigrate, tenantID, "docs")
	objectID := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "deletable")

	// Soft-delete with terminated_at well in the past so the worker
	// picks it up on the next sweep regardless of TTL.
	mustSoftDeleteWithBackdate(t, h.PoolMigrate, objectID, 24*time.Hour)

	storage := &recordingStorage{}
	w := &worker.LifecycleHardDeleter{
		Q:         sqlc.New(h.PoolMigrate),
		Storage:   storage,
		TTL:       1 * time.Hour, // anything < 24h triggers the row
		BatchSize: 100,
	}
	w.Sweep(context.Background())

	storage.mu.Lock()
	calls := append([]recordedDelete(nil), storage.calls...)
	storage.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("storage DELETE calls = %d, want 1: %+v", len(calls), calls)
	}
	if calls[0].bucket != "paladin-test" || calls[0].key != "deletable" {
		t.Errorf("storage call = %+v", calls[0])
	}

	// DB row must be gone.
	if exists := mustObjectExists(t, h.PoolMigrate, objectID); exists {
		t.Errorf("DB row still present after hard-delete")
	}
}

// TestHardDelete_RLSPoolFindsNothing pins the reaper-RLS invariant behind the
// whole class of "cross-tenant reaper silently no-ops" bugs: a LifecycleHardDeleter
// wired to the RLS paladin_app pool with NO tenant GUC (exactly a background reaper's
// context) sweeps ZERO objects — RLS hides every row — while the same sweep on the
// BYPASSRLS pool reclaims it. Guards serve_worker's reaper-pool wiring: reapers
// MUST run on the BYPASSRLS pool, never deps.Pool.
func TestHardDelete_RLSPoolFindsNothing(t *testing.T) {
	h := pgharness.Setup(t)
	tenantID := mustCreateTenant(t, h.PoolMigrate, "rls-reaper")
	mustCreateObjectKey(t, h.PoolMigrate, tenantID, "docs")
	objectID := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "leaky")
	mustSoftDeleteWithBackdate(t, h.PoolMigrate, objectID, 24*time.Hour)

	sweptCount := func(pool *pgxpool.Pool) int {
		st := &recordingStorage{}
		w := &worker.LifecycleHardDeleter{Q: sqlc.New(pool), Storage: st, TTL: time.Hour, BatchSize: 100}
		w.Sweep(context.Background()) // bare ctx → no paladin.tenant_id GUC
		st.mu.Lock()
		defer st.mu.Unlock()
		return len(st.calls)
	}

	// BUG condition: RLS pool + no GUC → RLS returns zero hard-deletable rows.
	if n := sweptCount(h.PoolApp); n != 0 {
		t.Fatalf("RLS pool (no tenant GUC) swept %d objects; want 0 — a reaper on the RLS pool must find nothing", n)
	}
	if !mustObjectExists(t, h.PoolMigrate, objectID) {
		t.Fatal("object hard-deleted via the RLS pool; RLS should have hidden it entirely")
	}
	// FIX: BYPASSRLS pool reclaims the same object cross-tenant.
	if n := sweptCount(h.PoolMigrate); n != 1 {
		t.Fatalf("BYPASSRLS pool swept %d objects; want 1", n)
	}
}

// TestHardDelete_RestoreWinsRace: a row gets Restored mid-sweep
// (after ListHardDeletable runs but before HardDeleteObjectIfStillDeleted
// fires). The OCC guard makes the DB DELETE no-op; the row stays
// AVAILABLE. The S3 DELETE we issued is the race-loss case the
// worker contract documents — this test pins that we don't ALSO
// drop the DB row.
func TestHardDelete_RestoreWinsRace(t *testing.T) {
	h := pgharness.Setup(t)
	tenantID := mustCreateTenant(t, h.PoolMigrate, "hd-restore")
	mustCreateObjectKey(t, h.PoolMigrate, tenantID, "docs")
	objectID := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "racy")
	mustSoftDeleteWithBackdate(t, h.PoolMigrate, objectID, 24*time.Hour)

	// Hand-roll the race: read the row via ListHardDeletable to
	// capture resource_version, then Restore (bumps version), then
	// call HardDeleteObjectIfStillDeleted with the stale version.
	q := sqlc.New(h.PoolMigrate)
	cutoff := pgxTimestamp(time.Now().Add(-1 * time.Hour))
	rows, err := q.ListHardDeletable(context.Background(), cutoff, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("seed list: rows=%d err=%v", len(rows), err)
	}
	staleVersion := rows[0].ResourceVersion

	tr := statemachine.New(h.PoolMigrate)
	if err := tr.Restore(context.Background(), objectID); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// Now run with the stale version — DELETE must affect 0 rows.
	n, err := q.HardDeleteObjectIfStillDeleted(context.Background(), rows[0].ObjectID, staleVersion)
	if err != nil {
		t.Fatalf("HardDeleteObjectIfStillDeleted: %v", err)
	}
	if n != 0 {
		t.Errorf("rows affected = %d, want 0 (Restore should have bumped version)", n)
	}
	if !mustObjectExists(t, h.PoolMigrate, objectID) {
		t.Error("row should still exist (Restored)")
	}
}

// ─── helpers ────────────────────────────────────────────────────────

type recordedDelete struct {
	bucket, objectKey, key string
}

type recordingStorage struct {
	mu    sync.Mutex
	calls []recordedDelete
}

func (s *recordingStorage) DeleteObject(_ context.Context, _ string, bucket string, _ uuid.UUID, objectKey, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, recordedDelete{bucket: bucket, objectKey: objectKey, key: key})
	return nil
}

// mustSoftDeleteWithBackdate flips the row to DELETED with a
// terminated_at value in the past so the worker's cooling-off
// window doesn't gate the test.
func mustSoftDeleteWithBackdate(t *testing.T, pool *pgxpool.Pool, objectID uuid.UUID, backdate time.Duration) {
	t.Helper()
	// Postgres interval cast: "<seconds> seconds" parses reliably
	// across versions; time.Duration.String() ("24h0m0s") doesn't.
	intervalSec := int64(backdate.Seconds())
	_, err := pool.Exec(context.Background(), `
        UPDATE objects
        SET state = 'DELETED',
            terminated_at = now() - ($2::bigint || ' seconds')::interval
        WHERE object_id = $1
    `, objectID, intervalSec)
	if err != nil {
		t.Fatalf("backdate soft-delete: %v", err)
	}
}

func mustObjectExists(t *testing.T, pool *pgxpool.Pool, objectID uuid.UUID) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM objects WHERE object_id = $1)`, objectID,
	).Scan(&exists); err != nil {
		t.Fatalf("exists check: %v", err)
	}
	return exists
}

// pgxTimestamp wraps time.Time into the pgtype.Timestamptz the sqlc
// generated signatures take.
func pgxTimestamp(t time.Time) pgtypeTS {
	return pgtypeTS{Time: t, Valid: true}
}

// pgtypeTS aliases pgtype.Timestamptz so the import lives in one
// place; the helper stays terse.
type pgtypeTS = pgtype.Timestamptz
