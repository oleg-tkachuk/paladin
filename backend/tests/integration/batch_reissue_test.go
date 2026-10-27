//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
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
	objects := adapters.NewObjectRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	exec := &operations.BatchCopyExecutor{
		Objects:     objects,
		Pending:     objects,
		Operations:  adapters.NewOperationRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate),
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

// permanentNoop stands in for the S3-backed PermanentDelete: these tests
// reach it only for objects still present, and what they pin is how the
// executor reads the tombstones a real erasure left.
type permanentNoop struct{ calls int }

func (p *permanentNoop) PermanentDelete(context.Context, uuid.UUID, objecth.Object, int64, bool) error {
	p.calls++
	return nil
}

func TestBatchExecutor_ReissuedPermanentDelete(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	tenantID := mustCreateTenant(t, h.PoolMigrate, "batch-reerase")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "docs")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "other")
	erased := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "docs", "a")
	elsewhere := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "other", "b")
	ghost := uuid.New()

	repo := adapters.NewObjectRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	for _, id := range []uuid.UUID{erased, elsewhere} {
		if err := repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return repo.HardDeleteTx(ctx, tx, tenantID, id, 0)
		}); err != nil {
			t.Fatalf("hard delete %s: %v", id, err)
		}
	}

	// The tombstone is scoped to the collection the object was in.
	got, err := repo.ErasedIDs(ctx, tenantID, "docs", []uuid.UUID{erased, elsewhere, ghost})
	if err != nil {
		t.Fatalf("erased ids: %v", err)
	}
	if !got[erased] || got[elsewhere] || got[ghost] || len(got) != 1 {
		t.Errorf("erased in docs = %v, want only %s", got, erased)
	}

	perm := &permanentNoop{}
	exec := &operations.BatchDeleteExecutor{
		Objects: repo, Transitions: statemachine.New(h.PoolMigrate), Permanent: perm, Erasures: repo,
	}
	args := batchh.BatchDeleteArgs{
		TenantID: tenantID, Collection: "docs", ObjectIDs: []uuid.UUID{erased, elsewhere, ghost}, Permanent: true,
	}
	wantCounts(t, "reissue", decodeCounts(t, runExecutor(t, exec, tenantID, args)),
		batchCounts{Total: 3, Succeeded: 1, AlreadyApplied: 1, Failed: 2})
	if perm.calls != 0 {
		t.Errorf("hard deletes = %d, want 0: nothing in the batch still exists", perm.calls)
	}

	// The tombstones go with the operations reaper's cutoff.
	if n, err := repo.PurgeErasedBefore(ctx, time.Now().Add(-time.Hour)); err != nil || n != 0 {
		t.Errorf("purge of an hour-old cutoff = %d, %v; want 0 — the tombstones are newer", n, err)
	}
	if n, err := repo.PurgeErasedBefore(ctx, time.Now().Add(time.Minute)); err != nil || n != 2 {
		t.Errorf("purge past the tombstones = %d, %v; want 2", n, err)
	}
	if got, _ := repo.ErasedIDs(ctx, tenantID, "docs", []uuid.UUID{erased}); len(got) != 0 {
		t.Errorf("tombstone survived its purge: %v", got)
	}
}

// A copy cut off before its promote leaves its destination PENDING, stamped
// with the operation that created it. A reissue adopts the row once that
// operation can no longer write it, and leaves it alone while it still can.
func TestBatchExecutor_ReissuedCopyAdoptsADeadRunsPendingRow(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	tenantID := mustCreateTenant(t, h.PoolMigrate, "batch-adopt")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "src")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "dst")
	fromDead := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "src", "dead")
	fromLive := mustInsertAvailableObject(t, h.PoolMigrate, tenantID, "src", "live")

	operation := func(state string) uuid.UUID {
		id := uuid.New()
		if _, err := h.PoolMigrate.Exec(ctx,
			`INSERT INTO operations (id, tenant_id, type, state) VALUES ($1, $2, 'BatchCopy', $3::operation_state)`,
			id, tenantID, state); err != nil {
			t.Fatalf("insert operation: %v", err)
		}
		return id
	}
	deadRun, liveRun, reissue := operation("FAILED"), operation("RUNNING"), operation("RUNNING")
	pendingCopy := func(key string, creator uuid.UUID) uuid.UUID {
		id := uuid.New()
		if _, err := h.PoolMigrate.Exec(ctx, `
			INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type,
			                     checksum_algorithm, size_bytes, presign_expires_at, created_by_operation)
			SELECT $1, $2, c.id, $3, 'PENDING', 'application/octet-stream', 1, 100,
			       now() + interval '1 minute', $4
			  FROM collections c WHERE c.tenant_id = $2 AND c.name = 'dst'`,
			id, tenantID, key, creator); err != nil {
			t.Fatalf("insert pending copy: %v", err)
		}
		return id
	}
	deadRow, liveRow := pendingCopy("dead", deadRun), pendingCopy("live", liveRun)

	objects := adapters.NewObjectRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	storage := &copyOnlyStorage{}
	exec := &operations.BatchCopyExecutor{
		Objects: objects, Pending: objects, Storage: storage,
		Operations:  adapters.NewOperationRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate),
		Transitions: statemachine.New(h.PoolMigrate),
		PendingTTL:  time.Minute,
		Limits: uploadpolicy.Limits{
			MaxObjectSize: 1 << 30, MaxMultipartSize: 1 << 40,
			MinPartSize: uploadpolicy.S3MinPartSize, MaxPartSize: uploadpolicy.S3MaxPartSize, MaxParts: uploadpolicy.S3MaxParts,
		},
	}
	md, err := json.Marshal(batchh.BatchCopyArgs{
		TenantID: tenantID, SrcCollection: "src", DstCollection: "dst", ObjectIDs: []uuid.UUID{fromDead, fromLive},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := exec.Execute(ctx, operationh.Operation{OperationID: reissue, TenantID: tenantID, Metadata: md})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var resp operations.BatchCopyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Succeeded != 1 || resp.Failed != 1 || storage.copies != 1 {
		t.Fatalf("resp = %+v, copies = %d; want the dead run's row finished and the live one's left", resp, storage.copies)
	}

	row := func(id uuid.UUID) (state string, creator uuid.UUID) {
		if err := h.PoolMigrate.QueryRow(ctx,
			`SELECT state::text, created_by_operation FROM objects WHERE id = $1`, id).Scan(&state, &creator); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		return state, creator
	}
	if state, creator := row(deadRow); state != "AVAILABLE" || creator != reissue {
		t.Errorf("dead run's row = %s by %s, want AVAILABLE by the reissue %s", state, creator, reissue)
	}
	if state, creator := row(liveRow); state != "PENDING" || creator != liveRun {
		t.Errorf("live run's row = %s by %s, want PENDING by %s, untouched", state, creator, liveRun)
	}

	// The handover is a compare-and-set on the creator: the loser of a race
	// matches nothing.
	if ok, err := objects.AdoptPending(ctx, tenantID, liveRow, deadRun, reissue, time.Now()); err != nil || ok {
		t.Errorf("adopt with a stale creator = %v, %v; want false", ok, err)
	}
}
