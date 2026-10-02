//go:build integration

package components

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// An operation whose worker stopped between the claim and the terminal write
// is invisible to the rest of the system: ClaimNext takes only PENDING, and
// the reaper only terminal states. One sat RUNNING for two days while the
// identical operation, retried nine minutes later, finished in 2.5 seconds.
func TestReclaimStaleOperations(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// "The outcome is unknown" is honest but useless to a caller deciding whether
// to reissue a batch. The runner leaves a {processed, total} snapshot in
// metadata as it goes, so the reclaim can say how far the work got — and must
// survive metadata that is not a progress snapshot at all, which is what an
// operation that died before its first report leaves behind.
func TestReclaimCarriesLastProgress(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	withProgress, beforeFirstReport, notJSON := uuid.New(), uuid.New(), uuid.New()
	for id, meta := range map[uuid.UUID][]byte{
		withProgress:      []byte(`{"processed":7,"total":9}`),
		beforeFirstReport: []byte(`{"object_ids":["a","b"]}`),
		notJSON:           []byte(`not json at all`),
	} {
		mustExec(t, ctx, pool,
			`INSERT INTO operations (id, tenant_id, type, state, metadata, created_at, updated_at)
			 VALUES ($1, $2, 'BatchUpdateTags', 'RUNNING', $3,
			         now() - interval '1 hour', now() - interval '1 hour')`,
			id, tenant, meta)
	}

	n, err := repo.ReclaimStale(ctx, 15*time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 3 {
		t.Fatalf("reclaimed %d rows, want 3 — one bad metadata payload must not "+
			"take the others down with it", n)
	}

	var progressed, unknown, unparsed []byte
	for id, dst := range map[uuid.UUID]*[]byte{
		withProgress: &progressed, beforeFirstReport: &unknown, notJSON: &unparsed,
	} {
		if err := pool.QueryRow(ctx,
			`SELECT response FROM operations WHERE id = $1`, id).Scan(dst); err != nil {
			t.Fatalf("read response: %v", err)
		}
	}

	var got struct {
		Code         string `json:"code"`
		LastProgress *struct {
			Processed int `json:"processed"`
			Total     int `json:"total"`
		} `json:"last_progress"`
	}
	if err := json.Unmarshal(progressed, &got); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, progressed)
	}
	if got.Code != "WORKER_LOST" {
		t.Errorf("code = %q, want WORKER_LOST", got.Code)
	}
	if got.LastProgress == nil {
		t.Fatal("last_progress missing — the snapshot is the only record of how far the work got")
	}
	if got.LastProgress.Processed != 7 || got.LastProgress.Total != 9 {
		t.Errorf("last_progress = %d/%d, want 7/9",
			got.LastProgress.Processed, got.LastProgress.Total)
	}

	for name, raw := range map[string][]byte{
		"executor arguments": unknown, "unparsable metadata": unparsed,
	} {
		got.LastProgress = nil
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("%s: response is not JSON: %v (%s)", name, err, raw)
		}
		if got.LastProgress != nil {
			t.Errorf("%s: reported progress %+v that was never measured",
				name, *got.LastProgress)
		}
	}
}
