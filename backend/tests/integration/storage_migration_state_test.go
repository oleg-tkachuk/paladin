//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// migrationTestBackend hosts the source and target buckets.
const migrationTestBackend = "be-migration-state"

// cleanupRetentionSeconds is the retention the test migration is created
// with; the transitions under test do not read it.
const cleanupRetentionSeconds = 3600

// The migration worker's writes used to match the tenant alone. These pin the
// SQL that now refuses a transition from a state the migration has left, and
// a copy count that goes backwards.
func TestStorageMigrationTransitionsApplyOnlyFromTheStateRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := pgharness.Setup(t)
	q := sqlc.New(h.PoolMigrate)
	repo := adapters.NewStorageMigrationRepo(q, h.PoolMigrate)
	seedBackend(t, h.PoolMigrate, migrationTestBackend)
	for _, b := range []string{"mig-shared", "mig-dedicated"} {
		if _, err := h.PoolMigrate.Exec(ctx, `
			INSERT INTO buckets (backend_id, name)
			SELECT sb.id, $2 FROM storage_backends sb WHERE sb.name = $1`, migrationTestBackend, b); err != nil {
			t.Fatalf("seed bucket %s: %v", b, err)
		}
	}
	tenant := mustCreateTenant(t, h.PoolMigrate, "migration-state")
	if _, err := q.CreateStorageMigration(ctx, pgtype.UUID{Bytes: tenant, Valid: true},
		migrationTestBackend, "mig-shared", migrationTestBackend, "mig-dedicated", cleanupRetentionSeconds); err != nil {
		t.Fatalf("create migration: %v", err)
	}
	read := func() (string, int64) {
		t.Helper()
		var state string
		var copied int64
		if err := h.PoolMigrate.QueryRow(ctx,
			`SELECT state, objects_copied FROM tenant_storage_migrations WHERE tenant_id = $1`, tenant).
			Scan(&state, &copied); err != nil {
			t.Fatalf("read migration: %v", err)
		}
		return state, copied
	}
	moved := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, worker.ErrMigrationMoved) {
			t.Errorf("%s returned %v, want ErrMigrationMoved", what, err)
		}
	}

	if err := repo.SetCopying(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateProvisioning}, 3); err != nil {
		t.Fatalf("provisioning -> copying: %v", err)
	}
	if err := repo.AdvanceCopy(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}, 3, "c", "d"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	moved("a smaller copy count", repo.AdvanceCopy(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}, 1, "c", "a"))
	if err := repo.SetState(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}, worker.MigStateRebinding); err != nil {
		t.Fatalf("copying -> rebinding: %v", err)
	}
	if err := repo.SetState(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateRebinding}, worker.MigStateVerifying); err != nil {
		t.Fatalf("rebinding -> verifying: %v", err)
	}

	// A worker that still believes the migration is copying.
	moved("AdvanceCopy from copying", repo.AdvanceCopy(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}, 3, "c", "d"))
	moved("SetState from copying", repo.SetState(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}, worker.MigStateRebinding))
	moved("Fail from copying", repo.Fail(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}, "stale"))
	moved("SetCopying from provisioning", repo.SetCopying(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateProvisioning}, 0))
	if state, copied := read(); state != worker.MigStateVerifying || copied != 3 {
		t.Fatalf("stale writes left the migration at %s with %d copied, want %s with 3",
			state, copied, worker.MigStateVerifying)
	}

	if err := repo.Complete(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateVerifying}); err != nil {
		t.Fatalf("verifying -> completed: %v", err)
	}
	moved("Fail after completion", repo.Fail(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateVerifying}, "late verify"))
	if err := repo.MarkCleaned(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCompleted}); err != nil {
		t.Fatalf("completed -> cleaned: %v", err)
	}
	if state, _ := read(); state != worker.MigStateCleaned {
		t.Errorf("state = %s, want %s", state, worker.MigStateCleaned)
	}
}

