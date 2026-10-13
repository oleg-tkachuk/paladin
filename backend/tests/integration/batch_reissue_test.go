//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker/operations"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// A batch whose worker died is failed by the stale-operation reclaimer, and
// the caller reissues it. These run each executor twice over the same batch
// against real SQL — the unique key on (collection, path), the state guards in
// the transition UPDATEs — and require the second run to report the first
// run's work as done, not as failures.

// batchCounts is the part of every batch response these tests compare.
type batchCounts struct {
	Total          int `json:"total"`
	Succeeded      int `json:"succeeded"`
	AlreadyApplied int `json:"already_applied"`
	Failed         int `json:"failed"`
}

func decodeCounts(t *testing.T, body []byte) batchCounts {
	t.Helper()
	var c batchCounts
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return c
}

func wantCounts(t *testing.T, run string, got, want batchCounts) {
	t.Helper()
	if got != want {
		t.Errorf("%s run = %+v, want %+v", run, got, want)
	}
}

func TestBatchExecutor_ReissuedSoftDelete(t *testing.T) {
	h := pgharness.Setup(t)
	tenantID := mustCreateTenant(t, h.PoolMigrate, "batch-redel")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "docs")
	ids := []uuid.UUID{
		mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "a"),
		mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "b"),
	}
	exec := &operations.BatchDeleteExecutor{
		Objects:     adapters.NewObjectRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate),
		Transitions: statemachine.New(h.PoolMigrate),
	}
	args := batchh.BatchDeleteArgs{TenantID: tenantID, Collection: "docs", ObjectIDs: ids}

	wantCounts(t, "first", decodeCounts(t, runExecutor(t, exec, tenantID, args)), batchCounts{Total: 2, Succeeded: 2})
	wantCounts(t, "second", decodeCounts(t, runExecutor(t, exec, tenantID, args)), batchCounts{Total: 2, Succeeded: 2, AlreadyApplied: 2})
}

func TestBatchExecutor_ReissuedRestore(t *testing.T) {
	h := pgharness.Setup(t)
	tenantID := mustCreateTenant(t, h.PoolMigrate, "batch-rerestore")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "docs")
	id := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "a")
	tr := statemachine.New(h.PoolMigrate)
	if err := tr.SoftDelete(context.Background(), id, 0); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	exec := &operations.BatchRestoreExecutor{
		Objects:     adapters.NewObjectRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate),
		Transitions: tr,
	}
	args := batchh.BatchRestoreObjectsArgs{TenantID: tenantID, Collection: "docs", ObjectIDs: []uuid.UUID{id}}

	wantCounts(t, "first", decodeCounts(t, runExecutor(t, exec, tenantID, args)), batchCounts{Total: 1, Succeeded: 1})
	wantCounts(t, "second", decodeCounts(t, runExecutor(t, exec, tenantID, args)), batchCounts{Total: 1, Succeeded: 1, AlreadyApplied: 1})
}

// copyOnlyStorage stands in for S3: the copy is the one call a batch copy
// makes, and what these tests pin is the bookkeeping around it.
type copyOnlyStorage struct {
	objecth.Storage
	copies int
}

func (s *copyOnlyStorage) CopyObject(context.Context, objecth.Location, objecth.Location) error {
	s.copies++
	return nil
}

func TestBatchExecutor_ReissuedCopy(t *testing.T) {
	h := pgharness.Setup(t)
	tenantID := mustCreateTenant(t, h.PoolMigrate, "batch-recopy")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "src")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "dst")
	copied := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "src", "a")
	clashing := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "src", "b")

	storage := &copyOnlyStorage{}
	exec := &operations.BatchCopyExecutor{
		Objects:     adapters.NewObjectRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate),
		Storage:     storage,
		Transitions: statemachine.New(h.PoolMigrate),
		PendingTTL:  time.Minute,
		Limits: uploadpolicy.Limits{
			MaxObjectSize: 1 << 30, MaxMultipartSize: 1 << 40,
			MinPartSize: uploadpolicy.S3MinPartSize, MaxPartSize: uploadpolicy.S3MaxPartSize, MaxParts: uploadpolicy.S3MaxParts,
		},
	}
	args := batchh.BatchCopyArgs{TenantID: tenantID, SrcCollection: "src", DstCollection: "dst", ObjectIDs: []uuid.UUID{copied}}

	wantCounts(t, "first", decodeCounts(t, runExecutor(t, exec, tenantID, args)), batchCounts{Total: 1, Succeeded: 1})
	wantCounts(t, "second", decodeCounts(t, runExecutor(t, exec, tenantID, args)), batchCounts{Total: 1, Succeeded: 1, AlreadyApplied: 1})
	if storage.copies != 1 {
		t.Errorf("storage copies = %d, want 1: the reissue must not copy again", storage.copies)
	}

	// A destination key held by something that is not this source's copy is
	// still a failure, named as such.
	mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "dst", "b")
	if _, err := h.PoolMigrate.Exec(context.Background(),
		`UPDATE objects SET etag = 'other' WHERE path = 'b' AND collection_id = (SELECT id FROM collections WHERE tenant_id = $1 AND name = 'dst')`,
		tenantID); err != nil {
		t.Fatalf("diverge destination: %v", err)
	}
	args.ObjectIDs = []uuid.UUID{clashing}
	var resp operations.BatchCopyResponse
	if err := json.Unmarshal(runExecutor(t, exec, tenantID, args), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Failed != 1 || resp.AlreadyApplied != 0 {
		t.Fatalf("clashing copy = %+v, want one failure", resp)
	}
	if r := resp.Failures[0].Reason; r != `destination key "b" already holds a different object` {
		t.Errorf("reason = %q", r)
	}
}
