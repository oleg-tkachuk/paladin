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

	if err := repo.SetCopying(ctx, tenant, worker.MigStateProvisioning, 3); err != nil {
		t.Fatalf("provisioning -> copying: %v", err)
	}
	if err := repo.AdvanceCopy(ctx, tenant, worker.MigStateCopying, 3, "c", "d"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	moved("a smaller copy count", repo.AdvanceCopy(ctx, tenant, worker.MigStateCopying, 1, "c", "a"))
	if err := repo.SetState(ctx, tenant, worker.MigStateCopying, worker.MigStateRebinding); err != nil {
		t.Fatalf("copying -> rebinding: %v", err)
	}
	if err := repo.SetState(ctx, tenant, worker.MigStateRebinding, worker.MigStateVerifying); err != nil {
		t.Fatalf("rebinding -> verifying: %v", err)
	}

	// A worker that still believes the migration is copying.
	moved("AdvanceCopy from copying", repo.AdvanceCopy(ctx, tenant, worker.MigStateCopying, 3, "c", "d"))
	moved("SetState from copying", repo.SetState(ctx, tenant, worker.MigStateCopying, worker.MigStateRebinding))
	moved("Fail from copying", repo.Fail(ctx, tenant, worker.MigStateCopying, "stale"))
	moved("SetCopying from provisioning", repo.SetCopying(ctx, tenant, worker.MigStateProvisioning, 0))
	if state, copied := read(); state != worker.MigStateVerifying || copied != 3 {
		t.Fatalf("stale writes left the migration at %s with %d copied, want %s with 3",
			state, copied, worker.MigStateVerifying)
	}

	if err := repo.Complete(ctx, tenant, worker.MigStateVerifying); err != nil {
		t.Fatalf("verifying -> completed: %v", err)
	}
	moved("Fail after completion", repo.Fail(ctx, tenant, worker.MigStateVerifying, "late verify"))
	if err := repo.MarkCleaned(ctx, tenant, worker.MigStateCompleted); err != nil {
		t.Fatalf("completed -> cleaned: %v", err)
	}
	if state, _ := read(); state != worker.MigStateCleaned {
		t.Errorf("state = %s, want %s", state, worker.MigStateCleaned)
	}
}
