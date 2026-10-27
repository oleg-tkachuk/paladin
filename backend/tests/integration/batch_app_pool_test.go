//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker/operations"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// The batch executors run as paladin_app, under row-level security, with the
// context scoped to the operation's tenant — as the runner scopes it. These
// pin that every write they make is one RLS admits for that tenant (a policy
// refusing a write here would fail every batch in production), and that a
// row of another tenant is out of their reach whatever ids the batch names.

// asTenant scopes ctx the way the operations runner scopes an executor's.
func asTenant(tenantID uuid.UUID) context.Context {
	return auth.WithActingTenant(context.Background(), tenantID)
}

func runAsTenant(t *testing.T, exec operations.Executor, tenantID uuid.UUID, args any) batchCounts {
	t.Helper()
	md, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	body, err := exec.Execute(asTenant(tenantID), operationh.Operation{
		OperationID: uuid.New(), TenantID: tenantID, Metadata: md,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return decodeCounts(t, body)
}

func TestBatchExecutors_OnTheAppPool(t *testing.T) {
	h := pgharness.Setup(t)
	tenantA := mustCreateTenant(t, h.PoolMigrate, "batch-app-a")
	tenantB := mustCreateTenant(t, h.PoolMigrate, "batch-app-b")
	mustCreateCollection(t, h.PoolMigrate, tenantA, "docs")
	mustCreateCollection(t, h.PoolMigrate, tenantB, "docs")
	mine := mustInsertAvailableObject(t, h.PoolMigrate, tenantA, "docs", "mine")
	theirs := mustInsertAvailableObject(t, h.PoolMigrate, tenantB, "docs", "theirs")

	objects := adapters.NewObjectRepo(sqlc.New(h.PoolApp), h.PoolApp)
	sm := statemachine.New(h.PoolApp)

	del := &operations.BatchDeleteExecutor{Objects: objects, Transitions: sm}
	got := runAsTenant(t, del, tenantA, batchh.BatchDeleteArgs{
		TenantID: tenantA, Collection: "docs", ObjectIDs: []uuid.UUID{mine, theirs},
	})
	wantCounts(t, "soft delete", got, batchCounts{Total: 2, Succeeded: 1, Failed: 1})
	if state := mustObjectState(t, h.PoolMigrate, mine); state != "DELETED" {
		t.Errorf("own object = %s, want DELETED", state)
	}
	if state := mustObjectState(t, h.PoolMigrate, theirs); state != "AVAILABLE" {
		t.Errorf("another tenant's object = %s, want AVAILABLE — out of the batch's reach", state)
	}

	restore := &operations.BatchRestoreExecutor{Objects: objects, Transitions: sm}
	got = runAsTenant(t, restore, tenantA, batchh.BatchRestoreObjectsArgs{
		TenantID: tenantA, Collection: "docs", ObjectIDs: []uuid.UUID{mine},
	})
	wantCounts(t, "restore", got, batchCounts{Total: 1, Succeeded: 1})

	// The transitions an executor makes by object id alone are held to the
	// tenant by RLS too, not only by the lookup that produced the ids.
	if err := sm.SoftDelete(asTenant(tenantA), theirs, 0); err == nil {
		t.Error("a transition scoped to tenant A soft-deleted tenant B's object")
	}

	// A hard delete writes its tombstone under the same policy; WITH CHECK
	// refuses a tombstone for another tenant, so the write has to be the
	// operation tenant's own.
	ctxA := asTenant(tenantA)
	if err := objects.RunInTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		return objects.HardDeleteTx(ctx, tx, tenantA, mine, 0)
	}); err != nil {
		t.Fatalf("hard delete as tenant A: %v", err)
	}
	erased, err := objects.ErasedIDs(ctxA, tenantA, "docs", []uuid.UUID{mine})
	if err != nil || !erased[mine] {
		t.Errorf("tombstone as tenant A = %v, %v; want it written and readable", erased, err)
	}
	if erased, err := objects.ErasedIDs(asTenant(tenantB), tenantA, "docs", []uuid.UUID{mine}); err != nil || len(erased) != 0 {
		t.Errorf("tenant A's tombstone read as tenant B = %v, %v; want none", erased, err)
	}

	// Liveness of a copy's creator reads operations under the same scope.
	ops := adapters.NewOperationRepo(sqlc.New(h.PoolApp), h.PoolApp)
	running := uuid.New()
	if _, err := h.PoolMigrate.Exec(context.Background(),
		`INSERT INTO operations (id, tenant_id, type, state) VALUES ($1, $2, 'BatchCopy', 'RUNNING')`,
		running, tenantB); err != nil {
		t.Fatalf("insert operation: %v", err)
	}
	if live, err := ops.IsLive(asTenant(tenantB), running); err != nil || !live {
		t.Errorf("own running operation = %v, %v; want live", live, err)
	}
	if live, err := ops.IsLive(asTenant(tenantA), running); err != nil || live {
		t.Errorf("another tenant's operation read as live = %v, %v; want not visible", live, err)
	}
}
