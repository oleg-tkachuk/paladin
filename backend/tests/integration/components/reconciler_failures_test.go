//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// When a transition the reconciler attempts fails, the tick still succeeds and
// the object stays PENDING for the next one, so a log line is all an operator
// gets. These tests make the database fail mid-tick — the probe closes the
// test's own pool — and pin that each failure says so.

// closingProbe answers HEAD with found and size, closing pool on HEAD or, when
// closeOnDelete is set, on the delete of mismatched bytes.
type closingProbe struct {
	pool          *pgxpool.Pool
	found         bool
	size          int64
	closeOnDelete bool
}

func (p closingProbe) HeadByObjectID(context.Context, uuid.UUID) (string, int64, string, string, bool, error) {
	if !p.closeOnDelete {
		p.pool.Close()
	}
	return "etag", p.size, "", "", p.found, nil
}

func (p closingProbe) DeleteByObjectID(context.Context, uuid.UUID) error {
	if p.closeOnDelete {
		p.pool.Close()
	}
	return nil
}

// seedOverdue inserts a PENDING object registered at size bytes whose upload
// window lapsed long ago.
func seedOverdue(t *testing.T, ctx context.Context, pool *pgxpool.Pool, size int64) {
	t.Helper()
	f := seedFixture(t, ctx, pool)
	mustExec(t, ctx, pool, `
		INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type,
		                     size_bytes, checksum_algorithm, presign_expires_at)
		SELECT $1, $2, c.id, 'k-overdue', 'PENDING', 'application/octet-stream', $4, 0,
		       now() - interval '48 hours'
		  FROM collections c WHERE c.tenant_id = $2 AND c.name = $3`,
		uuid.Must(uuid.NewV7()), f.tenantID, f.collection, size)
}

// reconcileLogged runs the reconciler for a few ticks and returns its log.
func reconcileLogged(t *testing.T, pool *pgxpool.Pool, probe worker.StorageProbe) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zap.WarnLevel)
	r := worker.NewReconcilerV2(statemachine.New(pool), probe, worker.ReconcilerV2Config{
		PollInterval:    50 * time.Millisecond,
		PendingGraceTTL: time.Second,
	}, zap.New(core))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = r.Run(ctx)
		close(done)
	}()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done
	return logs
}

func requireLogged(t *testing.T, logs *observer.ObservedLogs, msg string) {
	t.Helper()
	if logs.FilterMessage(msg).Len() == 0 {
		t.Errorf("no %q in the log; got %v", msg, logs.All())
	}
}

const registeredSize = 10

// The bytes are absent and the MarkFailed that should follow fails; so does
// the overdue sample taken after the batch.
func TestReconcilerLogsAFailedMarkFailedAndSample(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	seedOverdue(t, ctx, pool, registeredSize)

	logs := reconcileLogged(t, pool, closingProbe{pool: pool})
	requireLogged(t, logs, "failed to mark object as failed")
	requireLogged(t, logs, "failed to sample overdue pending objects")
}

// The bytes are present and match, and the promotion fails.
func TestReconcilerLogsAFailedPromotion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	seedOverdue(t, ctx, pool, registeredSize)

	logs := reconcileLogged(t, pool, closingProbe{pool: pool, found: true, size: registeredSize})
	requireLogged(t, logs, "failed to promote object")
}

// The bytes break the registration and are deleted, then failing the row
// fails: the reconciler must say so, and must not claim the object discarded.
func TestReconcilerLogsAFailedDiscard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	seedOverdue(t, ctx, pool, registeredSize)

	logs := reconcileLogged(t, pool, closingProbe{pool: pool, found: true, size: registeredSize + 1, closeOnDelete: true})
	requireLogged(t, logs, "failed to mark mismatched object as failed")
	if n := logs.FilterMessage("discarded object whose stored bytes broke its registration").Len(); n != 0 {
		t.Errorf("claimed %d discards of an object it failed to mark", n)
	}
}
