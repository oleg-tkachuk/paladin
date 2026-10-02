//go:build integration

package components

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// object.Handler.PermanentDelete is the on-demand hard delete. It used to be
// ~130 lines inline in the DeleteObject RPC; it was extracted so BatchDelete
// could reach it, which means one sequence now has two callers and a bug in
// it breaks both.
//
// That sequence is ordered on purpose, and the ordering is the part worth
// pinning down here rather than in a unit test:
//
//	tx { row removed, purge debt written, paladin.object.deleted enqueued }
//	then storage bytes
//	then tx { debt settled, paladin.object.purged enqueued }
//
// DB before S3, because the reverse can delete bytes and then fail to delete
// the row, leaving a live row pointing at nothing. The debt written INSIDE the
// first transaction, because the row that says where the bytes live is about
// to be deleted and nothing else in the schema records it — without a durable
// handle, a failed byte-delete leaves bytes findable only by listing a bucket.
//
// Both facts are invisible to a unit test with a fake repository: it would
// happily report success against a transaction that never committed.

// recordingStorage answers the object.Storage methods PermanentDelete uses and
// records the delete it was asked for. Everything else would panic, which is
// the point — this path must not reach for presigning or copying.
type recordingStorage struct {
	objecth.Storage
	deleted []string
	err     error
}

func (s *recordingStorage) DeleteObject(_ context.Context, backendID, bucket string, _ uuid.UUID, collection, key string) error {
	s.deleted = append(s.deleted, backendID+"/"+bucket+"/"+collection+"/"+key)
	return s.err
}

func purgeDebtCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, objectID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pending_purges WHERE object_id = $1`, objectID).Scan(&n); err != nil {
		t.Fatalf("count purge debt: %v", err)
	}
	return n
}

func availableObject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture) objecth.Object {
	t.Helper()
	id := seedPendingObject(t, ctx, pool, f)
	mustExec(t, ctx, pool, `UPDATE objects SET state = 'AVAILABLE' WHERE id = $1`, id)
	var key string
	if err := pool.QueryRow(ctx, `SELECT path FROM objects WHERE id = $1`, id).Scan(&key); err != nil {
		t.Fatalf("read key: %v", err)
	}
	return objecth.Object{
		ObjectID: id, TenantID: f.tenantID, Collection: f.collection, Key: key,
	}
}

func permanentDeleteHandler(pool *pgxpool.Pool, st objecth.Storage) *objecth.Handler {
	q := sqlc.New(pool)
	h := objecth.NewHandler(
		adapters.NewObjectRepo(q, pool),
		st,
		nil, // policy: PermanentDelete takes an already-authorized object
		nil, // filter: nothing lists here
		statemachine.New(pool),
		objecth.PresignConfig{},
	)
	h.SetEventProducer(&worker.Dispatcher{
		Store:       worker.NewRepoSubscriptionStore(adapters.NewEventSubscriptionRepoV2(q)),
		Outbox:      worker.PgxOutboxWriter{Pool: pool},
		Logger:      zap.NewNop(),
		MaxAttempts: 3,
	})
	h.SetLogger(zap.NewNop())
	return h
}

func TestPermanentDeleteRemovesRowBytesAndDebtInOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	st := &recordingStorage{}
	h := permanentDeleteHandler(pool, st)
	obj := availableObject(t, ctx, pool, f)

	if err := h.PermanentDelete(ctx, f.tenantID, obj, 0, false); err != nil {
		t.Fatalf("PermanentDelete: %v", err)
	}

	if objectExists(t, ctx, pool, obj.ObjectID) {
		t.Error("row survived a permanent delete")
	}
	if len(st.deleted) != 1 {
		t.Fatalf("storage deletes = %v, want exactly one", st.deleted)
	}
	if n := deliveryCount(t, ctx, pool, f.tenantID, "paladin.object.deleted"); n != 1 {
		t.Errorf("paladin.object.deleted rows = %d, want 1", n)
	}
	if n := deliveryCount(t, ctx, pool, f.tenantID, "paladin.object.purged"); n != 1 {
		t.Errorf("paladin.object.purged rows = %d, want 1 — the bytes went, so subscribers are owed the fact", n)
	}
	// Debt written and then settled. A leftover row would make the drainer
	// re-issue a delete forever against a key that is already gone.
	if n := purgeDebtCount(t, ctx, pool, obj.ObjectID); n != 0 {
		t.Errorf("pending_purges rows = %d, want 0 — the debt was not settled", n)
	}
}

func TestPermanentDeleteKeepsTheDebtWhenStorageFails(t *testing.T) {
	t.Parallel()
	// The delete IS committed — row gone, event enqueued — and the bytes are
	// owed rather than lost. Returning an error here would tell the client to
	// retry a delete that already succeeded, and the retry would get NotFound.
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	st := &recordingStorage{err: errors.New("s3 unreachable")}
	h := permanentDeleteHandler(pool, st)
	obj := availableObject(t, ctx, pool, f)

	if err := h.PermanentDelete(ctx, f.tenantID, obj, 0, false); err != nil {
		t.Fatalf("a failed byte-delete must not fail the RPC: %v", err)
	}
	if objectExists(t, ctx, pool, obj.ObjectID) {
		t.Error("row survived")
	}
	if n := deliveryCount(t, ctx, pool, f.tenantID, "paladin.object.deleted"); n != 1 {
		t.Errorf("paladin.object.deleted rows = %d, want 1", n)
	}
	// The whole reason the debt is written inside the removal transaction:
	// the row that recorded where these bytes live is gone, so this is the
	// only remaining handle on them.
	if n := purgeDebtCount(t, ctx, pool, obj.ObjectID); n != 1 {
		t.Fatalf("pending_purges rows = %d, want 1 — the bytes are now unfindable", n)
	}
	// And no purged event: nothing was reclaimed. The drainer emits it when
	// the retry succeeds.
	if n := deliveryCount(t, ctx, pool, f.tenantID, "paladin.object.purged"); n != 0 {
		t.Errorf("paladin.object.purged rows = %d, want 0 — the bytes are still there", n)
	}
}

func TestPermanentDeleteRefusesAVersionMismatch(t *testing.T) {
	t.Parallel()
	// Optimistic concurrency: a stale resource_version must leave everything
	// alone — no row removal, no debt, no event, and no storage call.
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	st := &recordingStorage{}
	h := permanentDeleteHandler(pool, st)
	obj := availableObject(t, ctx, pool, f)

	if err := h.PermanentDelete(ctx, f.tenantID, obj, 9999, false); err == nil {
		t.Fatal("a stale resource_version was accepted")
	}
	if !objectExists(t, ctx, pool, obj.ObjectID) {
		t.Error("row removed despite the version mismatch")
	}
	if len(st.deleted) != 0 {
		t.Errorf("storage delete issued on a refused delete: %v", st.deleted)
	}
	if n := purgeDebtCount(t, ctx, pool, obj.ObjectID); n != 0 {
		t.Errorf("pending_purges rows = %d, want 0", n)
	}
	if n := deliveryCount(t, ctx, pool, f.tenantID, "paladin.object.deleted"); n != 0 {
		t.Errorf("paladin.object.deleted rows = %d, want 0", n)
	}
}
