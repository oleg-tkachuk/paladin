//go:build integration

// Integration coverage for the batch executors. Each test seeds a
// known set of objects in PENDING/AVAILABLE/DELETED, runs the
// executor against an operations.Operation row built in-memory,
// and asserts the final DB state.
//
// What this catches that the unit tests can't: the executors call
// repo + state-machine methods that touch real SQL (FOREIGN KEYS,
// state-transition SQL guards, OCC version bumps via UPDATE
// triggers). A handler that compiles-and-passes-mocks but trips a
// FK or a CHECK constraint at runtime fails here.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/batch"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/operation"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker/operations"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// TestBatchExecutor_Delete: 3 AVAILABLE rows + 1 nonexistent id ⇒
// 3 succeed, 1 fails ("not found" reason).
func TestBatchExecutor_Delete(t *testing.T) {
	h := pgharness.Setup(t)
	tenantID := mustCreateTenant(t, h.PoolMigrate, "batch-del")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "docs")
	id1 := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "a")
	id2 := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "b")
	id3 := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "c")
	idGhost := uuid.New()

	q := sqlc.New(h.PoolMigrate)
	exec := &operations.BatchDeleteExecutor{
		Objects:     adapters.NewObjectRepo(q, h.PoolMigrate),
		Transitions: statemachine.New(h.PoolMigrate),
	}

	args := batch.BatchDeleteArgs{
		TenantID:   tenantID,
		Collection: "docs",
		ObjectIDs:  []uuid.UUID{id1, id2, id3, idGhost},
	}
	resp := runExecutor(t, exec, tenantID, args)

	var parsed operations.BatchDeleteResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if parsed.Total != 4 || parsed.Succeeded != 3 || parsed.Failed != 1 {
		t.Errorf("counts = total=%d ok=%d fail=%d, want 4/3/1",
			parsed.Total, parsed.Succeeded, parsed.Failed)
	}
	if len(parsed.Failures) != 1 || parsed.Failures[0].ObjectID != idGhost.String() {
		t.Errorf("failure entry = %+v, want one for the ghost id", parsed.Failures)
	}
	for _, id := range []uuid.UUID{id1, id2, id3} {
		if state := mustObjectState(t, h.PoolMigrate, id); state != "DELETED" {
			t.Errorf("object %s state = %s, want DELETED", id, state)
		}
	}
}

// TestBatchExecutor_UpdateTags: 2 rows with different tag maps;
// after batch update both share the new tag map.
func TestBatchExecutor_UpdateTags(t *testing.T) {
	h := pgharness.Setup(t)
	tenantID := mustCreateTenant(t, h.PoolMigrate, "batch-tags")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "docs")
	id1 := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "a")
	id2 := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "b")

	q := sqlc.New(h.PoolMigrate)
	exec := &operations.BatchUpdateTagsExecutor{
		Objects: adapters.NewObjectRepo(q, h.PoolMigrate),
	}

	args := batch.BatchUpdateTagsArgs{
		TenantID:   tenantID,
		Collection: "docs",
		ObjectIDs:  []uuid.UUID{id1, id2},
		Tags:       map[string]string{"env": "prod", "team": "platform"},
	}
	resp := runExecutor(t, exec, tenantID, args)

	var parsed operations.BatchUpdateTagsResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if parsed.Succeeded != 2 || parsed.Failed != 0 {
		t.Errorf("counts = ok=%d fail=%d, want 2/0", parsed.Succeeded, parsed.Failed)
	}
	for _, id := range []uuid.UUID{id1, id2} {
		got := mustObjectTags(t, h.PoolMigrate, id)
		if got["env"] != "prod" || got["team"] != "platform" {
			t.Errorf("tags on %s = %+v, want env=prod team=platform", id, got)
		}
	}
}

