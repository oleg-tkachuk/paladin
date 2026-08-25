//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// An operation whose worker stopped between the claim and the terminal write
// is invisible to the rest of the system: ClaimNext takes only PENDING, and
// the reaper only terminal states. One sat RUNNING for two days while the
// identical operation, retried nine minutes later, finished in 2.5 seconds.
func TestReclaimStaleOperations(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	// Two RUNNING rows: one whose heartbeat is old, one that just beat.
	stale := uuid.New()
	fresh := uuid.New()
	for id, age := range map[uuid.UUID]time.Duration{stale: time.Hour, fresh: 0} {
		mustExec(t, ctx, pool,
			`INSERT INTO operations (id, tenant_id, type, state, created_at, updated_at)
			 VALUES ($1, $2, 'BatchUpdateTags', 'RUNNING', now() - $3::interval, now() - $3::interval)`,
			id, tenant, age.String())
	}

	n, err := repo.ReclaimStale(ctx, 15*time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed %d rows, want 1 — the live operation must be left alone", n)
	}

	var state, code string
	if err := pool.QueryRow(ctx,
		`SELECT state::text, coalesce(error_code,'') FROM operations WHERE id = $1`, stale).
		Scan(&state, &code); err != nil {
		t.Fatalf("read stale: %v", err)
	}
	if state != "FAILED" || code != "WORKER_LOST" {
		t.Errorf("stale operation = %s/%s, want FAILED/WORKER_LOST", state, code)
	}
	var doneAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT done_at FROM operations WHERE id = $1`, stale).Scan(&doneAt); err != nil {
		t.Fatalf("read done_at: %v", err)
	}
	if doneAt == nil {
		t.Error("done_at not set — the reaper keys on it, so the row would never be cleaned up")
	}

	if err := pool.QueryRow(ctx,
		`SELECT state::text FROM operations WHERE id = $1`, fresh).Scan(&state); err != nil {
		t.Fatalf("read fresh: %v", err)
	}
	if state != "RUNNING" {
		t.Errorf("live operation was reclaimed: state = %s", state)
	}
}

// The heartbeat is what keeps a long operation out of the reclaimer's reach,
// so it has to actually move updated_at — and touch nothing else, because
// metadata carries progress counters the executor writes concurrently.
func TestTouchOperationKeepsItOutOfReclaim(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	id := uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO operations (id, tenant_id, type, state, metadata, created_at, updated_at)
		 VALUES ($1, $2, 'BatchUpdateTags', 'RUNNING', $3, now() - interval '1 hour', now() - interval '1 hour')`,
		id, tenant, []byte(`{"processed":7,"total":9}`))

	if err := repo.Touch(ctx, id); err != nil {
		t.Fatalf("touch: %v", err)
	}
	n, err := repo.ReclaimStale(ctx, 15*time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 0 {
		t.Errorf("reclaimed %d rows after a heartbeat, want 0", n)
	}

	var meta []byte
	if err := pool.QueryRow(ctx, `SELECT metadata FROM operations WHERE id = $1`, id).Scan(&meta); err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if string(meta) != `{"processed":7,"total":9}` {
		t.Errorf("heartbeat rewrote metadata: %s", meta)
	}
}

// A terminal operation must never be resurrected by the reclaim, whatever its
// age — the query keys on state, and getting that wrong would rewrite history.
func TestReclaimLeavesTerminalOperationsAlone(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	id := uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO operations (id, tenant_id, type, state, created_at, updated_at, done_at)
		 VALUES ($1, $2, 'BatchUpdateTags', 'SUCCEEDED', now() - interval '2 days',
		         now() - interval '2 days', now() - interval '2 days')`,
		id, tenant)

	if _, err := repo.ReclaimStale(ctx, time.Minute); err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT state::text FROM operations WHERE id = $1`, id).Scan(&state); err != nil {
		t.Fatalf("read: %v", err)
	}
	if state != "SUCCEEDED" {
		t.Errorf("terminal operation rewritten to %s", state)
	}
}
