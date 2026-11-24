//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/presignh"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
	"github.com/oleg-tkachuk/paladin/backend/tests/objectrow"
)

// The grace the discard tests scan with; the upload's URL expired twice as
// long ago, and a regenerated URL lives regeneratedURLTTL from now.
const (
	discardPendingGrace = time.Hour
	regeneratedURLTTL   = 15 * time.Minute
)

// expiredUpload seeds a PENDING upload whose URL expired past the grace and
// returns it as the reconciler's scan finds it.
func expiredUpload(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture) statemachine.ExpiredAnchor {
	t.Helper()
	expired := time.Now().Add(-2 * discardPendingGrace)
	id := seedObject(t, ctx, pool, objectrow.Row{
		TenantID: f.tenantID, CollectionID: f.collectionID, Path: "k-" + uuid.New().String()[:8],
		State: objectrow.StatePending, PresignExpiresAt: &expired,
	})
	anchors, err := statemachine.New(pool).ScanExpiredAnchors(ctx,
		statemachine.AnchorGraces{Pending: discardPendingGrace, Settle: discardPendingGrace}, scanBatch, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, a := range anchors {
		if a.ObjectID == id {
			return a
		}
	}
	t.Fatal("the expired upload was not scanned")
	return statemachine.ExpiredAnchor{}
}

// regenerate moves the upload's deadline as RegenerateUploadURL does.
func regenerate(ctx context.Context, q *sqlc.Queries, pool *pgxpool.Pool, f fixture, id uuid.UUID) error {
	return adapters.NewPresignRepo(q, pool).ExtendPendingPresign(ctx, f.tenantID, id, time.Now().Add(regeneratedURLTTL))
}

// A RegenerateUploadURL that commits while the discard waits on its row wins:
// the discard re-reads the row, finds its URL live, and keeps it, so the
// client's new URL has a row to complete.
func TestDiscardWaitsForARegenerateAndKeepsTheRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	p := expiredUpload(t, ctx, pool, f)

	regenerating, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = regenerating.Rollback(ctx) }()
	var regeneratorPID int
	if err := regenerating.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&regeneratorPID); err != nil {
		t.Fatalf("pid: %v", err)
	}
	// The regenerate's UPDATE, held uncommitted: it holds the row.
	if err := regenerate(ctx, sqlc.New(regenerating), pool, f, p.ObjectID); err != nil {
		t.Fatalf("regenerate: %v", err)
	}

	type outcome struct {
		discarded bool
		err       error
	}
	done := make(chan outcome, 1)
	go func() {
		discarded, err := statemachine.New(pool).DiscardExpired(ctx, p.ObjectID, p.BucketID, discardPendingGrace,
			statemachine.FailedPresignExpired)
		done <- outcome{discarded, err}
	}()
	waitBlockedBy(t, ctx, pool, regeneratorPID, func() bool {
		select {
		case o := <-done:
			t.Fatalf("the discard finished while the regenerate held the row: %+v", o)
		default:
		}
		return false
	})
	if err := regenerating.Commit(ctx); err != nil {
		t.Fatalf("commit the regenerate: %v", err)
	}
	if o := <-done; o.err != nil || o.discarded {
		t.Fatalf("DiscardExpired after a regenerate = %+v, want the row kept", o)
	}
	if !objectExists(t, ctx, pool, p.ObjectID) {
		t.Fatal("the regenerated upload's row was discarded")
	}
}

// The other order: a discard that commits first leaves the regenerate no row,
// so RegenerateUploadURL refuses rather than hand out a URL for nothing.
func TestARegenerateAfterTheDiscardFindsNoRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	p := expiredUpload(t, ctx, pool, f)

	discarded, err := statemachine.New(pool).DiscardExpired(ctx, p.ObjectID, p.BucketID, discardPendingGrace,
		statemachine.FailedPresignExpired)
	if err != nil || !discarded {
		t.Fatalf("DiscardExpired = %v, %v; want the expired upload discarded", discarded, err)
	}
	if err := regenerate(ctx, sqlc.New(pool), pool, f, p.ObjectID); !errors.Is(err, presignh.ErrNotPending) {
		t.Fatalf("regenerate after the discard = %v, want ErrNotPending", err)
	}
}

// The reconciler's own path: a regenerate that lands between its scan and its
// discard — here while it HEADs the anchor and finds nothing — keeps the row.
func TestReconcilerKeepsAnUploadRegeneratedSinceItsScan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	expired := time.Now().Add(-2 * discardPendingGrace)
	id := seedObject(t, ctx, pool, objectrow.Row{
		TenantID: f.tenantID, CollectionID: f.collectionID, Path: "regenerated",
		State: objectrow.StatePending, PresignExpiresAt: &expired,
	})

	probe := regeneratingProbe{before: func() {
		if err := regenerate(ctx, sqlc.New(pool), pool, f, id); err != nil {
			t.Errorf("regenerate: %v", err)
		}
	}}
	r := worker.NewReconcilerV2(statemachine.New(pool), probe, worker.ReconcilerV2Config{
		PendingGraceTTL: discardPendingGrace, Events: worker.NewEventRecorder(nil),
	}, zap.NewNop())
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !objectExists(t, ctx, pool, id) {
		t.Fatal("the reconciler discarded an upload regenerated since its scan")
	}
	if s := objectState(t, ctx, pool, id); s != objectrow.StatePending {
		t.Errorf("state = %s, want PENDING", s)
	}
}

// regeneratingProbe finds nothing in the bucket, after running before.
type regeneratingProbe struct{ before func() }

func (p regeneratingProbe) HeadByObjectID(context.Context, uuid.UUID) (string, int64, string, string, statemachine.Placement, bool, error) {
	p.before()
	return "", 0, "", "", statemachine.Placement{}, false, nil
}

func (regeneratingProbe) DeleteByObjectID(context.Context, uuid.UUID) error { return nil }

// waitBlockedBy waits until a session of this database waits on a lock pid
// holds; stop, polled between looks, ends the wait early by failing.
func waitBlockedBy(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pid int, stop func() bool) {
	t.Helper()
	deadline := time.Now().Add(lockWaitDeadline)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			 WHERE datname = current_database() AND $1 = ANY (pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
			t.Fatalf("look for the waiting session: %v", err)
		}
		if waiting || stop() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no session waited on the lock")
		}
		time.Sleep(lockWaitPoll)
	}
}