// TestBatchExecutor_Restore: soft-deleted rows flip to AVAILABLE.
func TestBatchExecutor_Restore(t *testing.T) {
	h := pgharness.Setup(t)
	tenantID := mustCreateTenant(t, h.PoolMigrate, "batch-restore")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "docs")
	id1 := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "a")
	id2 := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "b")

	// Soft-delete both via the state machine — same code path the
	// data plane uses on DeleteObject.
	tr := statemachine.New(h.PoolMigrate)
	for _, id := range []uuid.UUID{id1, id2} {
		if err := tr.SoftDelete(context.Background(), id, 1); err != nil {
			t.Fatalf("soft-delete %s: %v", id, err)
		}
	}

	q := sqlc.New(h.PoolMigrate)
	exec := &operations.BatchRestoreExecutor{
		Objects:     adapters.NewObjectRepo(q, h.PoolMigrate),
		Transitions: tr,
	}

	args := batch.BatchRestoreObjectsArgs{
		TenantID:   tenantID,
		Collection: "docs",
		ObjectIDs:  []uuid.UUID{id1, id2},
	}
	resp := runExecutor(t, exec, tenantID, args)

	var parsed operations.BatchRestoreResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if parsed.Succeeded != 2 || parsed.Failed != 0 {
		t.Errorf("counts = ok=%d fail=%d, want 2/0", parsed.Succeeded, parsed.Failed)
	}
	for _, id := range []uuid.UUID{id1, id2} {
		if state := mustObjectState(t, h.PoolMigrate, id); state != "AVAILABLE" {
			t.Errorf("object %s state = %s, want AVAILABLE", id, state)
		}
	}
}

// TestBatchExecutor_TenantMismatch_FailsWholeOp: metadata.TenantID
// disagreeing with operation.TenantID is a defence-in-depth signal —
// something tampered between handler and worker. The executor must
// reject the whole batch rather than partially process.
func TestBatchExecutor_TenantMismatch_FailsWholeOp(t *testing.T) {
	h := pgharness.Setup(t)
	tenantA := mustCreateTenant(t, h.PoolMigrate, "tenant-a")
	tenantB := mustCreateTenant(t, h.PoolMigrate, "tenant-b")
	mustCreateCollection(t, h.PoolMigrate, tenantA, "docs")
	id := mustInsertAvailableObject(t, h.PoolMigrate, tenantA, "docs", "a")

	q := sqlc.New(h.PoolMigrate)
	exec := &operations.BatchDeleteExecutor{
		Objects:     adapters.NewObjectRepo(q, h.PoolMigrate),
		Transitions: statemachine.New(h.PoolMigrate),
	}

	// Args claim tenant A, but we wrap in a tenant-B operation.
	args := batch.BatchDeleteArgs{
		TenantID:   tenantA,
		Collection: "docs",
		ObjectIDs:  []uuid.UUID{id},
	}
	md, _ := json.Marshal(args)
	op := operation.Operation{
		OperationID: uuid.New(),
		TenantID:    tenantB,
		Type:        "BatchDelete",
		State:       operation.StateRunning,
		Metadata:    md,
	}
	if _, err := exec.Execute(context.Background(), op); err == nil ||
		!strings.Contains(err.Error(), "tenant_id") {
		t.Errorf("expected tenant_id mismatch error, got %v", err)
	}
	// Object must NOT have been touched.
	if state := mustObjectState(t, h.PoolMigrate, id); state != "AVAILABLE" {
		t.Errorf("object state changed despite mismatch: %s", state)
	}
}

// ─── helpers ────────────────────────────────────────────────────────

func mustInsertAvailableObject(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, collection, key string) uuid.UUID {
	t.Helper()
	objID := uuid.New()
	_, err := pool.Exec(context.Background(), `
        INSERT INTO objects (
            id, tenant_id, collection_id, path, state,
            content_type, checksum_algorithm, size_bytes, etag, committed_at
        )
        SELECT $1, $2, c.id, $4, 'AVAILABLE',
               'application/octet-stream', 1, 100, 'etag', now()
          FROM collections c
         WHERE c.tenant_id = $2 AND c.name = $3
    `, objID, tenantID, collection, key)
	if err != nil {
		t.Fatalf("insert available object: %v", err)
	}
	return objID
}

func mustObjectTags(t *testing.T, pool *pgxpool.Pool, objectID uuid.UUID) map[string]string {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT tags FROM objects WHERE id = $1`, objectID,
	).Scan(&raw); err != nil {
		t.Fatalf("read tags: %v", err)
	}
	out := map[string]string{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode tags %q: %v", raw, err)
	}
	return out
}

// runExecutor wraps an executor.Execute call with the standard
// operation.Operation envelope a real runner would supply.
func runExecutor(t *testing.T, exec operations.Executor, tenantID uuid.UUID, args any) []byte {
	t.Helper()
	md, _ := json.Marshal(args)
	op := operation.Operation{
		OperationID: uuid.New(),
		TenantID:    tenantID,
		Type:        "Batch",
		State:       operation.StateRunning,
		Metadata:    md,
	}
	resp, err := exec.Execute(context.Background(), op)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return resp
}

// silence unused linter when imports drift between test files.
var _ = errors.New