// The state repeats across attempts: a retry walks the same states again.
// A worker that read the first attempt and resumed after it failed and was
// retried must change nothing — not the retry's progress, and above all not
// the tenant's binding, which a stale rebind would point at a bucket the
// retry has not filled.
func TestStorageMigrationTransitionsApplyOnlyToTheAttemptRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := pgharness.Setup(t)
	q := sqlc.New(h.PoolMigrate)
	repo := adapters.NewStorageMigrationRepo(q, h.PoolMigrate)
	const backend = "be-migration-attempt"
	seedBackend(t, h.PoolMigrate, backend)
	for _, b := range []string{"att-shared", "att-dedicated"} {
		if _, err := h.PoolMigrate.Exec(ctx, `
			INSERT INTO buckets (backend_id, name)
			SELECT sb.id, $2 FROM storage_backends sb WHERE sb.name = $1`, backend, b); err != nil {
			t.Fatalf("seed bucket %s: %v", b, err)
		}
	}
	tenant := mustCreateTenant(t, h.PoolMigrate, "migration-attempt")
	if _, err := q.CreateStorageMigration(ctx, pgtype.UUID{Bytes: tenant, Valid: true},
		backend, "att-shared", backend, "att-dedicated", cleanupRetentionSeconds); err != nil {
		t.Fatalf("create migration: %v", err)
	}

	// The first attempt reaches rebinding with a worker holding that read,
	// then fails; the retry starts over and is back in each state in turn.
	first := worker.MigrationRead{TenantID: tenant, State: worker.MigStateProvisioning}
	if err := repo.SetCopying(ctx, first, 2); err != nil {
		t.Fatalf("provisioning -> copying: %v", err)
	}
	first.State = worker.MigStateCopying
	if err := repo.SetState(ctx, first, worker.MigStateRebinding); err != nil {
		t.Fatalf("copying -> rebinding: %v", err)
	}
	staleRebinding := worker.MigrationRead{TenantID: tenant, State: worker.MigStateRebinding}
	staleCopying := worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}
	if err := repo.Fail(ctx, staleRebinding, "first attempt"); err != nil {
		t.Fatalf("fail the first attempt: %v", err)
	}
	retry := func(state string) {
		t.Helper()
		if _, err := h.PoolMigrate.Exec(ctx, `
			UPDATE tenant_storage_migrations
			   SET state = $2, objects_copied = 0, cursor_collection = '', cursor_path = '', error = ''
			 WHERE tenant_id = $1`, tenant, state); err != nil {
			t.Fatalf("retry into %s: %v", state, err)
		}
	}
	moved := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, worker.ErrMigrationMoved) {
			t.Errorf("%s returned %v, want ErrMigrationMoved", what, err)
		}
	}

	retry(worker.MigStateCopying)
	moved("stale AdvanceCopy", repo.AdvanceCopy(ctx, staleCopying, 2, "c", "z"))
	moved("stale SetState", repo.SetState(ctx, staleCopying, worker.MigStateRebinding))
	moved("stale Fail", repo.Fail(ctx, staleCopying, "stale"))
	var state string
	var copied int64
	var attempts int32
	if err := h.PoolMigrate.QueryRow(ctx,
		`SELECT state, objects_copied, attempts FROM tenant_storage_migrations WHERE tenant_id = $1`, tenant).
		Scan(&state, &copied, &attempts); err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if state != worker.MigStateCopying || copied != 0 || attempts != 1 {
		t.Fatalf("stale writes left the retry at %s, %d copied, attempt %d; want %s, 0, 1",
			state, copied, attempts, worker.MigStateCopying)
	}

	retry(worker.MigStateRebinding)
	moved("stale RebindTenant", repo.RebindTenant(ctx, staleRebinding, backend, "att-dedicated"))
	var layout string
	if err := h.PoolMigrate.QueryRow(ctx, `SELECT storage_layout FROM tenants WHERE id = $1`, tenant).
		Scan(&layout); err != nil {
		t.Fatalf("read layout: %v", err)
	}
	if layout != "shared" {
		t.Errorf("storage_layout = %s after a stale rebind, want shared", layout)
	}

	// The retry's own worker, reading attempt 1, still moves it on.
	current := worker.MigrationRead{TenantID: tenant, State: worker.MigStateRebinding, Attempts: 1}
	if err := repo.SetState(ctx, current, worker.MigStateVerifying); err != nil {
		t.Errorf("the current attempt's transition: %v", err)
	}
}
