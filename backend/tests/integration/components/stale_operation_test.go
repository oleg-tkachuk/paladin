//go:build integration

package components

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
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
// the executor writes its progress concurrently.
func TestTouchOperationKeepsItOutOfReclaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	id := uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO operations (id, tenant_id, type, state, progress, created_at, updated_at)
		 VALUES ($1, $2, 'BatchUpdateTags', 'RUNNING', '{"processed":7,"total":9}', now() - interval '1 hour', now() - interval '1 hour')`,
		id, tenant)

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

	var progress string
	if err := pool.QueryRow(ctx, `SELECT progress::text FROM operations WHERE id = $1`, id).Scan(&progress); err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if progress != `{"total": 9, "processed": 7}` {
		t.Errorf("heartbeat rewrote progress: %s", progress)
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
// progress as it goes, so the reclaim can say how far the work got; an
// operation that died before its first report has none. Either way the
// executor's arguments in metadata outlive the reclaim — they are what the
// operation was for.
func TestReclaimCarriesLastProgress(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	const args = `{"object_ids":["a","b"]}`
	withProgress, beforeFirstReport := uuid.New(), uuid.New()
	for id, progress := range map[uuid.UUID]*string{
		withProgress:      ptr(`{"processed":7,"total":9}`),
		beforeFirstReport: nil,
	} {
		mustExec(t, ctx, pool,
			`INSERT INTO operations (id, tenant_id, type, state, metadata, progress, created_at, updated_at)
			 VALUES ($1, $2, 'BatchUpdateTags', 'RUNNING', $3, $4::jsonb,
			         now() - interval '1 hour', now() - interval '1 hour')`,
			id, tenant, []byte(args), progress)
	}

	n, err := repo.ReclaimStale(ctx, 15*time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 2 {
		t.Fatalf("reclaimed %d rows, want 2", n)
	}

	for _, id := range []uuid.UUID{withProgress, beforeFirstReport} {
		var meta []byte
		if err := pool.QueryRow(ctx, `SELECT metadata FROM operations WHERE id = $1`, id).Scan(&meta); err != nil {
			t.Fatalf("read metadata: %v", err)
		}
		if string(meta) != args {
			t.Errorf("reclaimed operation's metadata = %q, want its arguments %s", meta, args)
		}
	}

	var progressed, unknown []byte
	for id, dst := range map[uuid.UUID]*[]byte{
		withProgress: &progressed, beforeFirstReport: &unknown,
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

	got.LastProgress = nil
	if err := json.Unmarshal(unknown, &got); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, unknown)
	}
	if got.LastProgress != nil {
		t.Errorf("reported progress %+v that was never measured", *got.LastProgress)
	}
}

// A finished operation stays finished. The runner writes progress and then its
// result without re-reading the state, so a cancel — or the reclaimer's
// WORKER_LOST — used to be overwritten: progress put the row back to RUNNING
// and the result to SUCCEEDED, against CancelOperation's contract.
func TestOperationStateWritesKeepTerminalStates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	const args = `{"object_ids":["a"]}`
	running := func() uuid.UUID {
		id := uuid.New()
		mustExec(t, ctx, pool,
			`INSERT INTO operations (id, tenant_id, type, state, metadata) VALUES ($1, $2, 'BatchUpdateTags', 'RUNNING', $3)`,
			id, tenant, []byte(args))
		return id
	}
	stateOf := func(id uuid.UUID) string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT state::text FROM operations WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatalf("read state: %v", err)
		}
		return s
	}
	progress := []byte(`{"processed":1,"total":2}`)

	cancelled := running()
	if err := repo.Cancel(ctx, cancelled, tenant); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	reclaimed := running()
	mustExec(t, ctx, pool, `UPDATE operations SET updated_at = now() - interval '1 hour' WHERE id = $1`, reclaimed)
	if _, err := repo.ReclaimStale(ctx, 15*time.Minute); err != nil {
		t.Fatalf("reclaim: %v", err)
	}

	for name, tc := range map[string]struct {
		id   uuid.UUID
		want string
	}{
		"cancelled": {cancelled, "CANCELLED"},
		"reclaimed": {reclaimed, "FAILED"},
	} {
		if err := repo.ReportProgress(ctx, tc.id, progress); !errors.Is(err, operationh.ErrOperationFinished) {
			t.Errorf("%s: late progress returned %v, want ErrOperationFinished", name, err)
		}
		if err := repo.UpdateState(ctx, tc.id, operationh.StateSucceeded, []byte(`{}`), 0, "", ""); !errors.Is(err, operationh.ErrOperationFinished) {
			t.Errorf("%s: late result returned %v, want ErrOperationFinished", name, err)
		}
		if got := stateOf(tc.id); got != tc.want {
			t.Errorf("%s: late writes turned it into %s", name, got)
		}
	}

	live := running()
	if err := repo.ReportProgress(ctx, live, progress); err != nil {
		t.Fatalf("progress on a running operation: %v", err)
	}
	if err := repo.UpdateState(ctx, live, operationh.StateSucceeded, []byte(`{}`), 0, "", ""); err != nil {
		t.Fatalf("result on a running operation: %v", err)
	}
	if got := stateOf(live); got != "SUCCEEDED" {
		t.Errorf("a running operation's result left it %s", got)
	}
	op, err := repo.Get(ctx, live, tenant)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(op.Metadata) != args {
		t.Errorf("metadata = %q after progress and result, want the arguments %s", op.Metadata, args)
	}
	if string(op.Progress) != `{"total": 2, "processed": 1}` {
		t.Errorf("progress = %q, want the last report", op.Progress)
	}
}

// The heartbeat is how a running executor learns it was cancelled: Touch
// reports the operation finished once it is no longer RUNNING.
func TestTouchReportsAnOperationNoLongerRunning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	id := uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO operations (id, tenant_id, type, state) VALUES ($1, $2, 'BatchUpdateTags', 'RUNNING')`,
		id, tenant)
	if err := repo.Touch(ctx, id); err != nil {
		t.Fatalf("touch a running operation: %v", err)
	}
	if err := repo.Cancel(ctx, id, tenant); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := repo.Touch(ctx, id); !errors.Is(err, operationh.ErrOperationFinished) {
		t.Errorf("touch after a cancel returned %v, want ErrOperationFinished", err)
	}
}

func ptr(s string) *string { return &s }
