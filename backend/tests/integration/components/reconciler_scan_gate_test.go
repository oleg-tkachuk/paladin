//go:build integration

package components

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The reconciler HEADs a pending object's bytes and settles its row, so it
// asks the bucket gate as a read: a disabled backend defers its pending
// objects, which stay PENDING and are not HEADed, while a draining or
// provisioning one is reconciled as before. Each test seeds the refused
// fixture's object with the older deadline and scans one row at a time, so a
// refused bucket left at the head of the scan would starve the healthy one.

// reconcilerGateGrace is the reconciler's grace past a presign's expiry; the
// seeded deadlines are well past it.
const reconcilerGateGrace = time.Second

// absentHead answers every HEAD as not found, so a reconciled object is
// discarded, and records the bucket of every HEAD it is asked for.
type absentHead struct {
	mu    sync.Mutex
	heads []string
}

func (h *absentHead) Head(_ context.Context, _, bucket string, _ uuid.UUID, _, _, _ string) (string, int64, string, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.heads = append(h.heads, bucket)
	return "", 0, "", "", fmt.Errorf("head: %w", s3adapter.ErrObjectNotFound)
}

func (h *absentHead) DeleteObject(context.Context, string, string, uuid.UUID, string, string) error {
	return nil
}

func (h *absentHead) headsIn(bucket string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, b := range h.heads {
		if b == bucket {
			n++
		}
	}
	return n
}

// The statements that put a collection's bucket in a gate state, by the
// collection's id.
const (
	disableCollectionBackendSQL = `UPDATE storage_backends SET enabled = FALSE
		WHERE id = (SELECT b.backend_id FROM buckets b JOIN collections c ON c.bucket_id = b.id WHERE c.id = $1)`
	drainCollectionBackendSQL = `UPDATE storage_backends SET read_only = TRUE
		WHERE id = (SELECT b.backend_id FROM buckets b JOIN collections c ON c.bucket_id = b.id WHERE c.id = $1)`
	provisionCollectionBucketSQL = `UPDATE buckets SET provision_state = 'pending'
		WHERE id = (SELECT bucket_id FROM collections WHERE id = $1)`
)

// gateStates are the gate states a read-gated worker meets, and whether a
// read is refused in each.
var gateStates = []struct {
	name    string
	sql     string
	refused bool
}{
	{"disabled", disableCollectionBackendSQL, true},
	{"draining", drainCollectionBackendSQL, false},
	{"provisioning", provisionCollectionBucketSQL, false},
}

func TestReconcilerDefersPendingObjectsOnlyOnADisabledBackend(t *testing.T) {
	t.Parallel()
	for _, tc := range gateStates {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			pool := startPostgres(t)
			gated, healthy := seedFixture(t, ctx, pool), seedFixture(t, ctx, pool)
			gatedObject, healthyObject := seedPendingObject(t, ctx, pool, gated), seedPendingObject(t, ctx, pool, healthy)
			// The gated object's deadline is the older, so it heads the scan.
			mustExec(t, ctx, pool, `UPDATE objects SET presign_expires_at = now() - interval '48 hours' WHERE id = $1`, gatedObject)
			mustExec(t, ctx, pool, `UPDATE objects SET presign_expires_at = now() - interval '24 hours' WHERE id = $1`, healthyObject)
			mustExec(t, ctx, pool, tc.sql, gated.collectionID)

			head := &absentHead{}
			r := worker.NewReconcilerV2(statemachine.New(pool), adapters.NewReconcilerProbe(sqlc.New(pool), head),
				worker.ReconcilerV2Config{PendingGraceTTL: reconcilerGateGrace, BatchSize: gateTestBatch}, zap.NewNop())
			// One tick of one row: the gated object heads the scan, so a
			// refused bucket that was not left out of the next scan would
			// leave the healthy object untouched.
			if err := r.RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}

			gatedBucket := fixtureBucket(t, ctx, pool, gated)
			if tc.refused {
				if !objectGone(t, ctx, pool, healthyObject) {
					t.Errorf("the healthy fixture's object was not reconciled: the refused bucket starved it")
				}
				if objectGone(t, ctx, pool, gatedObject) {
					t.Errorf("the object on a %s backend was discarded, want it left PENDING", tc.name)
				}
				if n := head.headsIn(gatedBucket); n != 0 {
					t.Errorf("%d HEADs on a %s backend, want none", n, tc.name)
				}
				return
			}
			if !objectGone(t, ctx, pool, gatedObject) {
				t.Errorf("the object on a %s bucket was not reconciled; a read is admitted there", tc.name)
			}
			if n := head.headsIn(gatedBucket); n != 1 {
				t.Errorf("%d HEADs on a %s bucket, want 1", n, tc.name)
			}
		})
	}
}
