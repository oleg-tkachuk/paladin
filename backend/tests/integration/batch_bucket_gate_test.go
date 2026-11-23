//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker/operations"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// A batch is admitted by the bucket gate at submit and runs later, on the
// worker. These change the backend between the two — as the app pool sees it,
// under row-level security — and run the executors: a disabled backend fails
// every batch with the gate's refusal and leaves its objects as they were; a
// draining one still takes the metadata batches and refuses a permanent
// delete, a write of bytes.

// The backend and bucket the gated collection is bound to.
const (
	gatedBatchBackend = "batch-gate-be"
	gatedBatchBucket  = "batch-gate-bucket"
	gatedBatchColl    = "docs"
)

// setBackendSQL sets the gated backend's enabled and read_only flags.
const setBackendSQL = `UPDATE storage_backends SET enabled = $2, read_only = $3 WHERE name = $1`

// executeAsTenant runs exec on args as the runner would, scoped to tenantID.
func executeAsTenant(t *testing.T, exec operations.Executor, tenantID uuid.UUID, args any) error {
	t.Helper()
	md, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	_, err = exec.Execute(asTenant(tenantID), operationh.Operation{
		OperationID: uuid.New(), TenantID: tenantID, Metadata: md,
	})
	return err
}

func TestBatchExecutorsAskTheBucketGateWhenTheyRun(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	tenant := mustCreateTenant(t, h.PoolMigrate, "batch-gate")
	seedBackend(t, h.PoolMigrate, gatedBatchBackend)
	mustSeedBucketAndKey(t, h.PoolMigrate, tenant, gatedBatchBackend, gatedBatchBucket, gatedBatchColl)
	obj := mustInsertAvailableObject(t, h.PoolMigrate, tenant, gatedBatchColl, "gated")

	objects := adapters.NewObjectRepo(sqlc.New(h.PoolApp), h.PoolApp)
	sm := statemachine.New(h.PoolApp)
	tags := &operations.BatchUpdateTagsExecutor{Objects: objects}
	del := &operations.BatchDeleteExecutor{Objects: objects, Transitions: sm, Permanent: refusingPermanentDeleter{t}, Erasures: objects}
	restore := &operations.BatchRestoreExecutor{Objects: objects, Transitions: sm}
	ids := []uuid.UUID{obj}

	// Disabled between submit and run: every batch fails with the refusal.
	if _, err := h.PoolMigrate.Exec(ctx, setBackendSQL, gatedBatchBackend, false, false); err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func() error{
		"tags": func() error {
			return executeAsTenant(t, tags, tenant, batchh.BatchUpdateTagsArgs{TenantID: tenant, Collection: gatedBatchColl, ObjectIDs: ids, Tags: map[string]string{"k": "v"}})
		},
		"soft delete": func() error {
			return executeAsTenant(t, del, tenant, batchh.BatchDeleteArgs{TenantID: tenant, Collection: gatedBatchColl, ObjectIDs: ids})
		},
		"restore": func() error {
			return executeAsTenant(t, restore, tenant, batchh.BatchRestoreObjectsArgs{TenantID: tenant, Collection: gatedBatchColl, ObjectIDs: ids})
		},
	} {
		if err := run(); !errors.Is(err, bucketgate.ErrBackendDisabled) {
			t.Errorf("%s on a disabled backend = %v, want %v", name, err, bucketgate.ErrBackendDisabled)
		}
	}
	if state := mustObjectState(t, h.PoolMigrate, obj); state != "AVAILABLE" {
		t.Errorf("object = %s after refused batches, want AVAILABLE", state)
	}

	// Draining: the metadata batches run, a permanent delete is refused.
	if _, err := h.PoolMigrate.Exec(ctx, setBackendSQL, gatedBatchBackend, true, true); err != nil {
		t.Fatal(err)
	}
	if err := executeAsTenant(t, del, tenant, batchh.BatchDeleteArgs{TenantID: tenant, Collection: gatedBatchColl, ObjectIDs: ids}); err != nil {
		t.Fatalf("soft delete on a draining backend = %v, want it run", err)
	}
	if state := mustObjectState(t, h.PoolMigrate, obj); state != "DELETED" {
		t.Errorf("object = %s after a soft delete on a draining backend, want DELETED", state)
	}
	err := executeAsTenant(t, del, tenant, batchh.BatchDeleteArgs{TenantID: tenant, Collection: gatedBatchColl, ObjectIDs: ids, Permanent: true})
	if !errors.Is(err, bucketgate.ErrBackendReadOnly) {
		t.Errorf("permanent delete on a draining backend = %v, want %v", err, bucketgate.ErrBackendReadOnly)
	}
	if err := executeAsTenant(t, restore, tenant, batchh.BatchRestoreObjectsArgs{TenantID: tenant, Collection: gatedBatchColl, ObjectIDs: ids}); err != nil {
		t.Fatalf("restore on a draining backend = %v, want it run", err)
	}
	if state := mustObjectState(t, h.PoolMigrate, obj); state != "AVAILABLE" {
		t.Errorf("object = %s after a restore on a draining backend, want AVAILABLE", state)
	}
}

// refusingPermanentDeleter fails the test if a permanent delete reaches it:
// the gate must refuse the batch before its first item.
type refusingPermanentDeleter struct{ t *testing.T }

func (d refusingPermanentDeleter) PermanentDelete(context.Context, uuid.UUID, objecth.Object, int64, bool) error {
	d.t.Error("a permanent delete reached the deleter on a backend that refuses writes of bytes")
	return nil
}
