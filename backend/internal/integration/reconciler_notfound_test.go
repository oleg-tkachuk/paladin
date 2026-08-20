//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// These drive the real ReconcilerV2 loop — real Transitioner, real
// ReconcilerProbe, real SQL — with only the S3 HEAD faked, because the HEAD
// result is the input whose classification was broken.
//
// The bug this covers: reconciler_probe's isNotFoundErr was a `return false`
// stub, so HeadByObjectID returned every HEAD failure as an error, reconcile()
// took its `if err != nil` branch, and MarkFailed was unreachable for exactly
// the objects it was written for. In production that showed up as 29
// pending-expired objects surviving indefinitely and 174 "failed to HEAD
// object" warnings in four minutes.

// headFunc adapts a function to adapters.HeadProber.
type headFunc func(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) (string, int64, string, string, error)

func (f headFunc) Head(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) (string, int64, string, string, error) {
	return f(ctx, backendID, bucket, tenantID, collection, key)
}

// runOneTick starts the reconciler, lets it fire at least once, and stops it.
// Driving Run (rather than reaching for the unexported tick) keeps the test on
// the same path production takes: ScanPendingExpired → reconcile → transition.
func runOneTick(t *testing.T, probe worker.StorageProbe, sm *statemachine.Transitioner) {
	t.Helper()
	r := worker.NewReconcilerV2(sm, probe, worker.ReconcilerV2Config{
		PollInterval: 50 * time.Millisecond,
		// The seeded objects lapsed 48h ago, so any small grace picks them
		// up. Not sub-second, though: ScanPendingExpired passes
		// graceBeyond.String() straight into a Postgres interval cast, and
		// Go renders sub-microsecond durations as "1ns", which Postgres
		// rejects — the scan then errors and tick swallows it as a warning.
		PendingGraceTTL: time.Second,
		BatchSize:       100,
	}, zap.NewNop())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = r.Run(ctx)
		close(done)
	}()
	time.Sleep(300 * time.Millisecond) // several ticks at 50ms
	cancel()
	<-done
}

// TestReconcilerMarksFailedWhenBytesAreAbsent is the regression test: a HEAD
// that says "no such object" must reach MarkFailed, not the error branch.
func TestReconcilerMarksFailedWhenBytesAreAbsent(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	id := uuid.Must(uuid.NewV7())
	mustExec(t, ctx, pool, `
		INSERT INTO objects (id, tenant_id, collection, key, state,
		                     content_type, checksum_algorithm, presign_expires_at)
		VALUES ($1, $2, $3, 'k-gone', 'PENDING', 'application/octet-stream', 0,
		        now() - interval '48 hours')`,
		id, f.tenantID, f.collection)

	// The backend answers exactly as garage/seaweedfs did in production: the
	// object is not there. Built through the real s3adapter wrapper so the
	// test depends on the sentinel contract, not on a hand-made error.
	probe := adapters.NewReconcilerProbe(sqlc.New(pool),
		headFunc(func(context.Context, string, string, uuid.UUID, string, string) (string, int64, string, string, error) {
			return "", 0, "", "", fmt.Errorf("head: %w: %w", s3adapter.ErrObjectNotFound,
				errors.New("operation error S3: HeadObject, https response error StatusCode: 404, NotFound"))
		}))

	runOneTick(t, probe, statemachine.New(pool))

	if got := objectState(t, ctx, pool, id); got != "FAILED" {
		t.Errorf("object state = %s, want FAILED — MarkFailed was not reached", got)
	}
}

// TestReconcilerLeavesPendingWhenBackendIsUnreachable is the safety half, and
// the more important of the two. A backend that cannot answer must leave the
// object PENDING for the next tick: FAILED is terminal, and marking a live
// object FAILED takes it out of service.
func TestReconcilerLeavesPendingWhenBackendIsUnreachable(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	id := uuid.Must(uuid.NewV7())
	mustExec(t, ctx, pool, `
		INSERT INTO objects (id, tenant_id, collection, key, state,
		                     content_type, checksum_algorithm, presign_expires_at)
		VALUES ($1, $2, $3, 'k-unreachable', 'PENDING', 'application/octet-stream', 0,
		        now() - interval '48 hours')`,
		id, f.tenantID, f.collection)

	probe := adapters.NewReconcilerProbe(sqlc.New(pool),
		headFunc(func(context.Context, string, string, uuid.UUID, string, string) (string, int64, string, string, error) {
			// No sentinel: the backend is down, not empty.
			return "", 0, "", "", errors.New("head: dial tcp: connection refused")
		}))

	runOneTick(t, probe, statemachine.New(pool))

	if got := objectState(t, ctx, pool, id); got != "PENDING" {
		t.Errorf("object state = %s, want PENDING — an unreachable backend must "+
			"never produce a terminal transition", got)
	}
}

// TestReconcilerPromotesWhenBytesArePresent pins the other branch, so a future
// change to the classification cannot quietly turn a successful HEAD into a
// FAILED. This is the path that already worked in production (2 of 31 objects
// promoted after the pool fix landed).
func TestReconcilerPromotesWhenBytesArePresent(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	id := uuid.Must(uuid.NewV7())
	mustExec(t, ctx, pool, `
		INSERT INTO objects (id, tenant_id, collection, key, state,
		                     content_type, checksum_algorithm, presign_expires_at)
		VALUES ($1, $2, $3, 'k-present', 'PENDING', 'application/octet-stream', 0,
		        now() - interval '48 hours')`,
		id, f.tenantID, f.collection)

	probe := adapters.NewReconcilerProbe(sqlc.New(pool),
		headFunc(func(context.Context, string, string, uuid.UUID, string, string) (string, int64, string, string, error) {
			return "etag-1", 4096, "", "", nil
		}))

	runOneTick(t, probe, statemachine.New(pool))

	if got := objectState(t, ctx, pool, id); got != "AVAILABLE" {
		t.Errorf("object state = %s, want AVAILABLE", got)
	}
}
