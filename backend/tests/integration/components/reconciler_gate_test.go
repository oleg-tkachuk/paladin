//go:build integration

package components

import (
	"context"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// The reconciler deletes bytes that broke their object's registration. That
// is a write of bytes, so a draining or disabled backend defers it: the probe
// answers the gate's refusal, calls nothing, and the object stays PENDING.

// mismatchedHead answers every HEAD with bytes one longer than registered, and
// counts the deletes it is asked for.
type mismatchedHead struct {
	noCopy
	deletes int
}

func (h *mismatchedHead) Head(context.Context, string, string, uuid.UUID, string, string, string) (string, int64, string, string, error) {
	return "etag", registeredSize + 1, "", "", nil
}

func (h *mismatchedHead) DeleteObject(context.Context, string, string, uuid.UUID, string, string) error {
	h.deletes++
	return nil
}

// The lines the reconciler defers work with. A draining backend admits the
// HEAD, a read, and refuses the delete that follows it; a disabled one
// refuses the HEAD itself, so the object is never scanned for this tick.
const (
	lineDeleteDeferred = "the bucket gate refuses deleting the mismatched bytes; the object stays pending until it admits it"
	lineBucketDeferred = "the bucket gate refuses this bucket; its work is deferred until the gate admits it"
)

// reconcilerDeferral is the line the reconciler defers r's work with.
func (r backendRefusal) reconcilerDeferral() string {
	if r.column == "enabled" {
		return lineBucketDeferred
	}
	return lineDeleteDeferred
}

func TestReconcilerDefersAMismatchedDeleteOnABackendTheGateRefuses(t *testing.T) {
	t.Parallel()
	for _, refusal := range backendRefusals {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			pool := startPostgres(t)
			f := seedFixture(t, ctx, pool)
			objectID := seedPendingObject(t, ctx, pool, f)
			backend := fixtureBackend(t, ctx, pool, f)
			refusal.apply(t, ctx, pool, backend, true)

			head := &mismatchedHead{}
			probe := adapters.NewReconcilerProbe(sqlc.New(pool), head)
			if err := probe.DeleteByObjectID(ctx, objectID); !bucketgate.Refused(err) {
				t.Fatalf("DeleteByObjectID on a %s backend = %v, want the gate's refusal", refusal.name, err)
			}
			if head.deletes != 0 {
				t.Fatalf("%d storage deletes on a %s backend, want none", head.deletes, refusal.name)
			}

			// The same object, overdue, on the refused backend.
			mustExec(t, ctx, pool,
				`UPDATE objects SET size_bytes = $2, presign_expires_at = now() - interval '48 hours' WHERE id = $1`,
				objectID, registeredSize)
			logs := reconcileLogged(t, pool, probe)
			requireLogged(t, logs, refusal.reconcilerDeferral())
			if head.deletes != 0 {
				t.Errorf("the reconciler deleted bytes on a %s backend", refusal.name)
			}
			if n := logs.FilterMessage("failed to delete mismatched object bytes").Len(); n != 0 {
				t.Errorf("a deferral was logged as %d failures", n)
			}
			var state string
			if err := pool.QueryRow(ctx, `SELECT state FROM objects WHERE id = $1`, objectID).Scan(&state); err != nil {
				t.Fatalf("read object: %v", err)
			}
			if state != "PENDING" {
				t.Errorf("object state = %s on a %s backend, want it left PENDING", state, refusal.name)
			}

			refusal.apply(t, ctx, pool, backend, false)
			if err := probe.DeleteByObjectID(ctx, objectID); err != nil {
				t.Fatalf("DeleteByObjectID once writable = %v", err)
			}
			if head.deletes != 1 {
				t.Errorf("%d storage deletes once writable, want 1", head.deletes)
			}
		})
	}
}
